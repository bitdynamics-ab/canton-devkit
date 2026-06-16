package types

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSchemaShape_GoldenPinsFieldLevelShape is the field-level
// structural pin. The other schema-pin layers
// (TestAllTopLevelResponses_CarrySchemaVersion here, the AST scan in
// internal/ui/handlers, and the regex grep in internal/ui) only
// assert that the single integer SchemaVersion constant matches and
// that each response carries a SchemaVersion field. None of them
// notice when a field is ADDED, REMOVED, RENAMED, or RETYPED on a
// shared response struct — yet that is the single most common way the
// Go wire shape drifts away from frontend/src/api.ts's hand-written
// `mirrors internal/...` interfaces while SchemaVersion silently stays
// at 1. The frontend then mis-decodes at runtime with no CI signal.
//
// This test closes that gap: it reflects over every shared shape in
// this package (and the external types they embed, e.g. skills.Skill),
// renders a canonical signature of each — json key + Go kind, in
// declaration order — and compares the whole set against the golden
// string committed below.
//
// Catch class: any structural edit to a shared type. The diff fails
// loudly and the message tells the contributor exactly what to do:
//
//  1. update wantSchemaShape to the new rendering, AND
//  2. mirror the change in frontend/src/api.ts (the corresponding
//     `export interface`), AND
//  3. bump SchemaVersion (+ a docs/limitations.md migration note) when
//     the change is wire-breaking (rename / remove / retype), so the
//     bootstrap handshake forces stale frontends to reload.
//
// The golden is the human checkpoint: regenerating it is a deliberate
// one-line edit visible in the diff, not something that
// happens by accident.
func TestSchemaShape_GoldenPinsFieldLevelShape(t *testing.T) {
	got := schemaShape(schemaShapeRoots())
	if got != wantSchemaShape {
		t.Errorf("shared API shape drifted from the golden.\n\n"+
			"A field was added/removed/renamed/retyped on a shape in\n"+
			"internal/api/types (or a type it embeds). This is the exact\n"+
			"drift class that silently breaks frontend/src/api.ts decoding\n"+
			"while SchemaVersion stays at %d.\n\n"+
			"If the change is intentional:\n"+
			"  1. mirror it in the matching `export interface` in frontend/src/api.ts\n"+
			"  2. bump types.SchemaVersion (+ docs/limitations.md note) if it is\n"+
			"     a rename/remove/retype (wire-breaking)\n"+
			"  3. replace wantSchemaShape below with the GOT block printed here\n\n"+
			"---- WANT (committed golden) ----\n%s\n"+
			"---- GOT (current code) ----\n%s\n",
			SchemaVersion, wantSchemaShape, got)
	}
}

// schemaShapeRoots is the hand-enumerated set of shared shapes whose
// field-level layout we pin. Every top-level response/request struct
// in this package belongs here; nested types (Endpoint, Party,
// Credential, ServiceStatus, PreflightSection/Check, SnapshotDatabase,
// skills.Skill) are reached transitively by the walker, so they do not
// need their own entry.
//
// Rule: when you add a new exported shape to this package, add it here
// too. A new top-level type that nobody references would otherwise
// escape the pin.
func schemaShapeRoots() []any {
	return []any{
		ContractsListResponse{},
		ContractDetailResponse{},
		DARListResponse{},
		DARUploadResponse{},
		DARVettingResponse{},
		DARVettingToggleResponse{},
		EnvExport{},
		Instance{},
		InstanceSummary{},
		ListResponse{},
		LogLine{},
		PreflightReport{},
		SkillsInstallResponse{},
		SkillsListResponse{},
		Snapshot{},
		TokenCreateRequest{},
		TokenRef{},
		TransactionsListResponse{},
		TxReplayResponse{},
	}
}

// schemaShape renders a deterministic, diff-friendly signature for the
// given root types and every struct type reachable from them. Output
// is one block per struct, blocks sorted by type name:
//
//	pkg.TypeName {
//	  json_key kind
//	  ...
//	}
//
// Fields are listed in declaration order (the order matters for the
// human reading the diff, and is stable across runs). The "kind" is
// the reflect.Kind of the field's type with element/key kinds spelled
// out for slices and maps, so a string→int retype is visible. We
// deliberately key on the JSON tag, not the Go field name: a JSON tag
// rename is a wire break even if the Go field keeps its name, and a Go
// field rename that preserves the tag is wire-compatible.
func schemaShape(roots []any) string {
	seen := map[reflect.Type]string{}
	var visit func(t reflect.Type)
	visit = func(t reflect.Type) {
		t = deref(t)
		if t.Kind() != reflect.Struct {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = "" // mark before recursing to break cycles
		var b strings.Builder
		fmt.Fprintf(&b, "%s {\n", typeName(t))
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			key := jsonKey(f)
			if key == "-" {
				continue // explicitly excluded from the wire
			}
			fmt.Fprintf(&b, "  %s %s\n", key, kindOf(f.Type))
			visit(f.Type)
		}
		b.WriteString("}")
		seen[t] = b.String()
	}
	for _, r := range roots {
		visit(reflect.TypeOf(r))
	}

	blocks := make([]string, 0, len(seen))
	for _, block := range seen {
		blocks = append(blocks, block)
	}
	sort.Strings(blocks)
	return strings.Join(blocks, "\n\n")
}

// deref unwraps pointer and (single-level) slice/array element types so
// the underlying struct is reached for recursion.
func deref(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			t = t.Elem()
		case reflect.Map:
			t = t.Elem()
		default:
			return t
		}
	}
}

