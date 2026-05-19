// Package v06 implements the Adapter contract for Splice LocalNet 0.6.x.
// It is the current default; the 0.5.x adapter in sibling package v05 is
// kept for users still on the 0.5 line.
//
// Differences from 0.5.x (per upstream research, see BIT-107):
//   - 0.6.x adds env/alpha-protocol-version.env, wired into the `splice`
//     service via env_file (required:false). The corresponding
//     ALPHA_PROTOCOL_VERSION_ENV variable must point at that file.
//
// Everything else (compose.yaml structure, profile names, env-file list,
// container_name hardcoding, port-suffix scheme) is shared with 0.5.x.
package v06

import (
	"fmt"
	"path/filepath"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

type Adapter struct{}

// New returns a fresh 0.6.x adapter. Stateless — safe to reuse.
func New() *Adapter { return &Adapter{} }

func (*Adapter) MajorVersion() string { return "0.6" }

func (*Adapter) ComposeFiles() []string {
	// Order matters: resource-constraints.yaml merges over compose.yaml.
	return []string{"compose.yaml", "resource-constraints.yaml"}
}

func (*Adapter) EnvFiles() []string {
	return []string{"compose.env", "env/common.env"}
}

func (*Adapter) Profiles() []string {
	return []string{"sv", "app-provider", "app-user", "swagger-ui"}
}

func (*Adapter) RequiredPorts() []int {
	return []int{2000, 3000, 4000, 5432, 9090}
}

func (a *Adapter) OverlayEnv(p splice.InstanceParams) map[string]string {
	env := map[string]string{
		"LOCALNET_DIR":     p.ProjectDir,
		"LOCALNET_ENV_DIR": filepath.Join(p.ProjectDir, "env"),
		"IMAGE_TAG":        p.Version.Tag,
		"DOCKER_NETWORK":   p.Name,
		"PARTY_HINT":       p.Name + "-localparty-1",
		"COMPOSE_PROFILES": "sv,app-provider,app-user,swagger-ui",
	}
	if a.SupportsAlphaProtocol() {
		env["ALPHA_PROTOCOL_VERSION_ENV"] = filepath.Join(p.ProjectDir, "env", "alpha-protocol-version.env")
	}
	if p.Ephemeral {
		// Empty (not "1"): `${TEST_PORT-DEFAULT}` returns DEFAULT only
		// when TEST_PORT is unset. Set-but-empty makes the expression
		// collapse to just the container port, so Docker assigns a
		// random host port. This pattern covers the canton participant
		// ports only — UI ports use UIPortOverrides below.
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

// EndpointServices maps each logical endpoint to the compose service +
// container port. nginx fronts the three web UIs (sv, app-provider,
// app-user) at container ports 2000/3000/4000; postgres listens on 5432;
// swagger-ui is its own service on container port 8080 → host 9090.
func (*Adapter) EndpointServices() map[string]splice.ServicePort {
	return map[string]splice.ServicePort{
		"app_user_ui":     {Service: "nginx", ContainerPort: 2000},
		"app_provider_ui": {Service: "nginx", ContainerPort: 3000},
		"sv_ui":           {Service: "nginx", ContainerPort: 4000},
		"postgres":        {Service: "postgres", ContainerPort: 5432},
		"swagger_ui":      {Service: "swagger-ui", ContainerPort: 8080},
	}
}

func (*Adapter) SupportsAlphaProtocol() bool { return true }
