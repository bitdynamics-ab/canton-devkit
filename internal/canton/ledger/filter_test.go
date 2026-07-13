package ledger

import (
	"strings"
	"testing"
)

// TestBuildTemplateFilters_PackageRef pins the package-selector
// normalisation Canton's LF-v2 Ledger API (Splice 0.6.x) requires: a
// bare package name is emitted as a "#name" reference the participant
// resolves by name, a "#name" or a concrete 64-hex package-id passes
// through unchanged, and the package-less "Module:Entity" form leaves
// PackageId empty. Both the CLI (`contracts`/`tx`) and the Web UI
// Explorer share this builder, so the fix lands on both surfaces.
func TestBuildTemplateFilters_PackageRef(t *testing.T) {
	hexID := strings.Repeat("a1", 32) // 64 lowercase-hex chars
	cases := []struct {
		name    string
		in      string
		wantPkg string
		wantMod string
		wantEnt string
	}{
		{
			name:    "2-part name-only leaves PackageId empty",
			in:      "Splice.Amulet:Amulet",
			wantPkg: "",
			wantMod: "Splice.Amulet",
			wantEnt: "Amulet",
		},
		{
			name:    "3-part package-name gains the # reference prefix",
			in:      "splice-amulet:Splice.Amulet:Amulet",
			wantPkg: "#splice-amulet",
			wantMod: "Splice.Amulet",
			wantEnt: "Amulet",
		},
		{
			name:    "3-part #name passes through unchanged",
			in:      "#splice-amulet:Splice.Amulet:Amulet",
			wantPkg: "#splice-amulet",
			wantMod: "Splice.Amulet",
			wantEnt: "Amulet",
		},
		{
			name:    "3-part hex package-id stays an exact pin",
			in:      hexID + ":Splice.Amulet:Amulet",
			wantPkg: hexID,
			wantMod: "Splice.Amulet",
			wantEnt: "Amulet",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := BuildTemplateFilters([]string{tc.in})
			if err != nil {
				t.Fatalf("BuildTemplateFilters(%q): %v", tc.in, err)
			}
			cum := f.GetCumulative()
			if len(cum) != 1 {
				t.Fatalf("want 1 cumulative filter, got %d", len(cum))
			}
			id := cum[0].GetTemplateFilter().GetTemplateId()
			if got := id.GetPackageId(); got != tc.wantPkg {
				t.Errorf("PackageId = %q, want %q", got, tc.wantPkg)
			}
			if got := id.GetModuleName(); got != tc.wantMod {
				t.Errorf("ModuleName = %q, want %q", got, tc.wantMod)
			}
			if got := id.GetEntityName(); got != tc.wantEnt {
				t.Errorf("EntityName = %q, want %q", got, tc.wantEnt)
			}
		})
	}
}

// TestIsHexPackageID guards the exact-pin vs package-name decision: only
// a 64-char hex string is a concrete package-id, so a bare name, a
// "#name", a wrong-length hex, or a non-hex 64-char string is treated as
// a name to reference instead.
func TestIsHexPackageID(t *testing.T) {
	hexID := strings.Repeat("a1", 32)
	cases := []struct {
		in   string
		want bool
	}{
		{hexID, true},
		{strings.ToUpper(hexID), true}, // uppercase hex still a package-id
		{"splice-amulet", false},
		{"#splice-amulet", false},
		{"", false},
		{hexID[:63], false},      // 63 chars
		{hexID + "0", false},     // 65 chars
		{"g" + hexID[1:], false}, // non-hex char
	}
	for _, tc := range cases {
		if got := isHexPackageID(tc.in); got != tc.want {
			t.Errorf("isHexPackageID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
