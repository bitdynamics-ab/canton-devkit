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
)

// localNetUnsafeDevOnlySecret is the HS256 signing key used by every
// JWT in Splice LocalNet. It is intentionally a fixed dev-only string.
//
// # Source-of-truth verification (Splice 0.6.4)
//
// Four independent locations in the upstream Splice tree
// (canton-network/splice → cluster/compose/localnet/) all use the
// literal "unsafe" — no quoting variations, no whitespace, no encoding.
// Grepped, listed here so a future reader can confirm against any new
// Splice release without re-deriving:
//
//  1. env/common.env:
//     SPLICE_APP_UI_UNSAFE_SECRET=${SPLICE_APP_UI_UNSAFE_SECRET:-unsafe}
//
//  2. conf/canton/{sv,app-provider,app-user}/app-auth.conf:
//     type   = unsafe-jwt-hmac-256
//     secret = "unsafe"
//     (Canton participant auth-services — what validates the JWTs we
//     send to the participant ledger API.)
//
//  3. conf/splice/{sv,app-provider,app-user}/app-auth.conf:
//     algorithm = "hs-256-unsafe"
//     secret    = "unsafe"
//     (Splice validator-apps auth + ledger-api auth-config — what
//     validates the JWTs Splice's own HTTP APIs accept.)
//
//  4. docker/console/entrypoint.sh:
//     jwt-cli encode hs256 --s unsafe --p '{"sub": "..", "aud": ".."}'
//     (Reference impl that produces console-tokens the same way we do.)
//
// The Splice config labels "unsafe-jwt-hmac-256" and "hs-256-unsafe"
// are just internal naming — the wire algorithm is plain HS256
// (HMAC-SHA-256). The contract test in jwt_test.go pins both the
// algorithm and the secret string so any upstream change fails loudly
// here before tokens are silently rejected by the validator.
//
// ⚠ DO NOT COPY this signer into a production codebase. The whole
// SignToken path exists only because LocalNet is a developer sandbox
// where the JWT-validation side accepts this exact secret. Any service
// that takes real money or real PII MUST use a real KMS / signing key.
const localNetUnsafeDevOnlySecret = "unsafe"

// DevSecretWarning is the one-line message orchestrators should print
// to the user's stderr the first time they sign a batch of dev JWTs in
// a process. SignToken itself stays pure — it does not write to any
// global stream — so callers control output via their own injected
// io.Writer (which keeps tests deterministic and is consistent with
// how RunUp / RunCreds own their errw plumbing).
const DevSecretWarning = "warning: signing JWTs with Splice LocalNet's hardcoded " +
	`"unsafe" dev secret. Tokens are valid ONLY against a LocalNet ` +
	"instance — never reuse this signer for production. See internal/splice/jwt.go."

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

// AllRoleNames exists so the participant fan-out paths (`dar upload
// --all-participants`, `token create`) cannot drift in which roles they
// target, or in what order.
func AllRoleNames() []string {
	roles := AllRoles()
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

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
// hardcoded LocalNet dev secret. The token has no `exp` claim —
// Splice's LocalNet auth flow doesn't enforce expiration.
//
// SignToken is pure: it does not write to stderr or any other global
// stream. Orchestrators (RunUp, RunCreds) print DevSecretWarning to
// their injected errw once per batch — see captureCredentials in
// internal/localnet/up.go for the canonical example. Keeping SignToken
// pure lets tests assert on the returned token without race conditions
// on process-global output.
func SignToken(in CredentialInputs) (string, error) {
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
