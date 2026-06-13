package dar

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
)

// Info is the high-level surface for a DAR. Returned by Open.
// JSON tags are stable — `dar info --format json` and `dar diff` both
// serialise this struct.
type Info struct {
	// Path is the absolute path of the .dar file we read.
	Path string `json:"path"`
	// Manifest is the parsed META-INF/MANIFEST.MF.
	Manifest *Manifest `json:"manifest"`
	// MainPackageID is the PackageID of the main package (the one
	// the manifest's Main-Dalf points at). Cached here for quick
	// access without searching Packages.
	MainPackageID string `json:"main_package_id"`
	// Packages is every .dalf inside the DAR, main package first.
	Packages []*PackageMeta `json:"packages"`
	// SHA256 is the hex digest of the whole .dar file. For tamper
	// detection.
	SHA256 string `json:"sha256"`
}

// MainPackage returns the main package metadata (or nil if missing,
// which shouldn't happen for a well-formed DAR).
func (i *Info) MainPackage() *PackageMeta {
	for _, p := range i.Packages {
		if p.IsMain {
			return p
		}
	}
	return nil
}

// PackageByID looks up a package by its 64-hex ID. Returns nil if not
// found.
func (i *Info) PackageByID(id string) *PackageMeta {
	for _, p := range i.Packages {
		if p.PackageID == id {
			return p
		}
	}
	return nil
}

// Open reads a DAR file and returns a populated Info. The file is
// closed before returning.
func Open(path string) (*Info, error) {
	abs := path
	if absPath, err := os.Stat(path); err == nil {
		_ = absPath // we don't actually need abs for now
	}

	raw, err := ReadDARFile(path)
	if err != nil {
		return nil, err
	}
	info, err := decodeDARBytes(raw, abs)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// OpenBytes parses an in-memory DAR (no path). Used by the Web UI's
// inspect / diff handlers, which fetch DAR bytes via the Canton
// admin API rather than from disk. Path-shaped fields on the
// returned Info are left empty.
//
// The byte slice is assumed to be a complete DAR; it must already
// be under MaxDARBytes — callers fetching from gRPC are limited by
// the admin client's MaxCallRecvMsgSize so this isn't a new
// vector.
func OpenBytes(raw []byte) (*Info, error) {
	return decodeDARBytes(raw, "")
}

// decodeDARBytes is the shared core for Open and OpenBytes. Takes
// the full file bytes and an optional path for the Info.Path field.
func decodeDARBytes(raw []byte, path string) (*Info, error) {
	// Compute the file-level SHA256 in one pass while also reading
	// the zip via a second read. Cheaper than streaming once and
	// hashing twice; for DARs in the 100KB–10MB range this is fine.
	fileSum := sha256.Sum256(raw)

	zr, err := zip.NewReader(bytesReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("open zip %s: %w", path, err)
	}

	// Map filename → *zip.File for quick lookup.
	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	// Track cumulative decompressed bytes across every entry we read.
	// The compressed file is already capped by ReadDARFile, but DEFLATE
	// compresses up to ~1032:1 — so each entry is bounded individually
	// (MaxEntryDecompressedBytes) AND in aggregate (MaxTotalDecompressedBytes)
	// to defeat a zip bomb that stays under the per-entry cap by
	// spreading across many entries.
	remaining := MaxTotalDecompressedBytes

	// Parse the manifest.
	manifestFile, ok := entries["META-INF/MANIFEST.MF"]
	if !ok {
		return nil, fmt.Errorf("%s: missing META-INF/MANIFEST.MF", path)
	}
	manifestBytes, err := readZipEntry(manifestFile, entryCap(remaining))
	if err != nil {
		return nil, fmt.Errorf("read manifest of %s: %w", path, err)
	}
	remaining -= int64(len(manifestBytes))
	manifest, err := parseManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return nil, fmt.Errorf("parse manifest of %s: %w", path, err)
	}

	// Decode every .dalf listed in the manifest. We trust the manifest
	// over scanning the zip directly: the manifest dictates load order
	// for the ledger and may omit auxiliary files. (Verified on all
	// four real Splice DARs.)
	packages := make([]*PackageMeta, 0, len(manifest.Dalfs))
	for _, dalfPath := range manifest.Dalfs {
		f, ok := entries[dalfPath]
		if !ok {
			return nil, fmt.Errorf("%s: dalf %q listed in manifest but missing from archive", path, dalfPath)
		}
		pkg, n, err := readDalf(f, dalfPath, entryCap(remaining))
		if err != nil {
			return nil, fmt.Errorf("decode %s/%s: %w", path, dalfPath, err)
		}
		remaining -= n
		pkg.IsMain = (dalfPath == manifest.MainDalf)
		packages = append(packages, pkg)
	}

	// Dependency order from the manifest is preserved; main first.
	// Within deps we sort lexicographically by name + version for
	// stable diff output.
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].IsMain != packages[j].IsMain {
			return packages[i].IsMain // main first
		}
		if packages[i].Name != packages[j].Name {
			return packages[i].Name < packages[j].Name
		}
		return packages[i].Version < packages[j].Version
	})

	info := &Info{
		Path:     path,
		Manifest: manifest,
		Packages: packages,
		SHA256:   hex.EncodeToString(fileSum[:]),
	}
	if main := info.MainPackage(); main != nil {
		info.MainPackageID = main.PackageID
	}
	return info, nil
}

