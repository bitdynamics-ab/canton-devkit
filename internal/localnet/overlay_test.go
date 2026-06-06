package localnet

import (
	"fmt"
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
// ~10-min onboarding completes.
// --------------- loopback-ports overlay tests ---------------

func TestWriteLoopbackPortsOverlay(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteLoopbackPortsOverlay(tmp)
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	if filepath.Base(path) != "loopback-ports.yaml" {
		t.Errorf("unexpected filename: %s", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	// Every port binding must include 127.0.0.1.
	if !strings.Contains(s, "services:\n") {
		t.Errorf("overlay missing `services:` header")
	}

	// Every service in the table must appear.
	for _, svc := range loopbackPortTable() {
		svcHeader := "  " + svc.Name + ":\n"
		if !strings.Contains(s, svcHeader) {
			t.Errorf("overlay missing service %q\n---\n%s\n---", svc.Name, s)
		}
		for _, p := range svc.Ports {
			want := fmt.Sprintf("127.0.0.1:%s:%d", p.HostExpr, p.ContainerPort)
			if !strings.Contains(s, want) {
				t.Errorf("overlay missing port binding %q\n---\n%s\n---", want, s)
			}
		}
	}

	// Must use !override to replace (not append) the ports list.
	if !strings.Contains(s, "ports: !override") {
		t.Errorf("overlay must use `ports: !override` to replace upstream port lists\n---\n%s\n---", s)
	}

	// Must NOT contain 0.0.0.0 anywhere.
	if strings.Contains(s, "0.0.0.0") {
		t.Errorf("overlay must not contain 0.0.0.0\n---\n%s\n---", s)
	}
}

func TestWriteLoopbackPortsOverlay_DeterministicOrder(t *testing.T) {
	a, _ := WriteLoopbackPortsOverlay(t.TempDir())
	b, _ := WriteLoopbackPortsOverlay(t.TempDir())

	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if string(ba) != string(bb) {
		t.Errorf("overlay output is non-deterministic — needed for diff-stable inspection")
	}
}

// TestWriteLoopbackPortsOverlay_CoversAllCantonPorts ensures the
// overlay covers every port in CantonPortInternal. If Splice adds a
// new participant port, this test catches the omission.
func TestWriteLoopbackPortsOverlay_CoversAllCantonPorts(t *testing.T) {
	path, err := WriteLoopbackPortsOverlay(t.TempDir())
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for key, port := range CantonPortInternal {
		want := fmt.Sprintf("127.0.0.1:0:%d", port)
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing canton port %d (key %q)", port, key)
		}
	}
}

// TestWriteLoopbackPortsOverlay_CoversAllSplicePorts ensures the
// overlay covers all splice internal ports.
func TestWriteLoopbackPortsOverlay_CoversAllSplicePorts(t *testing.T) {
	path, err := WriteLoopbackPortsOverlay(t.TempDir())
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, port := range SplicePortInternal {
		want := fmt.Sprintf("127.0.0.1:0:%d", port)
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing splice port %d", port)
		}
	}
}

// --------------- container-rename overlay tests (continued) ---------------

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
