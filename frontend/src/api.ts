// API client for the canton-devkit Web UI backend.
//
// Two contracts the backend (internal/ui) defines and this file
// consumes:
//
//  1. SCHEMA_VERSION handshake on bootstrap. Every top-level
//     response and event carries `schema_version`. If it doesn't
//     match what this bundle was built against, the UI refuses
//     to render and tells the user to restart `dpm localnet ui`.
//
//  2. Error envelope shape mirrors PR #36 / handlers/errorBody:
//     { code, error, detail?, remediation? }. We surface `error`
//     as a toast and `remediation` as a follow-up action.
//
// All API calls go through `apiFetch` so the schema check, error
// envelope, and credentials posture stay in one place.

export const SCHEMA_VERSION = 1;

export class ApiError extends Error {
  status: number;
  code: string;
  detail?: string;
  remediation?: string[];

  constructor(status: number, body: ApiErrorBody) {
    super(body.error || `HTTP ${status}`);
    this.status = status;
    this.code = body.code;
    this.detail = body.detail;
    this.remediation = body.remediation;
  }
}

interface ApiErrorBody {
  code: string;
  error: string;
  detail?: string;
  remediation?: string[];
}

// apiFetch is the single chokepoint. Every API call routes through
// here so:
//   - same-origin policy is enforced (we never call cross-origin)
//   - error envelope is decoded uniformly
//   - schema version drift surfaces consistently
export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  if (!path.startsWith("/")) {
    throw new Error(`apiFetch path must be absolute, got ${path}`);
  }
  const resp = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const text = await resp.text();
  const body = text ? (JSON.parse(text) as unknown) : undefined;
  if (!resp.ok) {
    throw new ApiError(resp.status, (body as ApiErrorBody) ?? { code: "UNKNOWN", error: resp.statusText });
  }
  return body as T;
}

// VersionResponse mirrors internal/ui/router.go versionPayload.
export interface VersionResponse {
  name: string;
  schema_version: number;
}

// InstanceSummary mirrors internal/api/types.InstanceSummary.
export interface InstanceSummary {
  name: string;
  status: string;
  splice_version: string;
  ports: string;
  started_ago: string;
  volume_size?: string;
}

export interface ListResponse {
  schema_version: number;
  instances: InstanceSummary[];
  warning?: string;
}

// Instance mirrors internal/api/types.Instance (subset; full shape
// has Services/Endpoints/Parties/Credentials once the live probe
// lands — added per-screen as those land).
export interface Instance {
  schema_version: number;
  name: string;
  splice_version: string;
  status: string;
  created_at: string;
  uptime?: string;
  compose_project: string;
  docker_network: string;
  container_prefix: string;
  project_dir: string;
  data_dir: string;
  live_probe_failed?: boolean;
}

// fetchVersion is the bootstrap handshake. Returns the server's
// schema_version; callers compare against SCHEMA_VERSION and
// refuse to render on mismatch.
export const fetchVersion = () => apiFetch<VersionResponse>("/api/version");

export const fetchInstances = () => apiFetch<ListResponse>("/api/instances");

export const fetchInstance = (name: string) =>
  apiFetch<Instance>(`/api/instances/${encodeURIComponent(name)}`);

// ContainerHealth mirrors internal/ui/handlers/instances.go
// ContainerHealth — one row from `docker compose ps --all`.
// state + health are the diagnostic pair the UI surfaces:
//   state    = docker container state (running, restarting,
//              exited, paused, dead, created)
//   health   = healthcheck verdict (healthy, starting,
//              unhealthy, "" when no healthcheck)
// status     = raw human string from docker
//              (e.g. "Up 4 minutes (health: starting)")
export interface ContainerHealth {
  name: string;
  service: string;
  state: string;
  health?: string;
  status: string;
  image?: string;
}

export interface ContainersResponse {
  schema_version: number;
  instance: string;
  containers: ContainerHealth[];
  healthy_count: number;
  starting_count: number;
  unhealthy_count: number;
  restarting_count: number;
  exited_count: number;
}

// fetchContainers powers the live ContainerHealth panel — polled
// every ~3s while the panel is visible. Stateless on the server
// (re-runs docker compose ps), so cheap to spam.
export const fetchContainers = (name: string) =>
  apiFetch<ContainersResponse>(`/api/instances/${encodeURIComponent(name)}/containers`);

