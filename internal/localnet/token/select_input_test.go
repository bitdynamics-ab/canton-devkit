package token

import (
	"strings"
	"testing"
)

// TestSelectInputHoldings_ExactDecimalSum pins C1: amounts round-trip
// exactly with big.Rat-based arithmetic. The old big.Float path
// returned "0.30000000000000004" for 0.1 + 0.2 because big.Float
// uses a binary mantissa; that silently misreports cash amounts.
func TestSelectInputHoldings_ExactDecimalSum(t *testing.T) {
	holdings := []holdingRef{
		{ContractID: "c1", Amount: "0.1"},
		{ContractID: "c2", Amount: "0.2"},
	}
	picked, sum, err := selectInputHoldings(holdings, "0.3")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(picked) != 2 {
		t.Fatalf("picked = %d, want 2", len(picked))
	}
	// FloatString pads to the larger of (input scale, 10), so 0.3
	// renders as "0.3000000000". Trim trailing zeros for the equality
	// check — the important property is "no binary float drift", not
	// the exact padding width.
	if got := strings.TrimRight(strings.TrimRight(sum, "0"), "."); got != "0.3" {
		t.Errorf("sum = %q, want exactly 0.3 (no binary float drift)", sum)
	}
}

// TestSelectInputHoldings_HighPrecisionRoundTrip pins that V2's
// 18-decimal-place test token amounts survive selectInputHoldings
// without precision loss. Sentinel value chosen so the 18th digit
// differs from a big.Float-rounded form.
func TestSelectInputHoldings_HighPrecisionRoundTrip(t *testing.T) {
	holdings := []holdingRef{
		{ContractID: "c1", Amount: "1.123456789012345678"},
	}
	_, sum, err := selectInputHoldings(holdings, "1.123456789012345678")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !strings.HasPrefix(sum, "1.123456789012345678") {
		t.Errorf("sum %q must round-trip the 18-dp input exactly", sum)
	}
}

// TestSelectInputHoldings_InsufficientFunds covers the error path so
// the new big.Rat formatter is exercised on the negative branch too.
func TestSelectInputHoldings_InsufficientFunds(t *testing.T) {
	holdings := []holdingRef{{ContractID: "c1", Amount: "1"}}
	_, _, err := selectInputHoldings(holdings, "2")
	if err == nil {
		t.Fatal("expected insufficient-funds error")
	}
}
