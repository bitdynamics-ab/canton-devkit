// Package v05 implements the Adapter contract for Splice LocalNet 0.5.x.
// See sibling package v06 for the differences vs 0.6.x — 0.5.x lacks the
// alpha-protocol-version env file.
package v05

import (
	"fmt"
	"path/filepath"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (*Adapter) MajorVersion() string { return "0.5" }

func (*Adapter) ComposeFiles() []string {
	return []string{"compose.yaml", "resource-constraints.yaml"}
}

func (*Adapter) EnvFiles() []string {
	return []string{"compose.env", "env/common.env"}
}

func (*Adapter) Profiles() []string {
	return []string{"sv", "app-provider", "app-user", "swagger-ui"}
}

func (*Adapter) OverlayEnv(p splice.InstanceParams) map[string]string {
	env := map[string]string{
		"LOCALNET_DIR":     p.ProjectDir,
		"LOCALNET_ENV_DIR": filepath.Join(p.ProjectDir, "env"),
		"IMAGE_TAG":        p.Version.Tag,
		"DOCKER_NETWORK":   p.Name,
		"PARTY_HINT":       splice.PartyHintFor(p.Name),
		"COMPOSE_PROFILES": "sv,app-provider,app-user,swagger-ui",
	}
	if p.Ephemeral {
		env["TEST_PORT"] = ""
	}
	for k, v := range p.UIPortOverrides {
		env[k] = fmt.Sprintf("%d", v)
	}
	return env
}

func (*Adapter) EndpointMap() map[string]int {
	return map[string]int{
		"app_user_ui":     2000,
		"app_provider_ui": 3000,
		"sv_ui":           4000,
		"postgres":        5432,
		"swagger_ui":      9090,
	}
}

func (*Adapter) EndpointServices() map[string]splice.ServicePort {
	return map[string]splice.ServicePort{
		"app_user_ui":     {Service: "nginx", ContainerPort: 2000},
		"app_provider_ui": {Service: "nginx", ContainerPort: 3000},
		"sv_ui":           {Service: "nginx", ContainerPort: 4000},
		"postgres":        {Service: "postgres", ContainerPort: 5432},
		"swagger_ui":      {Service: "swagger-ui", ContainerPort: 8080},
	}
}

func (*Adapter) SupportsAlphaProtocol() bool { return false }

// CoreServices is the BIT-222 contract — see internal/splice/adapter.go.
// 0.5.x's core stack matches the canonical set in splice.CoreServices.
func (*Adapter) CoreServices() []string {
	return splice.CoreServices()
}