// restartContainer hits POST .../containers/{container}/restart.
// 204 on success, 5xx with cause on failure. The handler
// validates the container belongs to the named instance's
// compose project, so arbitrary host containers can't be poked.
export async function restartContainer(
  instance: string,
  container: string,
): Promise<void> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(instance)}/containers/${encodeURIComponent(container)}/restart`,
    { method: "POST" },
  );
  if (!resp.ok) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
      // non-JSON; default body retained
    }
    throw new ApiError(resp.status, body);
  }
}

// fetchContainerLogs hits the docker-logs tail endpoint. Returns
// plain text (already escaped for HTML safety — the frontend
// renders in a <pre>). tail/since map to the server's query
// params (200/empty defaults; tail clamped server-side to
// [10, 2000]).
//
// Errors: 404 if the named container isn't in the instance's
// compose project; 503 if docker is unreachable.
export async function fetchContainerLogs(
  instance: string,
  container: string,
  opts: { tail?: number; since?: string } = {},
): Promise<string> {
  const qs = new URLSearchParams();
  if (opts.tail !== undefined) qs.set("tail", String(opts.tail));
  if (opts.since) qs.set("since", opts.since);
  const q = qs.toString() ? `?${qs.toString()}` : "";
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(instance)}/containers/${encodeURIComponent(container)}/logs${q}`,
  );
  if (!resp.ok) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
      // non-JSON; default body retained
    }
    throw new ApiError(resp.status, body);
  }
  return resp.text();
}

// JwtRequest mirrors internal/ui/handlers/auth.go jwtRequest.
export interface JwtRequest {
  role?: string;
  ttl_seconds?: number;
  audience?: string;
}

// JwtResponse mirrors internal/ui/handlers/auth.go jwtResponse.
// `token` is the redaction placeholder ("<redacted>") unless
// the request was made with ?include_jwt=true; `redacted`
// signals which path.
export interface JwtResponse {
  schema_version: number;
  token: string;
  redacted?: boolean;
  party: string;
  audience: string;
  role: string;
  warning_dev_secret: string;
  expires_in_seconds?: number;
}

