package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func dockerDaemonAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available")
	}
}

func TestRemoveProjectVolumes_NoMatchIsNoOp(t *testing.T) {
	dockerDaemonAvailable(t)
	if err := RemoveProjectVolumes(context.Background(), "canton-no-such-project-xyz"); err != nil {
		t.Fatalf("RemoveProjectVolumes: %v", err)
	}
}

func TestRemoveProjectVolumes_RemovesPrefixedVolumes(t *testing.T) {
	dockerDaemonAvailable(t)
	project := "canton-devkit-voltest-" + t.Name()
	vol := project + "_postgres"
	create := exec.Command("docker", "volume", "create", vol)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("docker volume create: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "volume", "rm", "-f", vol).Run()
	})

	if err := RemoveProjectVolumes(context.Background(), project); err != nil {
		t.Fatalf("RemoveProjectVolumes: %v", err)
	}
	check := exec.Command("docker", "volume", "inspect", vol)
	if err := check.Run(); err == nil {
		t.Fatalf("expected volume %q to be removed", vol)
	}
}

func TestRemoveProjectVolumes_EmptyProject(t *testing.T) {
	if err := RemoveProjectVolumes(context.Background(), "  "); err != nil {
		t.Fatalf("empty project: %v", err)
	}
}

func TestRemoveProjectVolumes_BadDockerPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "docker")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(filepath.ListSeparator)+os.Getenv("PATH"))
	if err := RemoveProjectVolumes(context.Background(), "canton-x"); err == nil {
		t.Fatal("expected error when docker volume ls fails")
	}
}
