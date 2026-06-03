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
export interface Endpoint {
  label: string;
  url: string;
  port?: number;
  scheme?: string;
}

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
  /** Per-role wallet UI endpoints; populated by detail handler post-BIT-192. */
  endpoints?: Endpoint[];
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
// profiles maps to `dpm localnet up --profile ...` — currently the
// only allowed entry is "observability" (Prometheus + Grafana).
export interface CreateInstanceRequest {
  name: string;
  version?: string;
  allow_uncurated?: boolean;
  profiles?: string[];
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
// BIT-184 — snapshot / restore.
//
// downloadSnapshot triggers POST /api/instances/:name/snapshot and
// hands the gzipped tar to the browser via an <a download> click. We
// don't use fetch() + Blob here for one reason: a snapshot can be
// 100s of MB, and putting the whole body into JS memory just to hand
// it back to the browser is wasteful. The form-submit trick keeps the
// response entirely in the browser's download pipeline.
//
// Returns a Promise that resolves when the request is dispatched
// (not when the download completes — the browser owns that). Errors
// from the server arrive as a JSON body the browser displays as a
// download; we accept that UX limitation rather than buffer the tar
// just to surface a structured error toast.
export async function downloadSnapshot(name: string): Promise<void> {
  const form = document.createElement("form");
  form.method = "POST";
  form.action = `/api/instances/${encodeURIComponent(name)}/snapshot`;
  // Hidden iframe target avoids navigating away from the SPA on
  // success. Browsers attach the download attribute on the response
  // headers (Content-Disposition), so the iframe never actually
  // renders anything — the file goes straight to the downloads bar.
  form.target = "_dpm_dl";
  let frame = document.querySelector(
    'iframe[name="_dpm_dl"]',
  ) as HTMLIFrameElement | null;
  if (!frame) {
    frame = document.createElement("iframe");
    frame.name = "_dpm_dl";
    frame.style.display = "none";
    document.body.appendChild(frame);
  }
  document.body.appendChild(form);
  form.submit();
  document.body.removeChild(form);
}

export interface RestoreResponse {
  name: string;
  restored: boolean;
}

// restoreSnapshot uploads a snapshot tar to POST /api/instances/restore
// via multipart/form-data. We use XMLHttpRequest (not fetch) for
// upload progress events — fetch has no equivalent in Safari ≤17 and
// the snapshot UX needs a progress bar for 100-MB-class uploads.
//
// onProgress receives a fraction in [0, 1]; callers render whatever
// they like (bar, %, spinner with %). Resolves with the parsed
// response body on 2xx; rejects with ApiError otherwise.
export function restoreSnapshot(
  file: File,
  name: string,
  opts: { force?: boolean; onProgress?: (frac: number) => void } = {},
): Promise<RestoreResponse> {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append("name", name);
    fd.append("file", file);
    if (opts.force) fd.append("force", "true");
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/instances/restore");
    if (opts.onProgress) {
      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable && opts.onProgress) {
          opts.onProgress(e.loaded / e.total);
        }
      });
    }
    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as RestoreResponse);
        } catch (e) {
          reject(
            new ApiError(xhr.status, {
              code: "UNKNOWN",
              error: "response was not JSON",
            }),
          );
        }
        return;
      }
      let body: ApiErrorBody = { code: "UNKNOWN", error: xhr.statusText };
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        /* non-JSON; keep default */
      }
      reject(new ApiError(xhr.status, body));
    });
    xhr.addEventListener("error", () => {
      reject(new ApiError(0, { code: "NETWORK", error: "network error" }));
    });
    xhr.send(fd);
  });
}

export interface ResumeAcceptedResponse {
  schema_version: number;
  instance: string;
  events_url: string;
}

