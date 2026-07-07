package types

// ServiceStatus is one row in the Services table on `status` /
// dashboard. Sourced from `docker compose ps` for an instance.
//
// State is one of {"healthy", "syncing", "unhealthy", "exited",
// "starting", "disabled"} — superset of docker compose's own
// status because we collapse "running+healthy" and surface
// "syncing" specifically (initial bring-up state we report to the
// user; see internal/docker/compose.go classifyHealth).
type ServiceStatus struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Ports   string `json:"ports,omitempty"` // pre-formatted "4441, 4442"
	CPUPct  string `json:"cpu_pct,omitempty"`
	Memory  string `json:"memory,omitempty"`  // "740MiB"
	Profile string `json:"profile,omitempty"` // sv | app-provider | …
}

// Reachability values for Endpoint.Reachability. Wire-stable: the
// frontend switches on these literals.
const (
	ReachabilityOK          = "ok"
	ReachabilityUnreachable = "unreachable"
)

// Endpoint is one row under "Endpoints" in status / dashboard.
// Key is the stable logical port name from state.json; match on it
// to find a specific endpoint. Label is display-only.
// Scheme is the URL scheme used to construct a clickable link:
// http, https, grpc, grpcs, postgresql.
type Endpoint struct {
	Key    string `json:"key"`              // "app_user_ui"
	Label  string `json:"label"`            // "Ledger API · app-provider"
	URL    string `json:"url"`              // "grpc://localhost:3901"
	Port   int    `json:"port,omitempty"`   // 3901
	Scheme string `json:"scheme,omitempty"` // "grpc"

	// Reachability is the status-time HTTP probe verdict for browser
	// UI endpoints ("ok" | "unreachable"). Empty when the endpoint was
	// not probed: non-UI schemes, stopped instances, or --no-live.
	Reachability string `json:"reachability,omitempty"`
	// ReachabilityDetail explains an "unreachable" verdict in human
	// terms ("connection accepted but no HTTP response", …).
	ReachabilityDetail string `json:"reachability_detail,omitempty"`
}

// Party is one row under "Parties" / "Identities". Wallet is the
// human label (alice, app-provider, …); ID is the canonical
// `party::1220…` identifier from Canton.
type Party struct {
	Wallet string `json:"wallet"`
	ID     string `json:"id"`
	Role   string `json:"role,omitempty"` // sv | app-provider | app-user | DSO
}

// Credential mirrors registry.Credential. Re-declared in this
// package so internal/api/types stays free of an
// internal/registry import (which would invite a cycle once handlers
// in internal/ui import both packages).
type Credential struct {
	Role     string `json:"role"`
	User     string `json:"user"`
	Audience string `json:"audience"`
	JWT      string `json:"jwt"`
}
