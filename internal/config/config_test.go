package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &LocalNetConfig{
		Name:    "test-net",
		Version: "latest",
		DataDir: tmpDir,
	}

	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	expectedFiles := []string{
		"docker-compose.yml",
		"init-db.sql",
		"identity.txt",
		filepath.Join("canton", "canton.conf"),
		filepath.Join("keys", "participant-admin.key"),
	}

	for _, f := range expectedFiles {
		path := filepath.Join(tmpDir, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected file %s to be non-empty", f)
		}
	}
}

func TestGenerateDeterministicKeys(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	cfg1 := &LocalNetConfig{Name: "same-name", Version: "latest", DataDir: dir1}
	cfg2 := &LocalNetConfig{Name: "same-name", Version: "latest", DataDir: dir2}

	if err := Generate(cfg1); err != nil {
		t.Fatal(err)
	}
	if err := Generate(cfg2); err != nil {
		t.Fatal(err)
	}

	key1, _ := os.ReadFile(filepath.Join(dir1, "keys", "participant-admin.key"))
	key2, _ := os.ReadFile(filepath.Join(dir2, "keys", "participant-admin.key"))

	if string(key1) != string(key2) {
		t.Error("expected deterministic key generation for same name")
	}
}

func TestGenerateDifferentNames(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	cfg1 := &LocalNetConfig{Name: "net-a", Version: "latest", DataDir: dir1}
	cfg2 := &LocalNetConfig{Name: "net-b", Version: "latest", DataDir: dir2}

	if err := Generate(cfg1); err != nil {
		t.Fatal(err)
	}
	if err := Generate(cfg2); err != nil {
		t.Fatal(err)
	}

	key1, _ := os.ReadFile(filepath.Join(dir1, "keys", "participant-admin.key"))
	key2, _ := os.ReadFile(filepath.Join(dir2, "keys", "participant-admin.key"))

	if string(key1) == string(key2) {
		t.Error("expected different keys for different names")
	}
}
