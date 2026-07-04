package localnet

import (
	"bytes"
	"strings"
	"testing"
)

// TestUpProfileFlagHelp pins the --profile help text so the per-
// component prometheus/grafana flags surface to users who run
// `localnet up --help`. The text is the discoverability contract;
// without it users couldn't find the new flags.
func TestUpProfileFlagHelp(t *testing.T) {
	cmd := buildUp()
	flag := cmd.Flag("profile")
	if flag == nil {
		t.Fatal("expected --profile flag on `up`")
	}
	usage := flag.Usage
	for _, expected := range []string{"prometheus", "grafana", "observability"} {
		if !strings.Contains(usage, expected) {
			t.Errorf("--profile help missing %q reference; users won't find the flag.\nGot: %s", expected, usage)
		}
	}
}

// TestUpProfileFlagParses ensures the cobra --profile flag accepts
// each of the four observability combinations end-to-end. This
// catches accidental flag-type changes (e.g. switching from
// StringSlice to StringArray would silently break comma-separated
// invocations like `--profile prometheus,grafana` documented in
// the CLI help).
func TestUpProfileFlagParses(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{"none", []string{"--name", "alpha"}, nil},
		{"none (positional)", []string{"alpha"}, nil},
		{"prometheus only", []string{"--name", "a", "--profile", "prometheus"}, []string{"prometheus"}},
		{"grafana only", []string{"--name", "a", "--profile", "grafana"}, []string{"grafana"}},
		{"both via repeat", []string{"--name", "a", "--profile", "prometheus", "--profile", "grafana"},
			[]string{"prometheus", "grafana"}},
		{"both via comma", []string{"--name", "a", "--profile", "prometheus,grafana"},
			[]string{"prometheus", "grafana"}},
		{"legacy umbrella", []string{"--name", "a", "--profile", "observability"}, []string{"observability"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildUp()
			// Don't actually run — just parse.
			cmd.RunE = nil
			if err := cmd.ParseFlags(tc.argv); err != nil {
				t.Fatalf("parse %v: %v", tc.argv, err)
			}
			got, err := cmd.Flags().GetStringSlice("profile")
			if err != nil {
				t.Fatalf("get profile: %v", err)
			}
			if !equalStrings(got, tc.want) {
				t.Errorf("argv=%v → profiles=%v; want %v", tc.argv, got, tc.want)
			}
		})
	}
}

// TestUpHasNoStartAlias confirms `start` is NOT an alias for `up`:
// `start` is now its own command (intelligent compose-start with an
// up fallback), so aliasing it here would shadow that command.
func TestUpHasNoStartAlias(t *testing.T) {
	cmd := buildUp()
	for _, a := range cmd.Aliases {
		if a == "start" {
			t.Fatal("`up` must not alias 'start' — it's a standalone command now")
		}
	}
}

// TestUpRejectsPositionalAndFlag confirms that providing the name
// both as a positional arg and --name is an error.
func TestUpRejectsPositionalAndFlag(t *testing.T) {
	cmd := buildUp()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"mynet", "--name", "mynet"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when both positional and --name are provided")
	}
	if !strings.Contains(out.String(), "not both") {
		t.Errorf("expected 'not both' error, got %q", out.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
