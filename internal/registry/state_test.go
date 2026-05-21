package registry

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// useTmpRoot points the registry at a temp dir for the test's lifetime
// and returns a cleanup func.
func useTmpRoot(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", tmp)
	return tmp
}

func TestWriteAndRead(t *testing.T) {
	useTmpRoot(t)

	in := NewState("alice", "0.6.4")
	in.ComposeProject = "canton-alice"
	in.ComposeFiles = []string{"/abs/compose.yaml"}
	in.DockerNetwork = "alice"
	in.Ports = map[string]int{"app_user_ui": 2000}
	in.Status = StatusRunning

	if err := Write(in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read("alice")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "alice" || got.SpliceVersion != "0.6.4" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Ports["app_user_ui"] != 2000 {
		t.Errorf("ports lost: %v", got.Ports)
	}
	if got.Status != StatusRunning {
		t.Errorf("status lost: %q", got.Status)
	}
}

func TestReadMissing(t *testing.T) {
	useTmpRoot(t)
	_, err := Read("ghost")
	if err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestStatePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode semantics differ on Windows")
	}
	useTmpRoot(t)
	if err := Write(NewState("alice", "0.6.4")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(PathFor("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state.json mode = %o, want 0600 (JWTs land here)", info.Mode().Perm())
	}
}

func TestAtomicWriteSurvivesCrash(t *testing.T) {
	useTmpRoot(t)
	// Pre-create a state file with a known good payload.
	if err := Write(NewState("alice", "0.6.4")); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(PathFor("alice"))

	// Simulate a half-written file by dropping a leftover tmp file
	// in the same directory — atomicWrite must clean it up.
	leftover := filepath.Join(DataDirFor("alice"), ".tmp-state-crash")
	if err := os.WriteFile(leftover, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write again. Should succeed; leftover may remain (not cleanup target).
	s := NewState("alice", "0.6.5")
	if err := Write(s); err != nil {
		t.Fatalf("re-Write: %v", err)
	}

	got, err := Read("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.SpliceVersion != "0.6.5" {
		t.Errorf("write didn't take effect: %s", got.SpliceVersion)
	}
	// Original payload must NOT still be on disk.
	current, _ := os.ReadFile(PathFor("alice"))
	if string(current) == string(original) {
		t.Errorf("state.json wasn't actually rewritten")
	}
}

func TestRejectsFutureSchema(t *testing.T) {
	useTmpRoot(t)
	if err := os.MkdirAll(DataDirFor("alice"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PathFor("alice"),
		[]byte(`{"schema_version": 999, "name": "alice"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Read("alice")
	if err == nil || !strings.Contains(err.Error(), "schema_version 999") {
		t.Errorf("expected newer-schema rejection, got %v", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	useTmpRoot(t)
	// Delete-before-create
	if err := Delete("ghost"); err != nil {
		t.Errorf("delete missing should be ok: %v", err)
	}
	// Create then delete twice
	if err := Write(NewState("alice", "0.6.4")); err != nil {
		t.Fatal(err)
	}
	if err := Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("alice"); err != nil {
		t.Errorf("second delete should be no-op: %v", err)
	}
	if _, err := Read("alice"); err != ErrNotFound {
		t.Errorf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestIndexTracksMultipleInstances(t *testing.T) {
	useTmpRoot(t)

	for _, n := range []string{"charlie", "alice", "bob"} {
		s := NewState(n, "0.6.4")
		s.Status = StatusRunning
		if err := Write(s); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(idx.Entries))
	}
	want := []string{"alice", "bob", "charlie"}
	for i, w := range want {
		if idx.Entries[i].Name != w {
			t.Errorf("entry[%d] = %q, want %q (entries should be sorted)", i, idx.Entries[i].Name, w)
		}
	}

	if err := Delete("bob"); err != nil {
		t.Fatal(err)
	}
	idx2, _ := ReadIndex()
	if len(idx2.Entries) != 2 {
		t.Errorf("after delete: want 2, got %d", len(idx2.Entries))
	}
	for _, e := range idx2.Entries {
		if e.Name == "bob" {
			t.Errorf("bob still in index after Delete")
		}
	}
}

func TestLockExcludesConcurrentOps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock is a no-op on windows")
	}
	useTmpRoot(t)

	rel1, err := Lock("alice")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	_, err = Lock("alice")
	if err == nil {
		t.Errorf("expected second lock to fail")
	}

	rel1()

	rel2, err := Lock("alice")
	if err != nil {
		t.Errorf("after release: %v", err)
	}
	rel2()
}

func TestConcurrentWritesDifferentInstancesAllSucceed(t *testing.T) {
	useTmpRoot(t)

	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			s := NewState(n, "0.6.4")
			s.Status = StatusRunning
			if err := Write(s); err != nil {
				t.Errorf("write %s: %v", n, err)
			}
		}(name)
	}
	wg.Wait()

	idx, err := ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 5 {
		t.Errorf("want 5 entries, got %d: %v", len(idx.Entries), idx.Entries)
	}
}

// --- Path-traversal hardening (Zhe review, PR #24) -----------------------

// TestValidateName_RejectsUnsafeNames covers every category of input
// that could let a `--name` value escape the registry root or break
// downstream tooling. Each case must fail with ErrInvalidName.
//
// This is the primary defense against the bug Zhe flagged: a Delete
// with --name=../x would have caused os.RemoveAll to target a path
// outside ~/.canton-devkit/localnet/.
func TestValidateName_RejectsUnsafeNames(t *testing.T) {
	cases := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{".", "current-dir literal"},
		{"..", "parent-dir literal"},
		{"../x", "parent-dir traversal"},
		{"../../etc/passwd", "deep traversal"},
		{"/tmp/x", "absolute unix"},
		{"a/b", "embedded forward slash"},
		{`a\b`, "embedded backslash (Windows separator)"},
		{".hidden", "leading dot (would land in Root/.hidden, valid-ish but excluded for clarity)"},
		{"-flag", "leading hyphen (could be parsed as a flag in tooling)"},
		{"name with spaces", "spaces"},
		{"name\nwithnewline", "control character"},
		{"name\x00null", "NUL byte"},
		{"name\twith tab", "tab"},
		{"日本語", "non-ASCII (we keep the regex ASCII-only for cross-OS path safety)"},
		{"a;b", "shell metachar"},
		{"a$b", "shell metachar"},
		{`a"b`, "shell metachar"},
		// 65-char name (one over the limit).
		{strings.Repeat("a", 65), "over 64-char length"},
	}
	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			err := ValidateName(c.name)
			if err == nil {
				t.Errorf("ValidateName(%q) accepted but should reject (%s)", c.name, c.why)
				return
			}
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("ValidateName(%q) returned %v, expected wrapped ErrInvalidName", c.name, err)
			}
		})
	}
}

