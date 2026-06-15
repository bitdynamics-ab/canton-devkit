package localnet

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestObsComponentFlags_Selected(t *testing.T) {
	cases := []struct {
		name     string
		prom     bool
		graf     bool
		wantProm bool
		wantGraf bool
	}{
		{"neither flag means both", false, false, true, true},
		{"prometheus only", true, false, true, false},
		{"grafana only", false, true, false, true},
		{"both flags", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := obsComponentFlags{prometheus: tc.prom, grafana: tc.graf}
			p, g := f.selected()
			if p != tc.wantProm || g != tc.wantGraf {
				t.Errorf("selected() = (%v,%v), want (%v,%v)", p, g, tc.wantProm, tc.wantGraf)
			}
		})
	}
}

// seedObsInstance writes a registry entry for the observability CLI
// tests. ports lets a test simulate sidecars already running.
func seedObsInstance(t *testing.T, name string, status registry.Status, ports map[string]int) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = status
	if ports != nil {
		s.Ports = ports
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// TestObservabilityStatus_TextOff: status on an instance with no
// sidecars shows both off and the enable hint.
func TestObservabilityStatus_TextOff(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedObsInstance(t, "demo", registry.StatusRunning, map[string]int{})

	cmd := buildObservability()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"status", "--name", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v (stderr=%q)", err, errBuf.String())
	}
	// term.Section uppercases the title for display; match the section
	// label case-insensitively and the body rows verbatim.
	s := out.String()
	if !strings.Contains(strings.ToLower(s), "observability · demo") {
		t.Errorf("status text missing the section header\n%s", s)
	}
	for _, want := range []string{"Prometheus", "Grafana", "off", "enable with"} {
		if !strings.Contains(s, want) {
			t.Errorf("status text missing %q\n%s", want, s)
		}
	}
}

// TestObservabilityStatus_JSONOn: status --format json reflects the
// recorded ports and emits the Grafana deep link.
func TestObservabilityStatus_JSONOn(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedObsInstance(t, "demo", registry.StatusRunning, map[string]int{
		"prometheus_ui": 19090,
		"grafana_ui":    13000,
	})

	cmd := buildObservability()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"status", "--name", "demo", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status json: %v (stderr=%q)", err, errBuf.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if got["prometheus"] != true || got["grafana"] != true {
		t.Errorf("expected both on, got %v", got)
	}
	if got["grafana_url"] != "http://localhost:13000/d/canton-localnet-v1" {
		t.Errorf("grafana_url = %v, want deep link", got["grafana_url"])
	}
	if got["prometheus_ui"].(float64) != 19090 {
		t.Errorf("prometheus_ui = %v, want 19090", got["prometheus_ui"])
	}
}

// TestObservabilityEnable_RejectsNotRunning: enable on a stopped
// instance is a user error (exit 1) BEFORE any docker work — the
// toggle requires a live stack. Mirrors the UI's 409 NOT_RUNNING.
func TestObservabilityEnable_RejectsNotRunning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedObsInstance(t, "demo", registry.StatusStopped, map[string]int{})

	cmd := buildObservability()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"enable", "--name", "demo"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error enabling observability on a stopped instance")
	}
	var ec localnet.ExitCodeError
	if !errors.As(err, &ec) || int(ec) != localnet.ExitUserError {
		t.Fatalf("expected ExitUserError, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "is not running") {
		t.Errorf("expected not-running message, got %q", errBuf.String())
	}
}

// TestObservabilityEnable_UnknownInstance: enable against an unknown
// name is a user error with a helpful message.
func TestObservabilityEnable_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	cmd := buildObservability()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"enable", "--name", "ghost"})
	err := cmd.Execute()
	var ec localnet.ExitCodeError
	if !errors.As(err, &ec) || int(ec) != localnet.ExitUserError {
		t.Fatalf("expected ExitUserError for unknown instance, got %v (stderr=%q)", err, errBuf.String())
	}
}