// kindOf renders a field type's shape: scalars as their kind, and
// containers with their element/key kinds spelled out so a retype of
// the contained type (e.g. []string → []int) is caught.
func kindOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + kindOf(t.Elem())
	case reflect.Slice, reflect.Array:
		return "[]" + kindOf(t.Elem())
	case reflect.Map:
		return "map[" + kindOf(t.Key()) + "]" + kindOf(t.Elem())
	case reflect.Struct:
		return typeName(t)
	default:
		return t.Kind().String()
	}
}

// jsonKey returns the wire key for a struct field: the name portion of
// its json tag, falling back to the Go field name when no tag is set
// (Go's encoding/json default).
func jsonKey(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// typeName is the package-qualified type name (e.g. "types.Instance",
// "skills.Skill") so cross-package embeds are unambiguous in the
// golden.
func typeName(t reflect.Type) string {
	pkg := t.PkgPath()
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		pkg = pkg[i+1:]
	}
	if pkg == "" {
		return t.Name()
	}
	return pkg + "." + t.Name()
}

// wantSchemaShape is the committed golden. Regenerate by copying the
// GOT block from a failing TestSchemaShape_GoldenPinsFieldLevelShape
// run — and only after doing the api.ts mirror + SchemaVersion bump the
// failure message describes.
const wantSchemaShape = `skills.Skill {
  filename string
  name string
  description string
  body string
}

types.ContractDetail {
  contract_id string
  template_id string
  package_name string
  payload map[string]interface
  signatories []string
  observers []string
  created_at string
  created_offset int64
  created_update_id string
  archived bool
  archived_at string
  archived_offset int64
  archived_update_id string
}

types.ContractDetailResponse {
  schema_version int
  instance string
  role string
  contract types.ContractDetail
}

types.ContractRow {
  contract_id string
  template_id string
  payload map[string]interface
  signatories []string
  observers []string
  created_at string
  package_name string
  package_version string
}

types.ContractsListResponse {
  schema_version int
  instance string
  role string
  parties []string
  ledger_end int64
  contracts []types.ContractRow
  truncated bool
  limit int
}

types.Credential {
  role string
  user string
  audience string
  jwt string
}

types.DARListResponse {
  schema_version int
  instance string
  role string
  dars []types.DARRow
}

types.DARRow {
  main string
  name string
  version string
  description string
  vetted *bool
}

types.DARUploadResponse {
  schema_version int
  instance string
  results []types.DARUploadRoleResult
  total_uploaded int
}

types.DARUploadRoleResult {
  role string
  ok bool
  dar_ids []string
  count int
  error string
}

types.DARVettingResponse {
  schema_version int
  instance string
  main string
  participants []types.DARVettingRow
}

types.DARVettingRow {
  role string
  vetted bool
  error string
}

types.DARVettingToggleResponse {
  schema_version int
  instance string
  main string
  role string
  vetted bool
}

types.Endpoint {
  label string
  url string
  port int
  scheme string
}

types.EnvExport {
  schema_version int
  instance string
  vars map[string]string
}

types.Instance {
  schema_version int
  name string
  splice_version string
  status string
  created_at string
  uptime string
  compose_project string
  docker_network string
  container_prefix string
  project_dir string
  data_dir string
  services []types.ServiceStatus
  live_probe_failed bool
  endpoints []types.Endpoint
  parties []types.Party
  credentials map[string]types.Credential
}

types.InstanceSummary {
  name string
  status string
  splice_version string
  ports string
  started_ago string
  volume_size string
}

types.ListResponse {
  schema_version int
  instances []types.InstanceSummary
  warning string
}

types.LogLine {
  time string
  service string
  level string
  message string
}

types.Party {
  wallet string
  id string
  role string
}

types.PreflightCheck {
  label string
  result string
  detail string
  elapsed string
  remediation []string
}

types.PreflightReport {
  schema_version int
  ok bool
  sections []types.PreflightSection
  summary string
  error_code string
}

types.PreflightSection {
  title string
  checks []types.PreflightCheck
}

types.ServiceStatus {
  name string
  image string
  state string
  ports string
  cpu_pct string
  memory string
  profile string
}

types.SkillsInstallResponse {
  schema_version int
  target string
  dir string
  installed []string
  count int
  skipped []string
}

types.SkillsListResponse {
  schema_version int
  skills []skills.Skill
}

types.Snapshot {
  schema_version int
  instance string
  splice_version string
  created_at string
  devkit_version string
  database *types.SnapshotDatabase
}

types.SnapshotDatabase {
  engine string
  postgres_image string
  user string
  volume_suffix string
  database_count int
  size_bytes int64
  content_sha string
}

types.TokenCreateRequest {
  name string
  symbol string
  decimals int
  initial_supply string
  issuer string
}

types.TokenRef {
  name string
  symbol string
  decimals int
  initial_supply string
  issuer_party string
  instrument_id string
  created_at string
  status string
}

types.TransactionEvent {
  kind string
  contract_id string
  template string
  witnesses []string
}

types.TransactionRow {
  kind string
  offset int64
  update_id string
  workflow_id string
  command_id string
  record_time string
  synchronizer string
  event_count int
  events []types.TransactionEvent
}

types.TransactionsListResponse {
  schema_version int
  instance string
  role string
  parties []string
  ledger_end int64
  transactions []types.TransactionRow
  count int
  scanned_from int64
  window_truncated bool
}

types.TxReplayEvent {
  kind string
  node_id int32
  contract_id string
  template_id string
  choice string
  acting_parties []string
  consuming bool
  signatories []string
  observers []string
}

types.TxReplayResponse {
  schema_version int
  instance string
  parties []string
  update_id string
  offset int64
  workflow_id string
  effective_at string
  event_count int
  events []types.TxReplayEvent
}`
