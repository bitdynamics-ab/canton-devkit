package v06

import (
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestAdapterContract(t *testing.T) {
	a := New()

	if a.MajorVersion() != "0.6" {
		t.Errorf("MajorVersion = %q", a.MajorVersion())
	}

	cf := a.ComposeFiles()
	if len(cf) != 2 || cf[0] != "compose.yaml" || cf[1] != "resource-constraints.yaml" {
		t.Errorf("ComposeFiles = %v", cf)
	}

	if !a.SupportsAlphaProtocol() {
		t.Errorf("0.6.x must support alpha protocol")
	}

	env := a.OverlayEnv(splice.InstanceParams{
		Name:       "alice",
		Version:    splice.Version{Tag: "0.6.4", Major: "0.6"},
		ProjectDir: "/tmp/cache/splice-0.6.4",
	})
	for _, key := range []string{
		"LOCALNET_DIR", "LOCALNET_ENV_DIR", "IMAGE_TAG",
		"DOCKER_NETWORK", "PARTY_HINT", "COMPOSE_PROFILES",
		"ALPHA_PROTOCOL_VERSION_ENV", // 0.6.x specific
	} {
		if _, ok := env[key]; !ok {
			t.Errorf("OverlayEnv missing %q", key)
		}
	}
	if env["IMAGE_TAG"] != "0.6.4" {
		t.Errorf("IMAGE_TAG = %q", env["IMAGE_TAG"])
	}
	if !strings.Contains(env["ALPHA_PROTOCOL_VERSION_ENV"], "alpha-protocol-version.env") {
		t.Errorf("alpha env path looks wrong: %q", env["ALPHA_PROTOCOL_VERSION_ENV"])
	}
}

// Compile-time check that Adapter satisfies the splice.Adapter contract.
var _ splice.Adapter = (*Adapter)(nil)
