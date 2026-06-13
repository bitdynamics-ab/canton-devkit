package localnet

import (
	"bytes"
	"strings"
	"testing"
)

// TestRestart_RejectsMalformedService pins the format-validation
// guard the review asked for: a garbage --service value is rejected
// up front (no docker subprocess) with a friendly message, instead
// of passing straight to `docker compose restart`.
func TestRestart_RejectsMalformedService(t *testing.T) {
	cmd := buildRestart()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	// A space + slash are outside validServiceArg's character class.
	cmd.SetArgs([]string{"--name", "dev", "--service", "bad name/../etc"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for malformed --service")
	}
	if !strings.Contains(errb.String(), "invalid --service") {
		t.Errorf("expected friendly invalid-service message, got stderr=%q", errb.String())
	}
}

// TestRestart_RequiresName confirms the instance name is mandatory
// (via either positional arg or --name flag).
func TestRestart_RequiresName(t *testing.T) {
	cmd := buildRestart()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--service", "canton"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when name is omitted")
	}
}

// TestRestart_PositionalName confirms the instance name can be
// passed as a positional argument.
func TestRestart_PositionalName(t *testing.T) {
	cmd := buildRestart()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	// "dev" will fail at ValidateName → RunRestart (no registry),
	// but should NOT fail at "name required" — that's the assertion.
	cmd.SetArgs([]string{"dev"})
	err := cmd.Execute()
	// We expect a runtime error (no registry), not a "name required" error.
	if err != nil && strings.Contains(errb.String(), "required") {
		t.Errorf("positional name was not resolved; stderr=%q", errb.String())
	}
}
