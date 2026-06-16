package registry

import "testing"

// TestRegistryEndpointVersioning pins that the transfer-instruction
// registry paths carry the Client's configured generation segment —
// v1 for CIP-0056 tokens (Amulet on a stable release), v2 by default —
// and that the instruction id is url-escaped.
func TestRegistryEndpointVersioning(t *testing.T) {
	v2, err := Dial(DialOptions{BaseURL: "http://x"}) // empty Version → v2 default
	if err != nil {
		t.Fatal(err)
	}
	v1, err := Dial(DialOptions{BaseURL: "http://x", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, got, want string
	}{
		{"v2 factory", v2.transferFactoryPath(), "/registry/transfer-instruction/v2/transfer-factory"},
		{"v1 factory", v1.transferFactoryPath(), "/registry/transfer-instruction/v1/transfer-factory"},
		{"v2 accept", v2.choiceContextPath("accept", "abc"), "/registry/transfer-instruction/v2/abc/choice-contexts/accept"},
		{"v1 accept", v1.choiceContextPath("accept", "abc"), "/registry/transfer-instruction/v1/abc/choice-contexts/accept"},
		{"v1 reject", v1.choiceContextPath("reject", "abc"), "/registry/transfer-instruction/v1/abc/choice-contexts/reject"},
		{"escapes id", v1.choiceContextPath("accept", "abc#1"), "/registry/transfer-instruction/v1/abc%231/choice-contexts/accept"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}
