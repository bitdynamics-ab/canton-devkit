package dar

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Manifest is the parsed MANIFEST.MF inside a DAR. Field names match
// the Java manifest spec (RFC822-style headers with line continuations
// — a line starting with a single space is a continuation of the
// previous line).
//
// Only the fields DevKit cares about are surfaced; everything else is
// dropped on the floor. Add fields here as future commands need them.
type Manifest struct {
	// ManifestVersion is the "Manifest-Version" header (always "1.0"
	// in DAR files; kept for completeness).
	ManifestVersion string
	// CreatedBy is who emitted the manifest (typically "damlc").
	CreatedBy string
	// Name is the human-readable package name + version, e.g.
	// "splice-api-token-metadata-v1-1.0.0".
	Name string
	// SdkVersion is the SDK that built the DAR, e.g.
	// "3.3.0-snapshot.20250502.13767.0.v2fc6c7e2".
	SdkVersion string
	// MainDalf is the relative path of the main package's .dalf
	// inside the archive.
	MainDalf string
	// Dalfs is the ordered list of every .dalf path inside the
	// archive (main package first, then dependencies). Parsed from
	// the comma-separated "Dalfs:" header.
	Dalfs []string
	// Format should be "daml-lf".
	Format string
	// Encryption is typically "non-encrypted".
	Encryption string
}

// maxManifestLogicalLines bounds the number of logical (continuation-
// joined) header lines parseManifest will accumulate. The 16 MiB
// scanner buffer below only bounds a SINGLE token, so a hostile
// MANIFEST.MF made of millions of short lines would otherwise grow the
// `logical` slice without limit. A real DAR manifest has a handful of
// headers (and one long wrapped Dalfs: list); 100k logical lines is
// orders of magnitude above anything legitimate while still bounding
// the slice.
const maxManifestLogicalLines = 100_000

// parseManifest reads a MANIFEST.MF stream and returns a Manifest.
//
// The Java manifest format uses RFC822 headers but with two twists:
//   - Lines are wrapped at 72 bytes and continued on the next line
//     prefixed with a single space.
//   - Sections are separated by blank lines; we only consume the first
//     section (main attributes) since DAR manifests don't have
//     per-entry sections.
//
// We rejoin continuation lines first, then split on `:` per header.
func parseManifest(r io.Reader) (*Manifest, error) {
	// Step 1: rejoin continuation lines into "logical" lines.
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024) // some DARs have huge Dalfs: lists
	var logical []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break // end of main section
		}
		if strings.HasPrefix(line, " ") && len(logical) > 0 {
			logical[len(logical)-1] += line[1:]
			continue
		}
		if len(logical) >= maxManifestLogicalLines {
			return nil, fmt.Errorf("manifest has more than %d header lines (possible malicious input)", maxManifestLogicalLines)
		}
		logical = append(logical, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	// Step 2: split each logical line on the first `:`.
	m := &Manifest{}
	for _, line := range logical {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		switch key {
		case "Manifest-Version":
			m.ManifestVersion = val
		case "Created-By":
			m.CreatedBy = val
		case "Name":
			m.Name = val
		case "Sdk-Version":
			m.SdkVersion = val
		case "Main-Dalf":
			m.MainDalf = val
		case "Dalfs":
			// Comma-separated, with optional whitespace.
			for _, p := range strings.Split(val, ",") {
				if p = strings.TrimSpace(p); p != "" {
					m.Dalfs = append(m.Dalfs, p)
				}
			}
		case "Format":
			m.Format = val
		case "Encryption":
			m.Encryption = val
		}
	}

	if m.Name == "" || m.MainDalf == "" {
		return nil, fmt.Errorf("manifest missing required Name/Main-Dalf headers")
	}
	if len(m.Dalfs) == 0 {
		// Some manifests only list Main-Dalf and rely on the consumer
		// to enumerate other .dalf files from the archive. Seed with
		// the main entry so callers see at least one package.
		m.Dalfs = []string{m.MainDalf}
	}
	return m, nil
}
