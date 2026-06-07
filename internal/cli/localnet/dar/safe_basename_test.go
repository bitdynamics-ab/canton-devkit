package dar

import "testing"

// TestSafeBasename_NeutralisesPathTraversal pins the // fix: a hostile participant returning name="../../etc/passwd" must
// not get DAR bytes written outside the caller's CWD.
func TestSafeBasename_NeutralisesPathTraversal(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"happy path — normal name", "retail-token", "retail-token"},
		{"traversal — leading parents", "../../etc/passwd", "passwd"},
		{"traversal — multiple parents + nested", "../../../home/victim/.ssh/authorized_keys", "authorized_keys"},
		{"traversal — only dots", "..", ""},
		{"traversal — single dot", ".", ""},
		{"absolute path", "/etc/shadow", "shadow"},
		{"windows separator embedded", "evil\\..\\file", "evil_.._file"},
		{"forward slash inside basename (defensive)", "no/slash/here", "here"},
		{"NUL byte (control char strip)", "evil\x00name", "evil_name"},
		{"tab + newline (control chars)", "name\tnew\nline", "name_new_line"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"trailing slash", "name/", "name"},
		{"unicode — kept verbatim", "rétail-tøkén", "rétail-tøkén"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := safeBasename(c.in)
			if got != c.want {
				t.Errorf("safeBasename(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
