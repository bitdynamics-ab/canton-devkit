// Package packaging guards the DPM component manifest template. The
// release workflow renders packaging/component.yaml.tmpl per platform by
// substituting the @@BINARY_PATH@@ token, then hands the result to
// `dpm publish component`. A typo in the template (bad YAML, renamed
// key, dropped exec-args) would only surface at release time, on a tag,
// against a real DPM CLI — too late and too rare to catch by hand.
//
// These tests render the template exactly the way the workflow does and
// assert the output is valid YAML with the structure DPM requires, for
// every platform in the build matrix. AGENTS.md: new code must be tested.
package packaging

import (
	"os"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// binaryPathToken mirrors the sed token the release workflow replaces:
//
//	sed "s|@@BINARY_PATH@@|bin/canton-devkit[.exe]|" component.yaml.tmpl
const binaryPathToken = "@@BINARY_PATH@@"

// renderedManifest is the subset of the component schema we assert on.
type renderedManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Commands []struct {
			Name     string   `yaml:"name"`
			Path     string   `yaml:"path"`
			Desc     string   `yaml:"desc"`
			ExecArgs []string `yaml:"exec-args"`
		} `yaml:"commands"`
	} `yaml:"spec"`
}

func TestComponentTemplateRendersValidYAMLPerPlatform(t *testing.T) {
	raw, err := os.ReadFile("component.yaml.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	tmpl := string(raw)
	if !strings.Contains(tmpl, binaryPathToken) {
		t.Fatalf("template no longer contains %s — the workflow's sed substitution would be a no-op", binaryPathToken)
	}

	// One case per build-matrix platform; Windows is the one that must
	// carry the .exe extension (DPM stat()s the literal path and does not
	// append it).
	cases := []struct {
		platform string
		binPath  string
	}{
		{"linux/amd64", "bin/canton-devkit"},
		{"darwin/arm64", "bin/canton-devkit"},
		{"windows/amd64", "bin/canton-devkit.exe"},
	}

	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			rendered := strings.ReplaceAll(tmpl, binaryPathToken, tc.binPath)
			if strings.Contains(rendered, binaryPathToken) {
				t.Fatalf("token still present after substitution")
			}

			var m renderedManifest
			if err := yaml.Unmarshal([]byte(rendered), &m); err != nil {
				t.Fatalf("rendered manifest is not valid YAML: %v\n---\n%s", err, rendered)
			}

			if m.APIVersion == "" {
				t.Error("apiVersion is empty")
			}
			if m.Kind != "Component" {
				t.Errorf("kind = %q, want Component", m.Kind)
			}
			if len(m.Spec.Commands) != 1 {
				t.Fatalf("want exactly one top-level command (collision-avoidance invariant), got %d", len(m.Spec.Commands))
			}
			cmd := m.Spec.Commands[0]
			if cmd.Name != "localnet" {
				t.Errorf("command name = %q, want localnet", cmd.Name)
			}
			if cmd.Path != tc.binPath {
				t.Errorf("command path = %q, want %q", cmd.Path, tc.binPath)
			}
			// exec-args MUST be exactly ["localnet"] — DPM doesn't pass the
			// registered command name into argv, so the binary relies on
			// this to dispatch into its localnet subtree (TestRunIsArgvOnly
			// locks the binary side of the same contract).
			if len(cmd.ExecArgs) != 1 || cmd.ExecArgs[0] != "localnet" {
				t.Errorf("exec-args = %v, want [localnet]", cmd.ExecArgs)
			}
		})
	}
}
