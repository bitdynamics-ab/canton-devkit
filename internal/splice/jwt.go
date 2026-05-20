package splice

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// localNetUnsafeDevOnlySecret is the HS256 signing key used by every
// JWT in Splice LocalNet. It is intentionally a fixed dev-only string
// — Splice's entrypoint scripts (docker/console/entrypoint.sh) and
// SPLICE_APP_UI_UNSAFE_SECRET in env/common.env both default to "unsafe"
// and there is no production setting in this stack.
//
// ⚠ DO NOT COPY this signer into a production codebase. The whole
// SignToken path exists only because LocalNet is a developer sandbox
// where the JWT-validation side accepts this exact secret. Any service
// that takes real money or real PII MUST use a real KMS / signing key.
//
// The contract test in jwt_test.go pins (algorithm, secret) so an
// upstream Splice change to either value fails loudly here before any
// JWTs DevKit signs are silently rejected by the validator.
const localNetUnsafeDevOnlySecret = "unsafe"

// devSecretWarnOnce ensures the prominent stderr warning fires exactly
// once per process. Without it, `creds --format json` (which calls
// SignToken three times — sv/app-provider/app-user) would spam the
// output channel three times in a row.
var devSecretWarnOnce sync.Once

func warnDevSecret() {
	devSecretWarnOnce.Do(func() {
		_, _ = fmt.Fprintln(os.Stderr,
			"warning: signing JWTs with Splice LocalNet's hardcoded "+
				"\"unsafe\" dev secret. Tokens are valid ONLY against a "+
				"LocalNet instance — never reuse this signer for "+
				"production. See internal/splice/jwt.go.")
	})
}

// Role identifies one of the JWT-issuing logical accounts in the
// Splice LocalNet topology. Names mirror what's documented in
// docker/console/entrypoint.sh.
type Role string

const (
	RoleSV          Role = "sv"
	RoleAppProvider Role = "app-provider"
	RoleAppUser     Role = "app-user"
)

// AllRoles returns the roles a single LocalNet bring-up issues tokens for.
func AllRoles() []Role { return []Role{RoleSV, RoleAppProvider, RoleAppUser} }

// CredentialInputs is the per-role data needed to construct a JWT:
// the subject (`AUTH_<ROLE>_VALIDATOR_USER_NAME`) and the audience
// (`AUTH_<ROLE>_AUDIENCE`). Both come from `env/<role>-auth-on.env`.
type CredentialInputs struct {
	Role     Role
	User     string
	Audience string
}

// LoadCredentialInputs reads `env/<role>-auth-on.env` from the cached
// project and returns the per-role token claims for every role.
func LoadCredentialInputs(projectDir string) ([]CredentialInputs, error) {
	var out []CredentialInputs
	for _, r := range AllRoles() {
		envFile := filepath.Join(projectDir, "env", string(r)+"-auth-on.env")
		raw, err := readEnvMap(envFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", envFile, err)
		}
		user := authVar(raw, r, "VALIDATOR_USER_NAME")
		aud := authVar(raw, r, "AUDIENCE")
		if user == "" || aud == "" {
			return nil, fmt.Errorf("%s: missing AUTH_<role>_VALIDATOR_USER_NAME or AUTH_<role>_AUDIENCE", envFile)
		}
		out = append(out, CredentialInputs{Role: r, User: user, Audience: aud})
	}
	return out, nil
}

// SignToken issues a fresh HS256 JWT for the given inputs using the
// hardcoded LocalNet dev secret. The token has no `exp` claim — Splice's
// LocalNet auth flow doesn't enforce expiration.
//
// First call in a process prints a one-line stderr warning so a user
// who accidentally pipes these tokens into a non-dev context sees what
// they're holding. Subsequent calls are silent (sync.Once).
func SignToken(in CredentialInputs) (string, error) {
	warnDevSecret()

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	payload := map[string]interface{}{
		"sub": in.User,
		"aud": in.Audience,
	}
	hBytes, _ := json.Marshal(header)
	pBytes, _ := json.Marshal(payload)
	h64 := base64.RawURLEncoding.EncodeToString(hBytes)
	p64 := base64.RawURLEncoding.EncodeToString(pBytes)

	signingInput := h64 + "." + p64
	mac := hmac.New(sha256.New, []byte(localNetUnsafeDevOnlySecret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// authVar looks up AUTH_<UPPER_ROLE>_<SUFFIX> in the env map. Roles
// with hyphens (e.g. "app-provider") become underscores in the env
// var names (`AUTH_APP_PROVIDER_*`).
func authVar(m map[string]string, role Role, suffix string) string {
	key := "AUTH_" + strings.ReplaceAll(strings.ToUpper(string(role)), "-", "_") + "_" + suffix
	return m[key]
}

// readEnvMap parses a Splice-style .env file into a key→value map with
// shell-style ${VAR:-default} expansion. Comments and blank lines are
// skipped; inline `#` comments are stripped.
func readEnvMap(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := stripInlineComment(line[eq+1:])
		val = stripQuotes(strings.TrimSpace(val))
		out[key] = expand(val, out)
	}
	return out, scanner.Err()
}
