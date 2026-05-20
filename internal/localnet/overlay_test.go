package localnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteContainerRenameOverlay(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteContainerRenameOverlay(tmp, "alice-")
	if err != nil {
		t.Fatalf("WriteContainerRenameOverlay: %v", err)
	}
	if filepath.Base(path) != "containers.yaml" {
		t.Errorf("unexpected filename: %s", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	// Every known container must appear with the prefix applied.
	for _, svc := range splice610Containers {
		want := "container_name: alice-" + svc
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing %q\n---\n%s\n---", want, s)
		}
	}

	// Service block must precede each container_name (basic YAML shape).
	if !strings.Contains(s, "services:\n") {
		t.Errorf("overlay missing `services:` header")
	}
}

func TestWriteContainerRenameOverlay_RejectsEmptyPrefix(t *testing.T) {
	tmp := t.TempDir()
	_, err := WriteContainerRenameOverlay(tmp, "")
	if err == nil {
		t.Error("expected error for empty prefix")
	}
}

func TestWriteContainerRenameOverlay_DeterministicOrder(t *testing.T) {
	a, _ := WriteContainerRenameOverlay(t.TempDir(), "alice-")
	b, _ := WriteContainerRenameOverlay(t.TempDir(), "alice-")

	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if string(ba) != string(bb) {
		t.Errorf("overlay output is non-deterministic — needed for diff-stable inspection")
	}
}

// TestWriteContainerRenameOverlay_RelaxesNginxSpliceDep locks the fix
// for the dep-health-timeout bug: nginx's depends_on:splice condition
// must be downgraded to service_started. Without this, `docker compose
// up -d` blocks on splice's healthcheck and times out before Splice's
// ~10-min onboarding completes (caught by BIT-117 integration test).
func TestWriteContainerRenameOverlay_RelaxesNginxSpliceDep(t *testing.T) {
	path, err := WriteContainerRenameOverlay(t.TempDir(), "alice-")
	if err != nil {
		t.Fatalf("WriteContainerRenameOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	// Must contain the relaxation block under nginx.
	for _, want := range []string{
		"  nginx:",
		"    depends_on:",
		"      splice:",
		"        condition: service_started",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("overlay missing %q\n---\n%s", want, src)
		}
	}

	// Must NOT carry a service_healthy condition under nginx — that's
	// what we're explicitly overriding.
	if strings.Contains(src, "        condition: service_healthy") {
		t.Errorf("overlay still has service_healthy condition; the relaxation didn't take\n%s", src)
	}
}
