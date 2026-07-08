package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestDetectMode covers the direct-vs-DPM resolution and marker stripping.
func TestDetectMode(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMode InvocationMode
		wantArgs []string
	}{
		{"direct localnet", []string{"localnet", "up"}, Direct, []string{"localnet", "up"}},
		{"via dpm", []string{"--via-dpm", "localnet", "up"}, ViaDPM, []string{"localnet", "up"}},
		{"nil args", nil, Direct, nil},
		{"marker only", []string{"--via-dpm"}, ViaDPM, []string{}},
		// Only a leading marker counts; a stray one further down is left
		// alone for the flag parser to reject.
		{"non-leading marker untouched", []string{"localnet", "--via-dpm"}, Direct, []string{"localnet", "--via-dpm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, args := detectMode(tc.args)
			if mode != tc.wantMode {
				t.Errorf("mode = %v, want %v", mode, tc.wantMode)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

func TestDisplayName(t *testing.T) {
	if got := Direct.displayName(); got != "canton-devkit" {
		t.Errorf("Direct.displayName() = %q, want canton-devkit", got)
	}
	if got := ViaDPM.displayName(); got != "dpm" {
		t.Errorf("ViaDPM.displayName() = %q, want dpm", got)
	}
}

// TestLocalnetHelpUsesDisplayName: the localnet help header must reflect
// how the binary was launched — `canton-devkit localnet …` directly,
// `dpm localnet …` under DPM.
func TestLocalnetHelpUsesDisplayName(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, "test", "").Run([]string{"localnet", "--help"})
		if code != 0 {
			t.Fatalf("exit = %d, stderr=%q", code, errb.String())
		}
		if !strings.Contains(out.String(), "canton-devkit localnet") {
			t.Fatalf("direct help should say 'canton-devkit localnet', got %q", out.String())
		}
		if strings.Contains(out.String(), "dpm localnet") {
			t.Fatalf("direct help should not mention 'dpm localnet', got %q", out.String())
		}
	})
	t.Run("via dpm", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, "test", "").Run([]string{"--via-dpm", "localnet", "--help"})
		if code != 0 {
			t.Fatalf("exit = %d, stderr=%q", code, errb.String())
		}
		if !strings.Contains(out.String(), "dpm localnet") {
			t.Fatalf("dpm help should say 'dpm localnet', got %q", out.String())
		}
	})
}

// TestDARProjectFlagVisibilityByMode: `--project` is a normal visible flag
// on `dar build-upload` directly, but hidden under DPM (where the cwd is
// always the Daml project root). It stays functional either way.
func TestDARProjectFlagVisibilityByMode(t *testing.T) {
	t.Run("direct shows --project", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, "test", "").Run([]string{"localnet", "dar", "build-upload", "--help"})
		if code != 0 {
			t.Fatalf("exit = %d, stderr=%q", code, errb.String())
		}
		if !strings.Contains(out.String(), "--project") {
			t.Fatalf("direct build-upload help should list --project, got %q", out.String())
		}
	})
	t.Run("via dpm hides --project", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, "test", "").Run([]string{"--via-dpm", "localnet", "dar", "build-upload", "--help"})
		if code != 0 {
			t.Fatalf("exit = %d, stderr=%q", code, errb.String())
		}
		if strings.Contains(out.String(), "--project") {
			t.Fatalf("dpm build-upload help should hide --project, got %q", out.String())
		}
	})
}