// resumeInstance invokes POST /api/instances/{name}/up — the
// "restart a stopped instance" verb. Backend kicks off the same
// goroutine + SSE shape as the create-instance flow, so the
// frontend can hand `response.events_url` straight to the
// existing progress modal without a separate code path.
//
// The recorded Splice version + ports are reused, so a resume
// won't silently upgrade or shuffle ports. Errors mirror the
// create flow: 404 if unregistered, 409 if running or already
// being brought up.
export async function resumeInstance(name: string): Promise<ResumeAcceptedResponse> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/up`,
    { method: "POST" },
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
  return (await resp.json()) as ResumeAcceptedResponse;
}

// BIT-188 — Metrics summary.
//
// The four curated headline numbers the CLI's
// `dpm localnet metrics --format json` also returns. Naming
// mirrors the `metricsq.Headline` Go constants so a frontend
// rename can't drift from the backend.
export interface MetricsSummary {
  schema_version: number;
  instance: string;
  metrics: {
    ledger_tps_5m?: number;
    mediator_p95_seconds?: number;
    jvm_heap_used_bytes?: number;
    postgres_conn_count?: number;
  };
}

// fetchMetricsSummary returns the headline panel data. The
// caller MUST handle ApiError with body.code === "OBSERVABILITY_PROFILE_OFF"
// to render the "raise observability" empty state — that's not
// a hard failure, just a missing profile.
// BIT-187 — DAR Manager.
//
// The Web UI lists DARs uploaded to a participant. Role defaults
// to app-user (the common dev target). The backend reads the
// per-role admin port from state.json (BIT-190) so the browser
// doesn't need to know about gRPC.
export type Role = "app-user" | "app-provider" | "sv";

export interface DARRow {
  main: string; // package id
  name: string;
  version: string;
  description?: string;
}

export interface DARListResponse {
  schema_version: number;
  instance: string;
  role: Role;
  dars: DARRow[];
}

export const fetchDARList = (name: string, role: Role = "app-user") =>
  apiFetch<DARListResponse>(
    `/api/instances/${encodeURIComponent(name)}/dar?role=${role}`,
  );

export interface DARUploadRoleResult {
  role: Role;
  ok: boolean;
  dar_ids?: string[];
  count: number;
  error?: string;
}

export interface DARUploadResponse {
  schema_version: number;
  instance: string;
  results: DARUploadRoleResult[];
  total_uploaded: number;
}

// uploadDARs posts a multipart body with one or more .dar files
// to /api/instances/:name/dar. Uses XMLHttpRequest for upload
// progress (the same pattern as BIT-184's BackupRestore).
//
// `roles` is the set of target participants — the backend dials
// each in parallel and returns a per-role success/error envelope.
// At least one role is required.
export function uploadDARs(
  name: string,
  files: File[],
  roles: Role[],
  onProgress?: (frac: number) => void,
): Promise<DARUploadResponse> {
  if (roles.length === 0) {
    return Promise.reject(
      new ApiError(400, {
        code: "INVALID_REQUEST",
        error: "select at least one target participant",
      }),
    );
  }
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    for (const r of roles) fd.append("roles", r);
    for (const f of files) fd.append("file", f);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/instances/${encodeURIComponent(name)}/dar`);
    if (onProgress) {
      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total);
      });
    }
    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as DARUploadResponse);
        } catch (e) {
          reject(
            new ApiError(xhr.status, {
              code: "UNKNOWN",
              error: "response was not JSON",
            }),
          );
        }
        return;
      }
      let body: ApiErrorBody = { code: "UNKNOWN", error: xhr.statusText };
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        /* keep default */
      }
      reject(new ApiError(xhr.status, body));
    });
    xhr.addEventListener("error", () =>
      reject(new ApiError(0, { code: "NETWORK", error: "network error" })),
    );
    xhr.send(fd);
  });
}

// BIT-186 — Explorer ACS snapshot.
export interface ContractRow {
  contract_id: string;
  template_id: string;
  payload?: Record<string, unknown>;
  signatories: string[];
  observers: string[];
  created_at?: string;
  package_name?: string;
}

export interface ContractsListResponse {
  schema_version: number;
  instance: string;
  role: Role;
  ledger_end: number;
  contracts: ContractRow[];
}

export const fetchContracts = (
  name: string,
  role: Role = "app-user",
  limit = 100,
) =>
  apiFetch<ContractsListResponse>(
    `/api/instances/${encodeURIComponent(name)}/contracts?role=${role}&limit=${limit}`,
  );

// BIT-186 follow-up — Transactions + Timeline.
//
// Each row in `transactions` represents one Canton update:
// transaction / reassignment / topology event. The frontend
// branches on `kind` to render either the table row or the
// timeline-strip glyph.
export interface TransactionEvent {
  kind: "create" | "archive" | "exercise";
  contract_id: string;
  template?: string;
  witnesses?: string[];
}

export interface TransactionRow {
  kind: "transaction" | "reassignment" | "topology" | "checkpoint";
  offset: number;
  update_id?: string;
  workflow_id?: string;
  command_id?: string;
  record_time?: string;
  synchronizer?: string;
  event_count?: number;
  events?: TransactionEvent[];
}

export interface TransactionsListResponse {
  schema_version: number;
  instance: string;
  role: Role;
  ledger_end: number;
  transactions: TransactionRow[];
  count: number;
}

export const fetchTransactions = (
  name: string,
  role: Role = "app-user",
  limit = 100,
) =>
  apiFetch<TransactionsListResponse>(
    `/api/instances/${encodeURIComponent(name)}/transactions?role=${role}&limit=${limit}`,
  );

export const fetchMetricsSummary = (name: string, signal?: AbortSignal) =>
  apiFetch<MetricsSummary>(
    `/api/instances/${encodeURIComponent(name)}/metrics/summary`,
    { signal },
  );

