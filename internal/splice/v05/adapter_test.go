package v05

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestAdapterContract(t *testing.T) {
	a := New()

	if a.MajorVersion() != "0.5" {
		t.Errorf("MajorVersion = %q", a.MajorVersion())
	}

	if a.SupportsAlphaProtocol() {
		t.Errorf("0.5.x must NOT advertise alpha protocol support")
	}

	env := a.OverlayEnv(splice.InstanceParams{
		Name:       "bob",
		Version:    splice.Version{Tag: "0.5.18", Major: "0.5"},
		ProjectDir: "/tmp/cache/splice-0.5.18",
	})
	if _, has := env["ALPHA_PROTOCOL_VERSION_ENV"]; has {
		t.Errorf("0.5.x adapter must not emit ALPHA_PROTOCOL_VERSION_ENV")
	}
	for _, key := range []string{
		"LOCALNET_DIR", "LOCALNET_ENV_DIR", "IMAGE_TAG",
		"DOCKER_NETWORK", "PARTY_HINT",
	} {
		if _, ok := env[key]; !ok {
			t.Errorf("OverlayEnv missing %q", key)
		}
	}
}

var _ splice.Adapter = (*Adapter)(nil)