// entryCap clamps the per-entry decompressed ceiling to whatever is
// left of the cumulative budget, so a single oversize entry can't be
// admitted just because it fits the per-entry cap when the running
// total is nearly spent. Never returns negative.
func entryCap(remaining int64) int64 {
	if remaining < MaxEntryDecompressedBytes {
		if remaining < 0 {
			return 0
		}
		return remaining
	}
	return MaxEntryDecompressedBytes
}

// readDalf reads one .dalf entry under the decompressed-size cap,
// computes SHA256, decodes the archive envelope for the package id +
// LF version, and parses the filename for name + version. Returns the
// number of decompressed bytes consumed so the caller can debit the
// cumulative budget.
func readDalf(f *zip.File, dalfPath string, cap int64) (*PackageMeta, int64, error) {
	data, err := readZipEntry(f, cap)
	if err != nil {
		return nil, 0, err
	}
	sum := sha256.Sum256(data)

	pkgID, lfMajor, lfMinor, err := decodeArchiveEnvelope(data)
	if err != nil {
		return nil, 0, fmt.Errorf("envelope: %w", err)
	}

	name, version, hashFromName := parseDalfFilename(dalfPath)

	// Cross-check: if the filename's trailing hash and the envelope's
	// hash both exist, they should agree. Mismatches are highly
	// suspicious (re-packed DAR with wrong filename, tampered bytes,
	// etc.) and we surface them as errors so downstream tooling
	// doesn't quietly trust a wrong ID.
	if hashFromName != "" && pkgID != "" && hashFromName != pkgID {
		return nil, 0, fmt.Errorf("package id mismatch: filename %q says %s, envelope says %s",
			dalfPath, hashFromName, pkgID)
	}
	if pkgID == "" {
		pkgID = hashFromName
	}

	meta := &PackageMeta{
		PackageID: pkgID,
		Name:      name,
		Version:   version,
		LFMajor:   lfMajor,
		LFMinor:   lfMinor,
		SizeBytes: int64(len(data)),
		SHA256:    hex.EncodeToString(sum[:]),
	}
	// Deep Daml-LF inspection — best-effort; nil for LF1 or
	// unparseable archives. Cheap (microseconds) and small, so populate
	// it eagerly; `dar info --deep` decides whether to render it.
	if c, ok := InspectDalf(data); ok {
		meta.Contents = c
	}
	return meta, int64(len(data)), nil
}

// bytesReader is a tiny zero-alloc adapter so we can hand a []byte to
// zip.NewReader (which wants io.ReaderAt). bytes.NewReader exists but
// is in the stdlib's `bytes` package; the unqualified name collides if
// we shadow it. Keep this trivial wrapper to make intent obvious.
type bytesReader []byte

func (b bytesReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
