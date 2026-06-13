package dar

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// TestVettingGlyph maps each verdict to its table glyph.
func TestVettingGlyph(t *testing.T) {
	cases := []struct {
		row  types.DARVettingRow
		want string
	}{
		{types.DARVettingRow{Role: "app-user", Vetted: true}, "✓"},
		{types.DARVettingRow{Role: "app-user", Vetted: false}, "✗"},
		{types.DARVettingRow{Role: "sv", Error: "port not recorded"}, "?"},
		// Error wins over a (stale) Vetted=true.
		{types.DARVettingRow{Role: "sv", Vetted: true, Error: "dial: x"}, "?"},
	}
	for _, c := range cases {
		if got := vettingGlyph(c.row); got != c.want {
			t.Errorf("vettingGlyph(%+v) = %q, want %q", c.row, got, c.want)
		}
	}
}

// TestVettingSummary renders the compact per-participant cell in the
// canonical order with role abbreviations.
func TestVettingSummary(t *testing.T) {
	parts := []types.DARVettingRow{
		{Role: "app-user", Vetted: true},
		{Role: "app-provider", Vetted: false},
		{Role: "sv", Error: "no JWT"},
	}
	got := vettingSummary(parts)
	want := "U:✓ P:✗ S:?"
	if got != want {
		t.Errorf("vettingSummary = %q, want %q", got, want)
	}
}

// TestPrintVettingDars covers the text renderer: header, one row per
// DAR, the legend, and the empty case.
func TestPrintVettingDars(t *testing.T) {
	var buf bytes.Buffer
	printVettingDars(&buf, []vettingDarRow{
		{
			MainPackageID: strings.Repeat("a", 64),
			Name:          "my-app",
			Version:       "1.0.0",
			Participants: []types.DARVettingRow{
				{Role: "app-user", Vetted: true},
				{Role: "app-provider", Vetted: true},
				{Role: "sv", Vetted: false},
			},
		},
	})
	out := buf.String()
	for _, want := range []string{"MAIN PACKAGE ID", "VETTING", "my-app", "1.0.0", "U:✓ P:✓ S:✗", "Legend:"} {
		if !strings.Contains(out, want) {
			t.Errorf("printVettingDars output missing %q\n%s", want, out)
		}
	}

	var empty bytes.Buffer
	printVettingDars(&empty, nil)
	if !strings.Contains(empty.String(), "No DARs uploaded.") {
		t.Errorf("empty printVettingDars = %q", empty.String())
	}
}

// TestList_VettingRequiresInstance pins the guard: `dar list
// --vetting` without --instance fails with a clear user error before
// any dial, because cross-participant vetting reads the registry.
func TestList_VettingRequiresInstance(t *testing.T) {
	conn := &connectFlags{} // no Instance set
	_, err := vettingRows(t.Context(), conn, nil)
	if err == nil {
		t.Fatal("vettingRows without --instance: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--instance") {
		t.Errorf("error %q should mention --instance", err.Error())
	}
}

// TestList_VettingFlagRegistered confirms the flag is wired onto the
// list command (surface-level guard so the flag can't silently
// disappear).
func TestList_VettingFlagRegistered(t *testing.T) {
	cmd := buildListUploaded()
	if cmd.Flags().Lookup("vetting") == nil {
		t.Fatal("dar list is missing the --vetting flag")
	}
}
