package token

import "testing"

// TestAddDecimal covers the big.Int-aligned fractional addition used to
// sum holdings per party — previously untested. The cases pin
// scale-alignment (operands with different fractional lengths), carries
// across the decimal point, and the empty-as-zero contract.
func TestAddDecimal(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"1", "2", "3"},
		{"0", "0", "0"},
		{"", "5", "5"},        // empty operand is zero
		{"5", "", "5"},        //
		{"1.5", "2.5", "4.0"}, // carry into the integer part, scale kept
		{"0.1", "0.2", "0.3"},
		{"1.05", "2.005", "3.055"},  // different scales: 2 vs 3 frac digits
		{"2.005", "1.05", "3.055"},  // commutative ordering of scales
		{"1.999", "0.001", "2.000"}, // fractional carry ripples to integer
		{"100", "0.5", "100.5"},     // integer + fraction
		{"0.50", "0.50", "1.00"},    // trailing-zero scale preserved
		{"999999999999999999", "1", "1000000000000000000"}, // beyond int64
	}
	for _, tc := range cases {
		got, err := addDecimal(tc.a, tc.b)
		if err != nil {
			t.Errorf("addDecimal(%q,%q) error: %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("addDecimal(%q,%q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAddDecimal_InvalidOperand(t *testing.T) {
	if _, err := addDecimal("abc", "1"); err == nil {
		t.Error("expected error for a non-numeric operand")
	}
}

// TestValidateAmount pins the local input guard that keeps "abc" /
// "1.2e5" / "0" from masquerading as ErrNeedsV2LocalNet.
func TestValidateAmount(t *testing.T) {
	good := []string{"1", "0.5", "1000", "123.456"}
	for _, s := range good {
		if err := validateAmount("mint", s); err != nil {
			t.Errorf("validateAmount(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "abc", "1.2e5", "-5", "+5", "0", "0.0", "1.2.3", "1,000"}
	for _, s := range bad {
		if err := validateAmount("mint", s); err == nil {
			t.Errorf("validateAmount(%q) = nil, want error", s)
		}
	}
}

// (TestValidatePartyID lives in validate_test.go, which owns
// validatePartyID; removed the duplicate here on merge to main.)
