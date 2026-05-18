package localnet

import "testing"

func TestValidateName(t *testing.T) {
	valid := []string{"alice", "alice-net", "Test123", "a", "a-b-c"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("expected %q valid, got %v", n, err)
		}
	}

	invalid := []string{"", "-alice", "alice-", "has space", "has_underscore", "slash/in", string(make([]byte, 64))}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestIsValidName(t *testing.T) {
	// isValidName is the lowercase predicate; ValidateName wraps it
	// with a richer error. Keep this test so a regression in the
	// predicate fails loudly without going through ValidateName.
	if !isValidName("alice") {
		t.Error("alice should validate")
	}
	if isValidName("") {
		t.Error("empty should not validate")
	}
	if isValidName("a b") {
		t.Error("spaces should not validate")
	}
}