// Prometheus range-query response (subset). The backend's
// /metrics/range endpoint passes Prometheus's response through
// verbatim, so the frontend decodes the same shape Prometheus
// publishes.
export interface PrometheusRangeResponse {
  status: string;
  data?: {
    resultType?: string;
    result?: Array<{
      metric?: Record<string, string>;
      values?: Array<[number, string]>;
    }>;
  };
}

export const fetchMetricsRange = (
  name: string,
  query: string,
  window = "1h",
  step?: string,
  signal?: AbortSignal,
) => {
  const params = new URLSearchParams({ query, window });
  if (step) params.set("step", step);
  return apiFetch<PrometheusRangeResponse>(
    `/api/instances/${encodeURIComponent(name)}/metrics/range?${params.toString()}`,
    { signal },
  );
};

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

// ── Agent Skills (BIT-189) ─────────────────────────────────────────
// Mirrors internal/skills.Skill + the /api/skills handler. The same
// embedded docs back the CLI `localnet skills` command.
export interface Skill {
  filename: string;
  name: string;
  description: string;
  body: string;
}

export interface SkillsListResponse {
  schema_version: number;
  skills: Skill[];
}

export interface SkillsInstallResponse {
  schema_version: number;
  target: string;
  dir: string;
  installed: string[];
  count: number;
  // Files left untouched because an existing copy differs from the
  // bundled doc (server is clobber-safe by default). Re-install with
  // force=true to overwrite them. Mirrors skills.InstallResult.Skipped.
  skipped: string[];
}

export const fetchSkills = () => apiFetch<SkillsListResponse>("/api/skills");

// force overwrites locally-modified SKILL.md files that the server
// would otherwise preserve. Defaults to false to match the safe-by-
// default CLI (`skills install` without --force).
export const installSkills = (target: "claude" | "codex", force = false) =>
  apiFetch<SkillsInstallResponse>("/api/skills/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ target, force }),
  });

// -------------------------------------------------------------------
// BIT-140 Tokens — Web UI client for /api/tokens.
//
// Mirrors registry.TokenRef + the request shapes the backend handlers
// expect. Every action is instance-scoped (`?instance=`); error mapping
// matches handlers.mapTokenError so the UI can switch on the code.
// -------------------------------------------------------------------

export interface TokenRef {
  name: string;
  symbol: string;
  decimals: number;
  initial_supply: string;
  issuer_party: string;
  instrument_id: string;
  created_at: string;
  status: string;
}

export interface TokenHolding {
  instrument_symbol?: string;
  instrument_id: string;
  party: string;
  amount: string;
}

export interface TokensListResponse {
  schema_version: number;
  tokens: TokenRef[];
}

export interface TokenHoldingsResponse {
  schema_version: number;
  holdings: TokenHolding[];
}

export const fetchTokens = (instance: string) =>
  apiFetch<TokensListResponse>(
    `/api/tokens?instance=${encodeURIComponent(instance)}`,
  );

export const fetchHoldings = (
  instance: string,
  symbol: string,
  party?: string,
) => {
  const params = new URLSearchParams({ instance });
  if (party) params.set("party", party);
  return apiFetch<TokenHoldingsResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/holdings?${params}`,
  );
};

export interface TokenCreateInput {
  name: string;
  symbol: string;
  decimals: number;
  initial_supply: string;
  issuer: string;
}

export const createToken = (instance: string, body: TokenCreateInput) =>
  apiFetch<TokenRef>(
    `/api/tokens?instance=${encodeURIComponent(instance)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );

export const mintToken = (
  instance: string,
  symbol: string,
  to: string,
  amount: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/mint?instance=${encodeURIComponent(instance)}`,
    { to, amount },
  );

export const transferToken = (
  instance: string,
  symbol: string,
  from: string,
  to: string,
  amount: string,
  reason?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/transfer?instance=${encodeURIComponent(instance)}`,
    { from, to, amount, reason: reason ?? "" },
  );

export const burnToken = (
  instance: string,
  symbol: string,
  from: string,
  amount: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/burn?instance=${encodeURIComponent(instance)}`,
    { from, amount },
  );

export const acceptTransfer = (
  instance: string,
  transferInstructionID: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/transfers/${encodeURIComponent(transferInstructionID)}/accept?instance=${encodeURIComponent(instance)}`,
    {},
  );

// apiFetchVoid is a thin POST wrapper for 204-returning handlers. The
// mint/transfer/burn/accept endpoints return 204 on success and an
// ApiError on failure — no body to decode either way.
async function apiFetchVoid(path: string, body: unknown): Promise<void> {
  const resp = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (resp.ok) return;
  let parsed: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
  try {
    parsed = await resp.json();
  } catch {
    /* keep default */
  }
  throw new ApiError(resp.status, parsed);
}
