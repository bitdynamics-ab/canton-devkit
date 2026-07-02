// Package types defines the data shapes emitted by every CLI
// `--json` flag and by the Web UI handlers in internal/ui/handlers.
// It is the single source of truth: both surfaces serialize the same
// Go structs, so a field added here lands on both and the two cannot
// drift. Splitting the package would force the CLI to maintain a
// parallel shape and reintroduce exactly that drift.
//
// Design rules:
//
//  1. JSON tags use snake_case; matches internal/registry.State on
//     disk and frontend/src/api.ts.
//  2. Schema versioned per top-level response (`schema_version` int)
//     so a future field rename is detectable. The field-level shape
//     of every response is pinned against a golden by
//     TestSchemaShape_GoldenPinsFieldLevelShape, so any add / remove /
//     rename / retype that drifts from frontend/src/api.ts is caught
//     in CI rather than at the user's runtime.
//  3. Pure data, no business logic — keeps the package importable
//     from any layer without inviting circular deps with
//     internal/registry.
//  4. Mirrors registry.State and registry.Credential for the
//     instance-shaped responses, but lives in its own package so the
//     public CLI/Web-UI contract is reviewable independently and
//     registry can evolve internal-only fields without breaking
//     external JSON consumers.
//
// When changing this package, run `go test ./internal/cli/...` to
// catch any --json golden-file drift.
package types

// SchemaVersion is the current shape version for every top-level
// response struct in this package. Bumped (+ a migration note in
// docs/limitations.md) on any wire-breaking change — a field renamed,
// removed, or retyped. Purely additive changes stay
// backward-compatible and need only the golden + api.ts mirror
// update that TestSchemaShape_GoldenPinsFieldLevelShape forces.
const SchemaVersion = 1
