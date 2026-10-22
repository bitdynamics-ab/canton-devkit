package campaign

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func setLinkTime(t *testing.T, v string) {
	t.Helper()
	prev := enabled
	enabled = v
	t.Cleanup(func() { enabled = prev })
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func onlyEntry(t *testing.T, dir, want string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != want {
		t.Errorf("dir entries = %v, want only %q", names, want)
	}
}

func TestEnabled_FalseyEnvOverridesLinkTimeOn(t *testing.T) {
	setLinkTime(t, "on")
	file := filepath.Join(t.TempDir(), "campaign-id")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)
	for _, v := range []string{"0", "false", "off", "no", "n", "OFF", " No ", "False"} {
		resetCache(t)
		t.Setenv("DEVKIT_CAMPAIGN", v)
		if Enabled() {
			t.Errorf("DEVKIT_CAMPAIGN=%q must disable a campaign build", v)
		}
		if got := Code(); got != "" {
			t.Errorf("DEVKIT_CAMPAIGN=%q: Code() = %q, want empty", v, got)
		}
		if code, err := Lookup(); code != "" || err != nil {
			t.Errorf("DEVKIT_CAMPAIGN=%q: Lookup() = (%q, %v), want (\"\", nil)", v, code, err)
		}
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an opted-out run must not mint a code file, stat err = %v", err)
	}
}

func TestEnabled_UnsetOrUnknownEnvKeepsLinkTimeDefault(t *testing.T) {
	setLinkTime(t, "on")
	unsetEnv(t, "DEVKIT_CAMPAIGN")
	if !Enabled() {
		t.Error("unset DEVKIT_CAMPAIGN must keep the link-time on default")
	}
	for _, v := range []string{"", "  ", "maybe"} {
		t.Setenv("DEVKIT_CAMPAIGN", v)
		if !Enabled() {
			t.Errorf("DEVKIT_CAMPAIGN=%q must keep the link-time on default", v)
		}
	}

	setLinkTime(t, "")
	unsetEnv(t, "DEVKIT_CAMPAIGN")
	if Enabled() {
		t.Error("unset DEVKIT_CAMPAIGN must keep the link-time off default")
	}
	t.Setenv("DEVKIT_CAMPAIGN", "maybe")
	if Enabled() {
		t.Error("an unrecognised DEVKIT_CAMPAIGN must keep the link-time off default")
	}
}

func TestLoadOrCreate_ConcurrentFirstRunsConverge(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	const racers = 32
	codes := make([]string, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i], errs[i] = loadOrCreate(file)
		}()
	}
	close(start)
	wg.Wait()

	for i := range racers {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if codes[i] != codes[0] {
			t.Fatalf("racers minted different codes: %q vs %q", codes[0], codes[i])
		}
	}
	if !codeRe.MatchString(codes[0]) {
		t.Fatalf("code %q is not well formed", codes[0])
	}
	if got := readFile(t, file); got != codes[0]+"\n" {
		t.Errorf("persisted %q, every racer returned %q", got, codes[0])
	}
	onlyEntry(t, dir, "campaign-id")
}

func TestCreateExclusive_NeverReplacesTheWinner(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	if err := createExclusive(file, "CCT-AAAAAAAA"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := createExclusive(file, "CCT-BBBBBBBB")
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second create err = %v, want fs.ErrExist", err)
	}
	if got := readFile(t, file); got != "CCT-AAAAAAAA\n" {
		t.Errorf("losing writer replaced the winner: file = %q", got)
	}
	onlyEntry(t, dir, "campaign-id")
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("code file mode = %o, want 600", perm)
		}
	}
}

func TestLoadOrCreate_LoserAdoptsWinnerFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "campaign-id")
	if err := createExclusive(file, "CCT-WINNER22"); err != nil {
		t.Fatal(err)
	}
	if err := createExclusive(file, "CCT-LOSER234"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("loser create err = %v, want fs.ErrExist", err)
	}
	code, err := loadOrCreate(file)
	if err != nil || code != "CCT-WINNER22" {
		t.Errorf("loadOrCreate = (%q, %v), want the winner CCT-WINNER22", code, err)
	}
}

