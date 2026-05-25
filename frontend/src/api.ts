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
// (400 validation / 409 duplicate / 413 oversized / 503 disabled).
export const createInstance = (req: CreateInstanceRequest) =>
  apiFetch<CreateInstanceAcceptedResponse>("/api/instances", {
    method: "POST",
    body: JSON.stringify(req),
  });

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