// TestValidateName_AcceptsRealisticNames complements the negative test:
// every name a user might plausibly pick must pass.
func TestValidateName_AcceptsRealisticNames(t *testing.T) {
	good := []string{
		"alice",
		"diag",
		"ci-1",
		"ci-local",
		"my-stack",
		"my_stack",
		"dev2",
		"a",
		strings.Repeat("a", 64),
	}
	for _, name := range good {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) rejected but should accept: %v", name, err)
		}
	}
}

// TestDelete_CannotEscapeRegistryRoot is the end-to-end proof of the
// fix: even attempting Delete with a traversal name returns
// ErrInvalidName instead of calling os.RemoveAll on a path outside
// Root().
//
// Verifies behaviourally — a "sentinel" file lives in a sibling
// directory; if Delete were buggy it would be removed.
func TestDelete_CannotEscapeRegistryRoot(t *testing.T) {
	registryRoot := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", registryRoot)

	// Set up a sentinel file in a sibling directory. A successful
	// traversal Delete would land on this path.
	parentDir := filepath.Dir(registryRoot)
	sentinel := filepath.Join(parentDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("must-not-be-deleted"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(sentinel) }()

	// What an attacker might try.
	hostileNames := []string{
		"../sentinel.txt",
		"..",
		"../../",
		"/" + filepath.Base(sentinel),
	}
	for _, n := range hostileNames {
		t.Run(n, func(t *testing.T) {
			err := Delete(n)
			if err == nil {
				t.Errorf("Delete(%q) returned nil — security bug, would have called RemoveAll outside root", n)
			}
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("Delete(%q) returned %v, expected wrapped ErrInvalidName", n, err)
			}
		})
	}

	// Sentinel must still exist.
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel was removed despite validation — %v", err)
	}
}
