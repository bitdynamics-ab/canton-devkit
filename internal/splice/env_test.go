package splice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripInlineComment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"value", "value"},
		{"value # comment", "value"},
		{"value\t# tab comment", "value"},
		{"url=http://x#anchor", "url=http://x#anchor"}, // no whitespace before #
		{`"value # in quotes"`, `"value # in quotes"`}, // quoted — preserve
		{"value  #  trailing", "value"},                // double-space before #
		{"", ""},
	}
	for _, c := range cases {
		if got := stripInlineComment(c.in); got != c.want {
			t.Errorf("stripInlineComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpand(t *testing.T) {
	m := map[string]string{
		"DB_USER":   "canton",
		"DB_SERVER": "postgres",
		"EMPTY":     "",
	}
	cases := []struct {
		in, want string
	}{
		{"static", "static"},
		{"${DB_USER}", "canton"},
		{"user=${DB_USER}", "user=canton"},
		{"${DB_USER}@${DB_SERVER}", "canton@postgres"},
		{"${UNDEFINED}", ""},
		{"${UNDEFINED:-fallback}", "fallback"},
		{"${EMPTY:-defaulted}", "defaulted"}, // empty triggers default
		{"${DB_USER:-ignored}", "canton"},
	}
	for _, c := range cases {
		if got := expand(c.in, m); got != c.want {
			t.Errorf("expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripQuotes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"hello"`, "hello"},
		{`'world'`, "world"},
		{`"mismatched'`, `"mismatched'`}, // unbalanced — leave alone
		{``, ``},
		{`"`, `"`},
	}
	for _, c := range cases {
		if got := stripQuotes(c.in); got != c.want {
			t.Errorf("stripQuotes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoadEnvAcrossFiles(t *testing.T) {
	tmp := t.TempDir()

	// File 1: defines DB_USER.
	mustWrite(t, filepath.Join(tmp, "a.env"), `# Comment
DB_USER=canton
DB_PASSWORD="canton-pass"
`)

	// File 2: references DB_USER from file 1.
	mustWrite(t, filepath.Join(tmp, "b.env"), `
POSTGRES_USER=${DB_USER}
POSTGRES_PASSWORD=${DB_PASSWORD}
WITH_DEFAULT=${MISSING:-fallback}
`)

	// File 3: doesn't exist — should be silently skipped.

	env, err := LoadEnv(tmp, []string{"a.env", "b.env", "missing.env"}, map[string]string{
		"OVERRIDE_KEY": "override-${DB_USER}",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"DB_USER":           "canton",
		"DB_PASSWORD":       "canton-pass",
		"POSTGRES_USER":     "canton",
		"POSTGRES_PASSWORD": "canton-pass",
		"WITH_DEFAULT":      "fallback",
		"OVERRIDE_KEY":      "override-canton",
	}
	got := envSliceToMap(env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, got[k], v)
		}
	}
}

func TestLoadEnvOverrideWins(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "base.env"), "FOO=base\n")

	env, err := LoadEnv(tmp, []string{"base.env"}, map[string]string{"FOO": "override"})
	if err != nil {
		t.Fatal(err)
	}
	m := envSliceToMap(env)
	if m["FOO"] != "override" {
		t.Errorf("override should win: got FOO=%q", m["FOO"])
	}
}

func TestLoadEnvSkipsCommentsAndBlanks(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "a.env"), `
# leading comment
KEY1=value1

# another
KEY2=value2
   # indented comment
`)

	env, _ := LoadEnv(tmp, []string{"a.env"}, nil)
	m := envSliceToMap(env)
	if m["KEY1"] != "value1" || m["KEY2"] != "value2" {
		t.Errorf("got: %v", m)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envSliceToMap(in []string) map[string]string {
	m := make(map[string]string, len(in))
	for _, kv := range in {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}
