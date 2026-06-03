package splice

import "strings"

// PartyHintFor builds the Splice-compliant validator party hint from a
// DevKit instance name. Splice strictly validates the hint against
// `<organization>-<function>-<enumerator>` where organization +
// function are alphanumeric (underscores allowed, hyphens are the
// separator) and the enumerator is an integer.
//
// We allow `--name` to contain hyphens (e.g. `ci-local`, `my-stack`)
// because the CLI surface should be friendly. But concatenating
// `<name>-localparty-1` directly yields `ci-local-localparty-1` →
// 4 hyphen-segments after Splice's `app_user_` prefix is prepended in
// env/app-user-auth-on.env. Splice rejects with INVALID_ARGUMENT and
// the splice container crash-loops. Replacing hyphens with underscores
// in the name component keeps the segment count at 3 while preserving
// uniqueness across instance names.
//
// Example:
//
//	PartyHintFor("ci-local")   → "ci_local-localparty-1"
//	PartyHintFor("alice")      → "alice-localparty-1"
//
// After Splice's env-file prefix: `app_user_ci_local-localparty-1`,
// `app_user_alice-localparty-1` — both 3 segments, both valid.
func PartyHintFor(name string) string {
	return strings.ReplaceAll(name, "-", "_") + "-localparty-1"
}

// Adapter abstracts the per-major-version differences in how the Splice
// LocalNet compose project is invoked. One Adapter implementation per
// supported Splice major (0.5.x, 0.6.x, …) lives under
// internal/splice/v0X/.
//
// The contract is intentionally narrow: each method returns the bits
// that `localnet up` needs to assemble the canonical `docker compose`
// invocation for that version, plus the host ports preflight needs to
// check. Anything stateful (per-instance overlay generation, JWT
// signing) lives in dedicated packages and reads from this contract.
type Adapter interface {
	// MajorVersion returns the human-readable major slot, e.g. "0.6".
	MajorVersion() string

	// ComposeFiles returns the list of compose files to pass with -f,
	// relative to the cached project directory. Order matters: later
	// files override earlier.
	ComposeFiles() []string

	// EnvFiles returns the list of --env-file paths, relative to the
	// project dir. Order matters: later files override earlier.
	EnvFiles() []string

	// Profiles returns the default set of --profile values that bring
	// up the full LocalNet topology (super-validator + both apps +
	// swagger-ui at minimum).
	Profiles() []string

	// OverlayEnv returns the shell-exported variables that the compose
	// process needs. These are merged into cmd.Env on top of the
	// inherited process env. Per-instance values like PARTY_HINT come
	// from the InstanceParams argument.
	OverlayEnv(p InstanceParams) map[string]string

	// EndpointMap returns logical endpoint name → host port, for the
	// default (zero-offset) port assignment. localnet up uses this to
	// pretty-print the "ready" summary and to populate state.Ports.
	EndpointMap() map[string]int

	// EndpointServices returns logical endpoint name → (compose service,
	// container port). Used when running with TEST_PORT=1 (ephemeral
	// ports) to query the actual daemon-assigned host port via
	// `docker compose port <svc> <container-port>`.
	EndpointServices() map[string]ServicePort

	// SupportsAlphaProtocol reports whether the alpha-protocol-version
	// env file should be wired through. False on 0.5.x; true on 0.6.x.
	SupportsAlphaProtocol() bool
}

// InstanceParams is the small bag of per-instance facts the adapter
// needs to render its overlay env.
type InstanceParams struct {
	Name       string
	Version    Version // the Splice tag being booted
	ProjectDir string  // absolute path to cached compose project

	// Ephemeral, when true, sets TEST_PORT="" so the Splice canton
	// service publishes its participant ports on ephemeral host ports
	// (the `${TEST_PORT-N:}M` pattern in upstream compose.yaml).
	//
	// Ephemeral does NOT cover the UI ports (nginx/swagger/postgres) —
	// those have their own env vars. Pass overrides via UIPortOverrides
	// for those.
	Ephemeral bool

	// UIPortOverrides, when non-nil, replaces the default host ports
	// for the named UI services. Keys are the env var names compose
	// substitutes:
	//   APP_USER_UI_PORT, APP_PROVIDER_UI_PORT, SV_UI_PORT,
	//   SWAGGER_UI_PORT, DB_PORT
	// Values are the allocated host ports.
	UIPortOverrides map[string]int
}

// ServicePort pairs a compose service name with a container port —
// enough info for `docker compose port <svc> <port>` lookups.
type ServicePort struct {
	Service       string
	ContainerPort int
}
