package splice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSignTokenContract pins the (algorithm, secret) pair to Splice's
// dev-only "unsafe" HS256 contract. If upstream Splice ever changes
// the secret or algorithm, this test fails loudly so DevKit's
// `localnet creds` doesn't silently start emitting invalid JWTs.
func TestSignTokenContract(t *testing.T) {
	tok, err := SignToken(CredentialInputs{
		Role:     RoleSV,
		User:     "ledger-api-user",
		Audience: "https://canton.network.global",
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want JWT with 3 parts, got %d (%q)", len(parts), tok)
	}

	// Header must specify HS256.
	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var hdr map[string]string
	if err := json.Unmarshal(hdrJSON, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr["alg"] != "HS256" {
		t.Errorf("alg = %q, want HS256", hdr["alg"])
	}

	// Signature must validate against the "unsafe" secret.
	want := hmac.New(sha256.New, []byte("unsafe"))
	want.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := base64.RawURLEncoding.EncodeToString(want.Sum(nil))
	if parts[2] != wantSig {
		t.Errorf("signature mismatch")
	}

	// Payload round-trips.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sub"] != "ledger-api-user" || payload["aud"] != "https://canton.network.global" {
		t.Errorf("payload mismatch: %v", payload)
	}
}

func TestLoadCredentialInputs(t *testing.T) {
	tmp := t.TempDir()
	envDir := filepath.Join(tmp, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"sv-auth-on.env": `AUTH_SV_AUDIENCE=https://example.com
AUTH_SV_VALIDATOR_USER_NAME=sv-user`,
		"app-provider-auth-on.env": `AUTH_APP_PROVIDER_AUDIENCE=https://example.com
AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=app-provider-user`,
		"app-user-auth-on.env": `AUTH_APP_USER_AUDIENCE=https://example.com
AUTH_APP_USER_VALIDATOR_USER_NAME=app-user-user`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := LoadCredentialInputs(tmp)
	if err != nil {
		t.Fatalf("LoadCredentialInputs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 inputs, got %d", len(got))
	}
	want := map[Role]string{
		RoleSV:          "sv-user",
		RoleAppProvider: "app-provider-user",
		RoleAppUser:     "app-user-user",
	}
	for _, in := range got {
		if want[in.Role] != in.User {
			t.Errorf("role %s: User = %q, want %q", in.Role, in.User, want[in.Role])
		}
		if in.Audience != "https://example.com" {
			t.Errorf("role %s: bad audience %q", in.Role, in.Audience)
		}
	}
}

func TestLoadCredentialInputs_MissingFile(t *testing.T) {
	_, err := LoadCredentialInputs(t.TempDir())
	if err == nil {
		t.Error("expected error for empty project dir")
	}
}
