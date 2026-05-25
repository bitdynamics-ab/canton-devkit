// Package types defines the data shapes emitted by every CLI
// `--json` flag and consumed by the future Web UI handlers in
// internal/ui/handlers. It is the single source of truth: a field
// added here flows into both surfaces by serializing the same Go
// struct.
//
// Design rules:
//
//  1. JSON tags use snake_case; matches existing
//     internal/registry.State on disk and the JSX mockup keys.
//  2. Schema versioned per top-level response (`schema_version` int)
//     so a future field rename is detectable.
//  3. No methods. Pure data — keeps the package importable from any
//     layer without inviting circular deps with internal/registry.
//  4. Mirrors registry.State and registry.Credential for the
//     instance-shaped responses, but lives in its own package so:
//     (a) the public CLI/Web-UI contract is reviewable independently,
//     (b) registry can evolve internal-only fields without breaking
//     external JSON consumers.
//
// When changing this package, run `go test ./internal/cli/...` to
// catch any --json golden-file drift.
package types

// SchemaVersion is the current shape version for every top-level
// response struct in this package. Bumped (+ a migration note in
// docs/limitations.md) when a field is renamed or removed.
const SchemaVersion = 1