// issueJwt posts to the JWT endpoint. `includeJwt=true` triggers
// the raw-token mode — UI surfaces it ONLY after the user clicks
// "show token" so the response stays redacted-by-default for
// screenshot shares.
export function issueJwt(
  name: string,
  req: JwtRequest,
  includeJwt: boolean,
): Promise<JwtResponse> {
  const qs = includeJwt ? "?include_jwt=true" : "";
  return apiFetch<JwtResponse>(
    `/api/instances/${encodeURIComponent(name)}/jwt${qs}`,
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}

// AppConfigFormat is the query-param `format=` whitelist.
export type AppConfigFormat = "env" | "json" | "yaml";

// fetchAppConfigText returns the .env or YAML body as plain
// text — the env / yaml endpoints emit text/plain so apiFetch's
// JSON-decode path would error. Inline a small fetch here that
// returns the body verbatim.
export async function fetchAppConfigText(
  name: string,
  format: "env" | "yaml",
): Promise<string> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/app-config?format=${format}`,
  );
  if (!resp.ok) {
    const body = await resp.text();
    throw new ApiError(resp.status, { code: "APP_CONFIG", error: body });
  }
  return resp.text();
}

// AppConfigPayload mirrors internal/ui/handlers/auth.go
// appConfigPayload (the JSON shape).
export interface AppConfigPayload {
  schema_version: number;
  name: string;
  splice_version: string;
  endpoints: Record<string, string>;
  parties: Record<string, string>;
}

export const fetchAppConfigJSON = (name: string) =>
  apiFetch<AppConfigPayload>(
    `/api/instances/${encodeURIComponent(name)}/app-config?format=json`,
  );

// ── BIT-163d/e/f: create-instance flow ────────────────────────────

// SpliceVersionEntry mirrors internal/ui/handlers/splice_versions.go
// SpliceVersionEntry. Status taxonomy matches the version picker
// badges in webui-create.jsx (latest / supported / available /
// drifted / catalogued-only). Today the backend only emits
// latest+supported; the rest are reserved for the upstream check.
export interface SpliceVersionEntry {
  tag: string;
  status: "latest" | "supported" | "available" | "drifted" | "catalogued-only";
  major: string;
  commit: string;
  note?: string;
}

export interface SpliceVersionsResponse {
  schema_version: number;
  latest_alias: string;
  versions: SpliceVersionEntry[];
}

// fetchSpliceVersions powers the version picker in the create
// modal. Stateless GET; safe to call on every modal open.
export const fetchSpliceVersions = () =>
  apiFetch<SpliceVersionsResponse>("/api/splice/versions");

// CreateInstanceRequest mirrors handlers/instances.go upRequest.
// version="" defers to the server's "latest" alias.
export interface CreateInstanceRequest {
  name: string;
  version?: string;
  allow_uncurated?: boolean;
}

// CreateInstanceAcceptedResponse is what POST /api/instances
// returns on success (202). events_url is the relative path the
// modal opens an EventSource on for progress streaming.
export interface CreateInstanceAcceptedResponse {
  schema_version: number;
  instance: string;
  events_url: string;
}

// createInstance kicks off the bring-up. The HTTP request returns
// 202 immediately; progress arrives over the SSE stream at the
// returned events_url. Errors here are client-side
// (400 validation / 409 duplicate / 413 oversized / 422 preflight
// failure / 503 disabled). 422 carries a PreflightReport body, not
// the usual ApiError envelope — handled by createInstance via the
// X-Preflight-Failed response header so the modal can surface the
// findings inline instead of generic error text.
export async function createInstance(req: CreateInstanceRequest): Promise<CreateInstanceAcceptedResponse> {
  const resp = await fetch("/api/instances", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(req),
  });
  if (resp.status === 422 && resp.headers.get("X-Preflight-Failed")) {
    const report = (await resp.json()) as PreflightReport;
    throw new PreflightFailedError(report);
  }
  const text = await resp.text();
  if (!resp.ok) {
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try { body = JSON.parse(text); } catch { /* non-JSON */ }
    throw new ApiError(resp.status, body);
  }
  return JSON.parse(text) as CreateInstanceAcceptedResponse;
}

// PreflightReport mirrors internal/api/types.PreflightReport.
// Returned by GET /api/preflight?version=<tag> and as the body of
// a 422 from POST /api/instances when the host can't satisfy the
// chosen version's resource floor.
export interface PreflightReport {
  schema_version: number;
  ok: boolean;
  sections: PreflightSection[];
  summary?: string;
  // BIT-172 — stable machine-readable code populated when ok=false.
  // Values: PORTS_IN_USE | DOCKER_DOWN | DOCKER_NOT_INSTALLED |
  // COMPOSE_V1_OR_MISSING | DOCKER_MEMORY_LOW | DISK_LOW |
  // PREFLIGHT_FAILED. Frontend switches on this for targeted
  // remediation panels.
  error_code?: ErrorCode;
}

// ErrorCode — wire-stable strings from internal/localnet/coded_error.go.
// New values are non-breaking; renaming/repurposing is breaking.
// Keep this union in sync with the Go constants.
export type ErrorCode =
  | "PORTS_IN_USE"
  | "DOCKER_DOWN"
  | "DOCKER_NOT_INSTALLED"
  | "COMPOSE_V1_OR_MISSING"
  | "DOCKER_MEMORY_LOW"
  | "DISK_LOW"
  | "PREFLIGHT_FAILED"
  | "CANTON_OOM"
  | "CONTAINER_UNHEALTHY";

export interface PreflightSection {
  title: string;
  checks: PreflightCheck[];
}

export interface PreflightCheck {
  label: string;
  result: "pass" | "warn" | "fail" | "skip";
  detail?: string;
  remediation?: string[];
}

// PreflightFailedError is thrown by createInstance when the server
// rejects with a 422 preflight failure. The caller (typically the
// create modal) catches this and renders the report inline rather
// than the generic error envelope.
export class PreflightFailedError extends Error {
  report: PreflightReport;
  constructor(report: PreflightReport) {
    super(report.summary || "system requirements not met");
    this.report = report;
  }
}

// fetchPreflight runs the system-requirements gate for a version
// without queuing a bring-up. The create modal calls this on
// version-change so the Create button can be disabled (and the
// findings shown) BEFORE the user clicks — no need to round-trip
// through POST /api/instances just to discover an under-provisioned
// host. Always returns a PreflightReport (200) — `ok=false` is the
// signal to block, not a thrown error.
export const fetchPreflight = (version: string) =>
  apiFetch<PreflightReport>(
    `/api/preflight?version=${encodeURIComponent(version)}`,
  );

// stopInstance invokes POST /api/instances/{name}/down — runs
// `docker compose down` against the named instance and removes
// per-instance data unless { keep_data: true }. Synchronous on the
// wire (down is fast, ~10-30s on the happy path); the call blocks
// until the server returns 204 or 5xx.
//
// On failure, the server's error envelope includes a one-line
// summary the modal shows to the user; the full output goes to
// the server log.
export async function stopInstance(name: string, keepData = false): Promise<void> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/down`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ keep_data: keepData }),
    },
  );
  if (!resp.ok) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
      /* non-JSON; keep default */
    }
    throw new ApiError(resp.status, body);
  }
}

