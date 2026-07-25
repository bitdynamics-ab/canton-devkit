package types

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSchemaShape_GoldenPinsFieldLevelShape is the field-level
// structural pin. The other schema-pin layers only check that the
// SchemaVersion constant matches and each response carries the field;
// none catch a field ADDED, REMOVED, RENAMED, or RETYPED on a shared
// struct — the most common way the Go wire shape drifts from
// frontend/src/api.ts's hand-written interfaces while SchemaVersion
// stays at 1, so the frontend mis-decodes at runtime with no CI signal.
//
// This reflects over every shared shape (and embedded external types
// like skills.Skill), renders a canonical json-key+Go-kind signature in
// declaration order, and diffs it against the committed golden. On
// failure the message tells the contributor to mirror the change in
// api.ts, bump SchemaVersion if wire-breaking, and regenerate the golden.
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
// field-level layout we pin. Nested types are reached transitively by
// the walker; only add top-level (unreferenced) shapes here. A new
// exported shape not listed escapes the pin.
func schemaShapeRoots() []any {
	return []any{
		AnalyzerResponse{},
		AnalyzerStatusResponse{},
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
		Allocation{},
		AllocationSummary{},
		AllocationsResponse{},
		BatchResult{},
		TokenActivityEvent{},
		TokenCreateRequest{},
		TokenIdentityResponse{},
		TokenRef{},
		TransactionsListResponse{},
		TxReplayResponse{},
	}
}

// schemaShape renders a deterministic, diff-friendly signature for the
// root types and every struct reachable from them — one block per
// struct (sorted by type name), fields in declaration order as
// `json_key kind`. Keyed on the JSON tag, not the Go field name, since
// a tag rename is a wire break and a Go-only rename is not.
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

// kindOf renders a field type's shape: scalars as their kind, containers
// with element/key kinds spelled out so a []string→[]int retype is caught.
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

// wantSchemaShape is the committed golden. Regenerate from a failing
// run's GOT block — only after the api.ts mirror + SchemaVersion bump.
const wantSchemaShape = `skills.Skill {
  filename string
  name string
  description string
  body string
}

types.Allocation {
  contract_id string
  status string
  settlement_id string
  admin string
  authorizer string
  executors []string
  committed bool
  settlement_deadline string
  transfer_legs []types.TokenTransferLeg
  created_at string
}

types.AllocationSummary {
  contract_id string
  status string
  settlement_id string
  authorizer string
  leg_count int
  committed bool
}

types.AllocationsResponse {
  schema_version int
  allocations []types.AllocationSummary
  aliases map[string]string
}

types.AnalyzerEndpoint {
  package string
  version string
  package_id string
  module string
  template string
  interface string
  choice string
  consuming *bool
}

types.AnalyzerInteraction {
  type string
  source *types.AnalyzerSource
  caller types.AnalyzerEndpoint
  target types.AnalyzerEndpoint
}

types.AnalyzerPackage {
  name string
  version string
  package_id string
  lf_version string
}

types.AnalyzerPackageRef {
  name string
  version string
  package_id string
}

types.AnalyzerReport {
  analyzed_package types.AnalyzerPackage
  dependencies []types.AnalyzerPackageRef
  summary types.AnalyzerSummary
  interactions []types.AnalyzerInteraction
}

types.AnalyzerResponse {
  schema_version int
  instance string
  dar_name string
  package_id string
  report *types.AnalyzerReport
}

types.AnalyzerSource {
  package string
  file string
  start_line *int
}

types.AnalyzerStatusResponse {
  schema_version int
  available bool
  runtime string
  source string
  detail string
}

types.AnalyzerSummary {
  total_interactions int
  by_type map[string]int
  by_target_package map[string]int
}

types.BatchActionResult {
  kind string
  ok bool
  detail string
}

types.BatchResult {
  update_id string
  actions []types.BatchActionResult
  ok bool
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
  key string
  label string
  url string
  port int
  scheme string
  reachability string
  reachability_detail string
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

types.TokenActivityEvent {
  source string
  update_id string
  offset int64
  record_time string
  instrument_id string
  account string
  admin string
  consumed_holding_count int
  created_holding_count int
  transfer_legs []types.TokenTransferLeg
  reason string
}

types.TokenCreateRequest {
  name string
  symbol string
  decimals int
  initial_supply string
  issuer string
}

types.TokenIdentityResponse {
  schema_version int
  instance string
  available_roles []string
  current_role string
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

types.TokenTransferLeg {
  transfer_leg_id string
  side string
  otherside string
  amount string
  instrument_id string
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
