package dar

import (
	"fmt"
	"path"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// PackageMeta is everything we surface about one .dalf inside a DAR.
// Populated by combining the .dalf filename, the on-the-wire archive
// envelope, and (for the main package) the DAR manifest's Name field.
type PackageMeta struct {
	// PackageID is the SHA256 hash of the ArchivePayload bytes,
	// hex-encoded. This is the canonical identifier Daml ledgers use
	// to reference a package. Read directly from the envelope's
	// `hash` field; cross-checked against the .dalf filename's
	// trailing 64 hex chars.
	PackageID string
	// Name is the parsed package name from the .dalf filename
	// (everything before the final `-<64-hex-id>.dalf`). For
	// daml-prim/stdlib internal packages the name is multi-segment
	// (e.g. "daml-stdlib-DA-Action-State-Type"), which is fine.
	Name string
	// Version is the version segment of the filename if one is
	// present (e.g. "1.0.0" in "splice-foo-1.0.0-<hash>.dalf").
	// Empty when the filename doesn't follow the versioned scheme
	// (most internal daml-prim modules).
	Version string
	// LFMajor is "1" or "2" depending on which oneof tag is set in
	// the ArchivePayload (daml_lf_1 = field 1, daml_lf_2 = field 2).
	// Empty if neither was found (corrupt or future LF version).
	LFMajor string
	// LFMinor is the `minor` string from ArchivePayload (e.g. "0",
	// "1", "dev").
	LFMinor string
	// SizeBytes is the on-disk size of this .dalf inside the DAR ZIP.
	SizeBytes int64
	// SHA256 is the hex-encoded SHA256 of the entire .dalf file.
	// Distinct from PackageID (which hashes only the inner payload).
	// Useful for tamper detection / cache verification.
	SHA256 string
	// IsMain is true for the package the manifest's `Main-Dalf`
	// points at; false for dependencies.
	IsMain bool

	// Contents is the deep Daml-LF view — modules, templates (+ their
	// choices), interfaces, data types (BIT-115). Populated for LF2
	// packages; nil for LF1 or unparseable archives. Surfaced by
	// `dar info --deep`.
	Contents *PackageContents `json:"contents,omitempty"`
}

// LFVersion returns the human-readable Daml-LF version like "2.1" or
// "1.dev". Returns "?" if the envelope couldn't be parsed.
func (p *PackageMeta) LFVersion() string {
	if p.LFMajor == "" {
		return "?"
	}
	if p.LFMinor == "" {
		return p.LFMajor
	}
	return p.LFMajor + "." + p.LFMinor
}

// parseDalfFilename extracts (name, version, hash) from a .dalf path.
// Filename shapes seen in real DARs:
//
//	splice-api-token-metadata-v1-1.0.0-<64-hex>.dalf       (versioned package)
//	daml-prim-<64-hex>.dalf                                 (unversioned internal)
//	daml-stdlib-DA-Action-State-Type-<64-hex>.dalf          (unversioned internal,
//	                                                         hyphenated name)
//	daml-stdlib-3.3.0.20250502.13767.0-<64-hex>.dalf        (versioned stdlib root)
//
// The hash is always the trailing 64-hex segment before the `.dalf`
// extension. Whatever's left is the package name (with the optional
// version tail). We split a final segment as version when it matches
// the semver-ish shape `digit(.digit)+(extras)`.
func parseDalfFilename(p string) (name, version, hash string) {
	base := path.Base(p)
	base = strings.TrimSuffix(base, ".dalf")

	// Hash is the last hyphen-separated segment of length 64 hex.
	if i := strings.LastIndexByte(base, '-'); i >= 0 {
		candidate := base[i+1:]
		if len(candidate) == 64 && isHex(candidate) {
			hash = candidate
			base = base[:i]
		}
	}

	// Try to peel off a version. A "version" segment is one that
	// starts with a digit and contains only digits, dots, and
	// occasionally identifiers like "snapshot.20250502". We use a
	// strict-ish rule: split on the last hyphen and check the right
	// side starts with a digit.
	if i := strings.LastIndexByte(base, '-'); i >= 0 {
		candidate := base[i+1:]
		if len(candidate) > 0 && candidate[0] >= '0' && candidate[0] <= '9' {
			version = candidate
			base = base[:i]
		}
	}
	name = base
	return
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// envelopeFields are the wire tags we look for inside the outer Archive
// message. The proto schema isn't published in digital-asset/daml's
// `main` branch any more, so these were determined empirically from
// real Splice DARs:
//
//	field 1: HashFunction hash_function    (varint enum; usually omitted = SHA256 default)
//	field 3: bytes        payload          (serialised ArchivePayload)
//	field 4: string       hash             (the package id as 64-hex string)
//
// Fields 2, 5+ exist in some Daml SDK versions but we don't need them.
// Verified: SHA256(field-3 bytes) == field-4 string for every package
// in the four Splice DARs we tested against.
const (
	archiveFieldHashFn  = 1
	archiveFieldPayload = 3
	archiveFieldHash    = 4
)

// payloadFields are the wire tags inside ArchivePayload. These were
// also determined empirically (the schema isn't on digital-asset/daml's
// `main` branch any more):
//
//	field 3: string  minor       (LF minor version, e.g. "1", "dev")
//	field 1: bytes   daml_lf_1   (serialised legacy LF1 package — by inference)
//	field 4: bytes   daml_lf_2   (serialised current LF2 package — verified on Splice 0.6.4 DARs)
//
// "By inference" for daml_lf_1 because we have no LF1 DAR to test
// against. The mapping field-1→"1", field-4→"2" matches the JSON
// emitted by `damlc inspect --json`, which renders the oneof as
// `{"Sum": {"daml_lf_2": "<base64>"}}` for our DARs.
const (
	payloadFieldDamlLf1 = 1
	payloadFieldMinor   = 3
	payloadFieldDamlLf2 = 4
)

// decodeArchiveEnvelope reads the outer Archive message via wire
// scanning — no .proto schema needed. Pulls out the package id and
// recursively decodes the ArchivePayload for the LF version.
//
// Resilient to:
//   - Unknown future fields (skipped via protowire.ConsumeFieldValue).
//   - Re-ordered or repeated fields (proto3 allows either).
//
// Returns (packageID, lfMajor, lfMinor, error).
func decodeArchiveEnvelope(data []byte) (packageID, lfMajor, lfMinor string, err error) {
	var payload []byte
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", "", "", fmt.Errorf("tag: %w", protowire.ParseError(n))
		}
		data = data[n:]

		switch int(num) {
		case archiveFieldHash:
			v, n2 := protowire.ConsumeString(data)
			if n2 < 0 {
				return "", "", "", fmt.Errorf("hash field: %w", protowire.ParseError(n2))
			}
			packageID = v
			data = data[n2:]
		case archiveFieldPayload:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return "", "", "", fmt.Errorf("payload field: %w", protowire.ParseError(n2))
			}
			payload = append(payload[:0], v...) // copy because data underlies it
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return "", "", "", fmt.Errorf("skip field %d: %w", num, protowire.ParseError(n2))
			}
			data = data[n2:]
		}
	}

	if len(payload) == 0 {
		return packageID, "", "", nil
	}

	// Recurse into ArchivePayload to find LF major (which oneof tag
	// is set) and minor.
	for len(payload) > 0 {
		num, typ, n := protowire.ConsumeTag(payload)
		if n < 0 {
			return packageID, "", "", fmt.Errorf("payload tag: %w", protowire.ParseError(n))
		}
		payload = payload[n:]
		switch int(num) {
		case payloadFieldDamlLf1:
			lfMajor = "1"
			// We don't decode the inner payload — skip its bytes.
			n2 := protowire.ConsumeFieldValue(num, typ, payload)
			if n2 < 0 {
				return packageID, lfMajor, "", fmt.Errorf("skip lf1: %w", protowire.ParseError(n2))
			}
			payload = payload[n2:]
		case payloadFieldDamlLf2:
			lfMajor = "2"
			n2 := protowire.ConsumeFieldValue(num, typ, payload)
			if n2 < 0 {
				return packageID, lfMajor, "", fmt.Errorf("skip lf2: %w", protowire.ParseError(n2))
			}
			payload = payload[n2:]
		case payloadFieldMinor:
			v, n2 := protowire.ConsumeString(payload)
			if n2 < 0 {
				return packageID, lfMajor, "", fmt.Errorf("minor: %w", protowire.ParseError(n2))
			}
			lfMinor = v
			payload = payload[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, payload)
			if n2 < 0 {
				return packageID, lfMajor, "", fmt.Errorf("skip payload field %d: %w", num, protowire.ParseError(n2))
			}
			payload = payload[n2:]
		}
	}

	return packageID, lfMajor, lfMinor, nil
}
