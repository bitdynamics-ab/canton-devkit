package splice

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvFiles lists, in load order, the .env files Splice LocalNet expects to
// have available at `docker compose` invocation time. Order matters — later
// files override earlier ones. Paths are relative to the cached project
// directory.
//
// We load them ourselves (rather than passing each as `--env-file`) because
// docker compose's `--env-file` does NOT perform `${VAR}` interpolation
// across files, but the Splice env files heavily cross-reference each other
// (e.g. postgres.env: `POSTGRES_USER=${DB_USER}`).
func EnvFiles() []string {
	return []string{
		"compose.env",
		"env/common.env",
		"env/postgres.env",
		"env/splice.env",
		"env/alpha-protocol-version.env",
		"env/app-provider-auth-on.env",
		"env/app-user-auth-on.env",
		"env/sv-auth-on.env",
	}
}

// envLine: KEY=value (KEY: letters, digits, underscore; not starting with digit)
var envLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)

// maxExpandDepth caps recursive expansion so a self-referential value
// like KEY=$KEY can't blow the stack. Real Splice files only nest 1-2
// levels deep; 16 is generous.
const maxExpandDepth = 16

// LoadEnv reads the listed .env files (paths relative to projectDir) and
// returns a fully-resolved KEY=value list suitable for `exec.Cmd.Env`,
// merged with the current process environment as the base layer.
//
// Resolution semantics (mirrors what `source` + bash would do):
//   - Files are read in order; later definitions overwrite earlier.
//   - Each value undergoes `${VAR}` and `${VAR:-default}` expansion against
//     the running map at the time of evaluation (so a later file can refer
//     to earlier values).
//   - Process env (`os.Environ()`) seeds the initial map, so `${HOME}` etc.
//     resolve to the host's values.
//   - Lines starting with `#` and blank lines are skipped. Quotes (single
//     or double) around the value are stripped if balanced.
func LoadEnv(projectDir string, files []string, overrides map[string]string) ([]string, error) {
	resolved := envFromOSEnv()

	for _, rel := range files {
		path := filepath.Join(projectDir, rel)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Some env files are profile-conditional; skip silently.
				continue
			}
			return nil, fmt.Errorf("open %s: %w", path, err)
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			m := envLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			key, raw := m[1], stripInlineComment(m[2])
			raw = stripQuotes(strings.TrimSpace(raw))
			resolved[key] = expand(raw, resolved)
		}
		_ = f.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}

	for k, v := range overrides {
		resolved[k] = expand(v, resolved)
	}

	out := make([]string, 0, len(resolved))
	for k, v := range resolved {
		out = append(out, k+"="+v)
	}
	return out, nil
}

func envFromOSEnv() map[string]string {
	m := make(map[string]string, len(os.Environ()))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

// expand performs ${VAR}, ${VAR:-default}, and bare $VAR substitution
// against m. Unknown variables expand to the empty string (or the
// default if given). Non-`$...` text passes through unchanged.
//
// Defaults are expanded recursively (up to maxExpandDepth) so nested
// references resolve. Example from upstream Splice env files:
//
//	LOCALNET_ENV_DIR=${LOCALNET_ENV_DIR:-$LOCALNET_DIR/env}
//
// expands to `<value-of-LOCALNET_DIR>/env` when LOCALNET_ENV_DIR is
// itself unset and LOCALNET_DIR is set in m.
//
// We hand-roll the parser instead of using regexp because Go's RE2
// can't match balanced braces — and Splice env files include defaults
// that themselves contain `${VAR}`. `$$` is preserved as a literal `$`,
// matching `make`/`docker compose` substitution semantics.
func expand(s string, m map[string]string) string {
	return expandDepth(s, m, 0)
}

func expandDepth(s string, m map[string]string, depth int) string {
	if depth >= maxExpandDepth {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		c := s[i]
		if c != '$' || i+1 >= len(s) {
			b.WriteByte(c)
			i++
			continue
		}
		next := s[i+1]

		// $$ → literal $
		if next == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}

		// ${...} — find the matching close-brace with brace counting so
		// nested ${...} inside a default is captured.
		if next == '{' {
			end, ok := findMatchingBrace(s, i+1)
			if !ok {
				// Unbalanced — leave the run literal.
				b.WriteByte(c)
				i++
				continue
			}
			body := s[i+2 : end] // strip ${ and }
			key, hasDef, def := splitBracedRef(body)
			if !isValidIdent(key) {
				// `${...}` whose VAR isn't a valid identifier — leave
				// literal (defensive; shouldn't appear in real files).
				b.WriteString(s[i : end+1])
				i = end + 1
				continue
			}
			b.WriteString(resolve(key, hasDef, def, m, depth))
			i = end + 1
			continue
		}

		// $VAR (bareword) — only if the next char starts a valid
		// identifier. Otherwise the `$` is literal ("price: $9.99").
		if isIdentStart(next) {
			j := i + 1
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			key := s[i+1 : j]
			b.WriteString(resolve(key, false, "", m, depth))
			i = j
			continue
		}

		// Literal $
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// resolve looks up key in m. If found and non-empty, returns it. If
// hasDef, expands and returns the default. Otherwise returns "".
func resolve(key string, hasDef bool, def string, m map[string]string, depth int) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	if hasDef {
		return expandDepth(def, m, depth+1)
	}
	return ""
}

// findMatchingBrace returns the index of the `}` that closes the `{` at
// `start`. Handles nested `${...}` by counting unescaped braces.
func findMatchingBrace(s string, start int) (int, bool) {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

// splitBracedRef splits the body of a `${...}` form into key and (if
// present) the default-value tail introduced by `:-`.
func splitBracedRef(body string) (key string, hasDef bool, def string) {
	if i := strings.Index(body, ":-"); i >= 0 {
		return body[:i], true, body[i+2:]
	}
	return body, false, ""
}

func isIdentStart(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '_'
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func isValidIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentCont(s[i]) {
			return false
		}
	}
	return true
}

// stripInlineComment trims a trailing ` # comment` (or `\t# ...`) from a
// raw env value, mirroring shell semantics. A `#` that appears inside a
// quoted segment is preserved; we keep the implementation simple and
// require whitespace before the `#` to count as a comment marker.
//
// Examples:
//
//	`value # note`         → `value`
//	`value`                → `value`
//	`"value # not comment"`→ `"value # not comment"` (quoted; left alone)
//	`url=http://#anchor`   → `url=http://#anchor` (no preceding whitespace)
func stripInlineComment(s string) string {
	// If the value starts with a quote, comments inside the quote stay.
	// Find the matching close-quote first; only look for comments after.
	start := 0
	if len(s) > 0 && (s[0] == '"' || s[0] == '\'') {
		if i := strings.IndexByte(s[1:], s[0]); i >= 0 {
			start = 1 + i + 1
		} else {
			return s // unbalanced quote — leave as-is
		}
	}
	for i := start; i < len(s)-1; i++ {
		if (s[i] == ' ' || s[i] == '\t') && s[i+1] == '#' {
			// Trim trailing whitespace from the captured value so
			// "value  #..." → "value", not "value ".
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return s
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
