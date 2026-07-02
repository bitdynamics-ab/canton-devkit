package token

import (
	"strings"
	"testing"
)

// TestValidatePartyID guards the issuer-party check RunCreate applies.
func TestValidatePartyID(t *testing.T) {
	good := []string{"alice", "alice::1220abcd", "app_provider", "sv-1", "Bob#2"}
	for _, s := range good {
		if err := validatePartyID("issuer", s); err != nil {
			t.Errorf("validatePartyID(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", " ", "a b", "alice\n", "💀", "::nohint"}
	for _, s := range bad {
		if err := validatePartyID("issuer", s); err == nil {
			t.Errorf("validatePartyID(%q) = nil, want error", s)
		}
	}
}

// TestLooksLikeDecimal pins the grammar both the create wizard and the
// mint/transfer/burn amount guard share. The load-bearing case is the
// lone ".": accepting it would persist a bogus "." supply that breaks
// the holdings renderer and addDecimal's big.Int parse.
func TestLooksLikeDecimal(t *testing.T) {
	good := []string{"0", "123", "0.5", "123.456", "1000000", "00.10"}
	for _, s := range good {
		if !looksLikeDecimal(s) {
			t.Errorf("looksLikeDecimal(%q) = false, want true", s)
		}
	}
	// "." and ".." carry no digit; the rest are rejected sign /
	// exponent / multi-dot shapes.
	bad := []string{"", ".", "..", "-5", "+5", "1.2e3", "1.2.3", "abc", "1,000"}
	for _, s := range bad {
		if looksLikeDecimal(s) {
			t.Errorf("looksLikeDecimal(%q) = true, want false", s)
		}
	}
}

// TestValidateCreate_InitialSupplyBounds pins the supply guards: a lone
// dot, a zero supply, an over-precision (>38-digit) supply, and a
// supply whose fractional width exceeds the declared decimals are all
// rejected at the local gate rather than failing opaquely on-ledger.
func TestValidateCreate_InitialSupplyBounds(t *testing.T) {
	base := CreateOptions{
		Instance:      "demo",
		Name:          "Retail Token",
		Symbol:        "RTK",
		Decimals:      6,
		InitialSupply: "1000000",
		Issuer:        "alice::abc",
	}

	cases := []struct {
		name   string
		supply string
		dec    int
		want   string // substring; "" means must pass
	}{
		{"lone dot", ".", 6, "not a valid decimal"},
		{"empty handled earlier", "", 6, "initial supply is required"},
		{"zero", "0", 6, "must be greater than zero"},
		{"zero with dot", "0.00", 6, "must be greater than zero"},
		{"too many significant digits", strings.Repeat("9", 39), 0, "exceeding the 38-digit"},
		{"exactly 38 digits ok", strings.Repeat("9", 38), 0, ""},
		{"fractional exceeds decimals", "1.123", 2, "exceeding the declared decimals=2"},
		{"fractional within decimals ok", "1.12", 2, ""},
		{"plain integer ok", "1000000", 6, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			opts.InitialSupply = tc.supply
			opts.Decimals = tc.dec
			err := validateCreate(opts)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateCreate(supply=%q, decimals=%d) = %v, want nil",
						tc.supply, tc.dec, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateCreate(supply=%q, decimals=%d) = nil, want error containing %q",
					tc.supply, tc.dec, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