// scrubInstance invokes DELETE /api/instances/{name} — removes the
// registry entry entirely. Use for cleanup of zombie creating
// entries (e.g. server restart killed the goroutine mid-up,
// leaving an orphan record) or for failed instances the user
// wants to retry the name of.
//
// Backend refuses on `running` (409 INSTANCE_RUNNING — wants the
// real `down` flow) or while a job is actively creating (409
// INSTANCE_CREATING — caller should cancel /up first).
export async function scrubInstance(name: string): Promise<void> {
  const resp = await fetch(`/api/instances/${encodeURIComponent(name)}`, {
    method: "DELETE",
  });
  if (!resp.ok && resp.status !== 404) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
      /* non-JSON; keep default */
    }
    throw new ApiError(resp.status, body);
  }
}

// cancelInstanceUp invokes DELETE /api/instances/{name}/up. 204 on
// success; 404 if the goroutine already exited (idempotent). The
// SSE stream carries the synthetic kind=cancelled event the
// backend publishes before the actual cancel propagates.
export async function cancelInstanceUp(name: string): Promise<void> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/up`,
    { method: "DELETE" },
  );
  if (!resp.ok && resp.status !== 404) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
      /* non-JSON; keep default */
    }
    throw new ApiError(resp.status, body);
  }
}

// CreateProgressEvent — the discriminated-union of payloads
// SSEProgress publishes (internal/ui/progress/sse_progress.go).
// EventSource wire format: each event's `data:` line carries a
// JSON object the modal switches on by `kind`.
export type CreateProgressEvent =
  | StepStartedEvent
  | StepProgressEvent
  | StepFinishedEvent
  | StepFailedEvent
  | WarnEvent
  | DoneEvent
  | OutputEvent
  | CancelledEvent;

// step names mirror internal/localnet/progress.go Step constants.
// The modal renders a step row per name in display order.
export type StepName =
  | "resolve_version"
  | "acquire_lock"
  | "preflight"
  | "fetch_splice"
  | "persist_state"
  | "start_services"
  | "wait_healthy"
  | "capture_jwts";

export const STEP_ORDER: StepName[] = [
  "resolve_version",
  "acquire_lock",
  "preflight",
  "fetch_splice",
  "persist_state",
  "start_services",
  "wait_healthy",
  "capture_jwts",
];

export const STEP_LABELS: Record<StepName, string> = {
  resolve_version: "Resolve version + adapter",
  acquire_lock: "Acquire instance lock",
  preflight: "Run preflight checks",
  fetch_splice: "Fetch Splice LocalNet",
  persist_state: "Persist state + write overlay",
  start_services: "Starting services",
  wait_healthy: "Wait for services to become healthy",
  capture_jwts: "Capture JWTs · register endpoints",
};

interface StepStartedEvent {
  kind: "step.started";
  step: StepName;
  detail?: string;
}
interface StepProgressEvent {
  kind: "step.progress";
  step: StepName;
  detail?: string;
  percent?: number;
}
interface StepFinishedEvent {
  kind: "step.finished";
  step: StepName;
  detail?: string;
}
interface StepFailedEvent {
  kind: "step.failed";
  step: StepName;
  summary?: string;
  cause?: string;
  // BIT-172 — stable machine-readable code, populated when the
  // server recognized the failure mode (PORTS_IN_USE,
  // DOCKER_DOWN, DOCKER_MEMORY_LOW, etc.). useCreateProgress
  // surfaces this in the banner so the modal can render targeted
  // remediation panels instead of generic "failed" copy.
  error_code?: ErrorCode;
}
interface WarnEvent {
  kind: "warn";
  message: string;
}
interface DoneEvent {
  kind: "done";
  detail?: string;
}
interface OutputEvent {
  kind: "output";
  stream: "stdout" | "stderr";
  text: string;
}
interface CancelledEvent {
  kind: "cancelled";
  reason?: string;
}

// ApiErrorBody is re-exported here because cancelInstanceUp uses
// it directly (it bypasses apiFetch for the raw fetch path).
interface ApiErrorBody {
  code: string;
  error: string;
  detail?: string;
  remediation?: string[];
}
