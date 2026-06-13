package handlers

import (
	"strings"
	"testing"

	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
)

// TestStructuralDiff_PackageLevel exercises the hand-rolled structural
// diff on synthetic Info values. We build the trees via the exported
// cdkdar.PackageContents type rather than crafting real DAR bytes —
// the diff comparator is structure-only and never inspects raw bytes.
func TestStructuralDiff_PackageLevel(t *testing.T) {
	a := &cdkdar.Info{
		MainPackageID: "aaaa",
		Packages: []*cdkdar.PackageMeta{
			{
				IsMain:    true,
				PackageID: "aaaa",
				Name:      "demo",
				Version:   "1",
				Contents: &cdkdar.PackageContents{
					Modules: []cdkdar.ModuleContents{
						{
							Name: "M",
							Templates: []cdkdar.TemplateContents{
								{Name: "T1", Choices: []string{"C1", "C2"}},
							},
							Interfaces: []cdkdar.IfaceContents{
								{Name: "I1", Choices: []string{"X"}, Methods: []string{"m1"}},
							},
						},
					},
				},
			},
		},
	}
	b := &cdkdar.Info{
		MainPackageID: "bbbb",
		Packages: []*cdkdar.PackageMeta{
			{
				IsMain:    true,
				PackageID: "bbbb",
				Name:      "demo",
				Version:   "2",
				Contents: &cdkdar.PackageContents{
					Modules: []cdkdar.ModuleContents{
						{
							Name: "M",
							Templates: []cdkdar.TemplateContents{
								{Name: "T1", Choices: []string{"C1", "C3"}}, // C2 dropped, C3 added
								{Name: "T2", Choices: []string{"C4"}},       // new template
							},
							Interfaces: []cdkdar.IfaceContents{
								{Name: "I1", Choices: []string{"X", "Y"}, Methods: []string{"m1", "m2"}},
							},
						},
						{Name: "N"}, // new empty module
					},
				},
			},
		},
	}

	diff := structuralDiff(a, b)

	// Module set delta.
	modsAdded := diff["modules_added"].([]string)
	if len(modsAdded) != 1 || modsAdded[0] != "N" {
		t.Errorf("modules_added: want [N], got %v", modsAdded)
	}
	if rem := diff["modules_removed"].([]string); len(rem) != 0 {
		t.Errorf("modules_removed: want [], got %v", rem)
	}

	// Template additions/removals/changes.
	tplAdded := diff["templates_added"].([]map[string]any)
	if len(tplAdded) != 1 || tplAdded[0]["name"] != "T2" {
		t.Errorf("templates_added: want T2, got %v", tplAdded)
	}
	tplChanged := diff["templates_changed"].([]map[string]any)
	if len(tplChanged) != 1 {
		t.Fatalf("templates_changed: want 1, got %d (%v)", len(tplChanged), tplChanged)
	}
	if tplChanged[0]["name"] != "T1" {
		t.Errorf("changed template name: %v", tplChanged[0]["name"])
	}
	added := tplChanged[0]["choices_added"].([]string)
	removed := tplChanged[0]["choices_removed"].([]string)
	if len(added) != 1 || added[0] != "C3" {
		t.Errorf("choices_added: %v", added)
	}
	if len(removed) != 1 || removed[0] != "C2" {
		t.Errorf("choices_removed: %v", removed)
	}

	// Interface change picks up both choice and method additions.
	ifChanged := diff["interfaces_changed"].([]map[string]any)
	if len(ifChanged) != 1 {
		t.Fatalf("interfaces_changed: want 1, got %d", len(ifChanged))
	}
	if c := ifChanged[0]["choices_added"].([]string); len(c) != 1 || c[0] != "Y" {
		t.Errorf("if choices_added: %v", c)
	}
	if m := ifChanged[0]["methods_added"].([]string); len(m) != 1 || m[0] != "m2" {
		t.Errorf("if methods_added: %v", m)
	}
}

// TestLooksLikePackageID guards the cheap hex-id validator. 64
// lowercase hex passes; anything else fails. This is the gate
// every package-id path segment flows through before reaching
// gRPC.
func TestLooksLikePackageID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"abc", false},
		{"a" + strings.Repeat("0", 64), false}, // 65 chars
		{strings.Repeat("0", 64), true},
		{strings.Repeat("A", 64), false}, // uppercase rejected
		{strings.Repeat("g", 64), false}, // non-hex
	}
	for _, c := range cases {
		if got := looksLikePackageID(c.in); got != c.want {
			t.Errorf("looksLikePackageID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
