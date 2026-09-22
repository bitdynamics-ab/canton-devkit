// Package godamlprobe is an experimental PoC that dials a Canton participant
// Ledger API endpoint using github.com/noders-team/go-daml and reports whether
// the participant accepts the call (connectivity + auth).
//
// This package is intentionally NOT wired into production readiness paths
// (ComposeRunner.WaitForHealthy, localnet doctor, or the Web UI reconciler).
// Production ledger dials continue to use internal/canton/ledger (dazl-client).
//
// The PoC exists to evaluate whether go-daml is a viable alternative client
// for a cheap unary probe (GetLedgerApiVersion + GetLedgerEnd) against LocalNet.
package godamlprobe
