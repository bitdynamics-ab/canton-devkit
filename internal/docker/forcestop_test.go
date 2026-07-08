package docker

import (
	"context"
	"strings"
	"testing"
)

func argvContains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestForceStop_LabelOnly verifies the force teardown is by project label
// only: it must NOT pass the -f compose files or --env-file (whose
// interpolation is exactly what fails on a wedged instance), and it sets
// --timeout to SIGKILL stuck containers.
func TestForceStop_LabelOnly(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec) // deliberately HAS ComposeFiles + EnvFiles set
	if err := c.ForceStop(context.Background(), false); err != nil {
		t.Fatalf("ForceStop: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("want 1 docker call, got %d", len(rec.calls))
	}
	args := rec.calls[0].Args
	joined := strings.Join(args, " ")
	if argvContains(args, "-f") {
		t.Errorf("force down must NOT pass -f compose files; got: %s", joined)
	}
	if argvContains(args, "--env-file") {
		t.Errorf("force down must NOT pass --env-file; got: %s", joined)
	}
	for _, want := range []string{"compose", "-p", "canton-test", "down", "--remove-orphans", "--timeout"} {
		if !argvContains(args, want) {
			t.Errorf("force down args missing %q; got: %s", want, joined)
		}
	}
	if argvContains(args, "--volumes") {
		t.Errorf("ForceStop(false) must not pass --volumes; got: %s", joined)
	}
}

// TestForceStop_RemoveVolumes adds --volumes when asked (the remove path).
func TestForceStop_RemoveVolumes(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec)
	if err := c.ForceStop(context.Background(), true); err != nil {
		t.Fatalf("ForceStop: %v", err)
	}
	if !argvContains(rec.calls[0].Args, "--volumes") {
		t.Errorf("ForceStop(true) must pass --volumes; got: %v", rec.calls[0].Args)
	}
}
