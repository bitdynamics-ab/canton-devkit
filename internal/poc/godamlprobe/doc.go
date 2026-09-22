// Package godamlprobe dials a Canton participant Ledger API endpoint using
// github.com/noders-team/go-daml and reports whether the participant accepts
// the call (connectivity + auth).
//
// Production readiness (localnet up / start / restart after Docker health)
// calls Probe via internal/localnet.ensureLedgerReady. Command paths that
// submit ledger transactions continue to use internal/canton/ledger
// (dazl-client); this package is the connectivity probe only.
package godamlprobe
