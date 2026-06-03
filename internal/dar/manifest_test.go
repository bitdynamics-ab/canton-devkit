package dar

import (
	"strings"
	"testing"
)

// Sample manifest taken verbatim from a Splice DAR (lines wrapped at 72
// bytes with a continuation space, just like the real thing). The
// `Dalfs:` value is shortened for the test but otherwise authentic.
const sampleManifest = `Manifest-Version: 1.0
Created-By: damlc
Name: splice-api-token-metadata-v1-1.0.0
Sdk-Version: 3.3.0-snapshot.20250502.13767.0.v2fc6c7e2
Main-Dalf: splice-api-token-metadata-v1-1.0.0-abc/splice-api-token-meta
 data-v1-1.0.0-abcdef.dalf
Dalfs: splice-api-token-metadata-v1-1.0.0-abc/splice-api-token-metadata
 -v1-1.0.0-abcdef.dalf, splice-api-token-metadata-v1-1.0.0-abc/daml-pri
 m-fedcba.dalf
Format: daml-lf
Encryption: non-encrypted
`

func TestParseManifest(t *testing.T) {
	m, err := parseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if m.Name != "splice-api-token-metadata-v1-1.0.0" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.SdkVersion != "3.3.0-snapshot.20250502.13767.0.v2fc6c7e2" {
		t.Errorf("SdkVersion = %q", m.SdkVersion)
	}
	// Continuation line: Main-Dalf is wrapped across two source lines.
	wantMain := "splice-api-token-metadata-v1-1.0.0-abc/splice-api-token-metadata-v1-1.0.0-abcdef.dalf"
	if m.MainDalf != wantMain {
		t.Errorf("MainDalf = %q, want %q", m.MainDalf, wantMain)
	}
	if len(m.Dalfs) != 2 {
		t.Errorf("Dalfs count = %d, want 2: %v", len(m.Dalfs), m.Dalfs)
	}
	if m.Format != "daml-lf" || m.Encryption != "non-encrypted" {
		t.Errorf("Format/Encryption: %q / %q", m.Format, m.Encryption)
	}
}

func TestParseManifest_MissingRequired(t *testing.T) {
	// No Name / Main-Dalf — should fail loudly.
	body := "Manifest-Version: 1.0\nCreated-By: damlc\n"
	_, err := parseManifest(strings.NewReader(body))
	if err == nil {
		t.Error("expected error for missing Name/Main-Dalf")
	}
}

func TestParseManifest_FallbackDalfs(t *testing.T) {
	// Some manifests only list Main-Dalf; parser seeds Dalfs from it.
	body := "Manifest-Version: 1.0\nName: foo-1.0\nMain-Dalf: foo/foo-deadbeef.dalf\n"
	m, err := parseManifest(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Dalfs) != 1 || m.Dalfs[0] != m.MainDalf {
		t.Errorf("Dalfs fallback failed: %v", m.Dalfs)
	}
}

func TestParseDalfFilename(t *testing.T) {
	cases := []struct {
		in           string
		wantName     string
		wantVersion  string
		wantHashSize int
	}{
		{
			"splice-api-token-metadata-v1-1.0.0/splice-api-token-metadata-v1-1.0.0-4ded6b668cb3b64f7a88a30874cd41c75829f5e064b3fbbadf41ec7e8363354f.dalf",
			"splice-api-token-metadata-v1", "1.0.0", 64,
		},
		{
			"splice-api-token-metadata-v1-1.0.0/daml-prim-54f85ebfc7dfae18f7d70370015dcc6c6792f60135ab369c44ae52c6fc17c274.dalf",
			"daml-prim", "", 64,
		},
		{
			"splice-api-token-metadata-v1-1.0.0/daml-stdlib-DA-Action-State-Type-a1fa18133ae48cbb616c4c148e78e661666778c3087d099067c7fe1868cbb3a1.dalf",
			"daml-stdlib-DA-Action-State-Type", "", 64,
		},
		{
			"splice-api-token-metadata-v1-1.0.0/daml-stdlib-3.3.0.20250502.13767.0-9d1a644e686435cf934f2f3f049138a424863b2a800130046dadc0c994ea5df3.dalf",
			"daml-stdlib", "3.3.0.20250502.13767.0", 64,
		},
	}
	for _, c := range cases {
		t.Run(c.wantName, func(t *testing.T) {
			n, v, h := parseDalfFilename(c.in)
			if n != c.wantName {
				t.Errorf("name = %q, want %q", n, c.wantName)
			}
			if v != c.wantVersion {
				t.Errorf("version = %q, want %q", v, c.wantVersion)
			}
			if len(h) != c.wantHashSize {
				t.Errorf("hash size = %d, want %d", len(h), c.wantHashSize)
			}
		})
	}
}