func TestTakeInvalid_RescuesACodeWrittenByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	if err := os.WriteFile(file, []byte("CCT-ABCD2345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, err := takeInvalid(file)
	if err != nil || code != "CCT-ABCD2345" {
		t.Fatalf("takeInvalid = (%q, %v), want the valid code back", code, err)
	}
	if got := readFile(t, file); got != "CCT-ABCD2345\n" {
		t.Errorf("valid code file must be restored, got %q", got)
	}
	onlyEntry(t, dir, "campaign-id")
}

func TestCode_CorruptFileIsReplacedAndPersisted(t *testing.T) {
	resetCache(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	if err := os.WriteFile(file, []byte("not-a-code\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	code, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !codeRe.MatchString(code) {
		t.Fatalf("code %q is not well formed", code)
	}
	if got := readFile(t, file); got != code+"\n" {
		t.Errorf("fresh code not persisted over the corrupt file: %q", got)
	}
	onlyEntry(t, dir, "campaign-id")
	resetCache(t)
	if again := Code(); again != code {
		t.Errorf("healed code must survive a restart: %q then %q", code, again)
	}
}

func TestLookup_PersistFailureIsReportedAndCodeStaysStable(t *testing.T) {
	resetCache(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(blocker, "campaign-id")
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	code, err := Lookup()
	if !codeRe.MatchString(code) {
		t.Fatalf("an unwritable home must still yield a code, got %q", code)
	}
	if err == nil {
		t.Fatal("persist failure must be reported, got nil error")
	}
	if !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "change on restart") {
		t.Errorf("error should name the file and the consequence, got %q", err)
	}
	again, err2 := Lookup()
	if again != code {
		t.Errorf("code must not be re-minted within a process: %q then %q", code, again)
	}
	if err2 == nil || err2.Error() != err.Error() {
		t.Errorf("second Lookup err = %v, want the cached %v", err2, err)
	}
	if got := Code(); got != code {
		t.Errorf("Code() = %q, want %q", got, code)
	}
}

func readOnlyDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not block writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return dir
}

func TestLookup_UnwritableDirIsReportedAndCodeStaysStable(t *testing.T) {
	for name, rel := range map[string]string{
		"temp file cannot be created": "campaign-id",
		"code dir cannot be created":  filepath.Join(".canton-devkit", "campaign-id"),
	} {
		t.Run(name, func(t *testing.T) {
			resetCache(t)
			dir := readOnlyDir(t)
			file := filepath.Join(dir, rel)
			t.Setenv("DEVKIT_CAMPAIGN", "on")
			t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

			code, err := Lookup()
			if !codeRe.MatchString(code) {
				t.Fatalf("an unwritable home must still yield a code, got %q", code)
			}
			if err == nil || !strings.Contains(err.Error(), "change on restart") {
				t.Fatalf("persist failure must be reported, got %v", err)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("err = %v, want the write-side permission error", err)
			}
			again, err2 := Lookup()
			if again != code {
				t.Errorf("code must not be re-minted within a process: %q then %q", code, again)
			}
			if err2 == nil || err2.Error() != err.Error() {
				t.Errorf("second Lookup err = %v, want the cached %v", err2, err)
			}
			if entries, rerr := os.ReadDir(dir); rerr != nil || len(entries) != 0 {
				t.Errorf("read-only dir must stay empty, entries = %v, err = %v", entries, rerr)
			}
		})
	}
}

func TestLookup_CacheFollowsTheCodeFile(t *testing.T) {
	resetCache(t)
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("CCT-AAAAAAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("CCT-BBBBBBBB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN_FILE", a)
	if got := Code(); got != "CCT-AAAAAAAA" {
		t.Fatalf("Code() = %q, want CCT-AAAAAAAA", got)
	}
	t.Setenv("DEVKIT_CAMPAIGN_FILE", b)
	if got := Code(); got != "CCT-BBBBBBBB" {
		t.Errorf("Code() after switching files = %q, want CCT-BBBBBBBB", got)
	}
}

func TestLoadOrCreate_UnreadableCodePathStillYieldsACode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "campaign-id")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	code, err := loadOrCreate(dir)
	if !codeRe.MatchString(code) {
		t.Errorf("code = %q, want a well-formed fallback code", code)
	}
	if err == nil {
		t.Error("a code path that cannot be read must be reported")
	}
}

func TestTakeInvalid_MissingFileIsNotAnError(t *testing.T) {
	code, err := takeInvalid(filepath.Join(t.TempDir(), "campaign-id"))
	if code != "" || err != nil {
		t.Errorf("takeInvalid on a missing file = (%q, %v), want (\"\", nil)", code, err)
	}
}

func danglingSymlink(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "campaign-id")
	if err := os.Symlink(filepath.Join(dir, "missing-target"), file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dir, file
}

func TestLookup_DanglingSymlinkEndsWithAPersistedStableCode(t *testing.T) {
	resetCache(t)
	dir, file := danglingSymlink(t)
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	code, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup over a dangling symlink: %v", err)
	}
	if !codeRe.MatchString(code) {
		t.Fatalf("code %q is not well formed", code)
	}
	if got := readFile(t, file); got != code+"\n" {
		t.Errorf("code not persisted over the dangling symlink: file = %q, want %q", got, code+"\n")
	}
	fi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("dangling symlink must be replaced by a regular code file, mode = %v", fi.Mode())
	}
	onlyEntry(t, dir, "campaign-id")

	resetCache(t)
	again, err := Lookup()
	if err != nil || again != code {
		t.Errorf("restart must keep the persisted code: got (%q, %v), want %q", again, err, code)
	}
}

func TestLoadOrCreate_LiveSymlinkIsReadThroughAndKept(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-id")
	if err := os.WriteFile(target, []byte("CCT-ABCD2345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "campaign-id")
	if err := os.Symlink(target, file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	code, err := loadOrCreate(file)
	if err != nil || code != "CCT-ABCD2345" {
		t.Fatalf("loadOrCreate = (%q, %v), want the linked code", code, err)
	}
	fi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("a symlink to a valid code must be left in place, mode = %v", fi.Mode())
	}
}
