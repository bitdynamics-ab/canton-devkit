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
//  2. Error envelope shape: { code, error, detail?, remediation? }.
//     We surface `error` as a toast and `remediation` as a
//     follow-up action.
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
  // A proxy or a panicking server can return a non-empty body that
  // is NOT our JSON envelope (an HTML 502 page, a plain-text stack
  // trace). Parsing must not throw a raw SyntaxError here — that
  // would escape every screen's `e instanceof ApiError` branch and
  // surface as an opaque "failed to load" with no status/code. Fall
  // back to `undefined` so the !resp.ok branch still emits a proper
  // ApiError (carrying the HTTP status), and a malformed 2xx body
  // surfaces as `undefined` rather than crashing the caller.
  let body: unknown;
  try {
    body = text ? (JSON.parse(text) as unknown) : undefined;
  } catch {
    body = undefined;
  }
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
// has Services/Endpoints/Parties/Credentials from the live probe).
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
  /** Per-role wallet UI endpoints; populated by the detail handler. */
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

// AppConfigPayload mirrors the shared apitypes.EnvExport
// (internal/api/types/env.go) the app-config handler now emits — the
// SAME shape `dpm localnet env --format=json` produces. `vars` carries
// the flat CANTON_<...> key/value set: endpoint ports (incl. the
// participant Ledger/Admin/JSON APIs and scan UI URL), per-role
// credentials, and the real on-ledger party ids.
export interface AppConfigPayload {
  schema_version: number;
  instance: string;
  vars: Record<string, string>;
}

export const fetchAppConfigJSON = (name: string) =>
  apiFetch<AppConfigPayload>(
    `/api/instances/${encodeURIComponent(name)}/app-config?format=json`,
  );

// ── create-instance flow ──────────────────────────────────

// SpliceVersionEntry mirrors internal/ui/handlers/splice_versions.go
// SpliceVersionEntry. Status taxonomy matches the version picker
// badges (latest / supported / available / drifted /
// catalogued-only). Today the backend only emits latest+supported;
// the rest are reserved for the upstream check.
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
  // port_base > 0 pins deterministic host ports from this base
  // (`dpm localnet up --port-base`). Omit / 0 → auto-allocate.
  port_base?: number;
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
  // stable machine-readable code populated when ok=false.
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
// `docker compose down` against the named instance, preserving
// Docker volumes and the registry entry (status=stopped).
// Synchronous on the wire (down is fast, ~10-30s on the happy
// path); the call blocks until the server returns 204 or 5xx.
//
// On failure, the server's error envelope includes a one-line
// summary the modal shows to the user; the full output goes to
// the server log.
// snapshot / restore.
//
// downloadSnapshot triggers POST /api/instances/:name/snapshot and
// hands the gzipped tar to the browser via an <a download> click. We
// don't use fetch() + Blob here for one reason: a snapshot can be
// 100s of MB, and putting the whole body into JS memory just to hand
// it back to the browser is wasteful. The form-submit trick keeps the
// response entirely in the browser's download pipeline.
//
// Error surfacing: on success the server replies with
// Content-Disposition: attachment, so the browser hands the body to
// the download manager and the hidden iframe never navigates — no
// `load` event fires. On failure (instance not found, docker error,
// 5xx) the server replies with an inline JSON error body and NO
// Content-Disposition, so the iframe DOES navigate to it and fires a
// `load` event. We listen for that asymmetry: a `load` on the iframe
// means the download did not happen and an error document was
// rendered instead, so we reject with an ApiError the caller can
// toast. A successful download is detected by absence (a settle
// timeout resolves once the dispatch is clean), since we cannot read
// the cross-document iframe body.
//
// Returns a Promise that REJECTS with an ApiError when the server
// returned an error instead of a download, and otherwise resolves
// once the request has been dispatched (not when the download
// completes — the browser owns that). The hidden iframe is reused
// across downloads (one per document), and each call attaches a
// one-shot `load` listener so handlers don't accumulate.
const DOWNLOAD_SETTLE_MS = 1200;

export function downloadSnapshot(name: string): Promise<void> {
  return new Promise<void>((resolve, reject) => {
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

    let settled = false;
    let settleTimer: ReturnType<typeof setTimeout> | undefined;
    const onLoad = () => {
      // The iframe navigated → the server returned an inline
      // document (the JSON error body) rather than an attachment.
      // A successful download hands the body to the download
      // manager and never navigates the iframe, so a `load` here
      // means the snapshot did NOT download. We can't read the
      // cross-document body safely, so surface a generic-but-honest
      // error instead of failing silently.
      if (settled) return;
      settled = true;
      if (settleTimer !== undefined) clearTimeout(settleTimer);
      frame?.removeEventListener("load", onLoad);
      reject(
        new ApiError(0, {
          code: "SNAPSHOT_DOWNLOAD_FAILED",
          error:
            "snapshot download failed — the server returned an error instead of a file",
        }),
      );
    };
    frame.addEventListener("load", onLoad);

    document.body.appendChild(form);
    form.submit();
    document.body.removeChild(form);

    // No `load` within the settle window → the body was taken by the
    // download manager (success).
    settleTimer = setTimeout(() => {
      if (settled) return;
      settled = true;
      frame?.removeEventListener("load", onLoad);
      resolve();
    }, DOWNLOAD_SETTLE_MS);
  });
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

// recreateInstance invokes POST /api/instances/{name}/recreate — a
// single-click "Restart" path that the backend implements as a
// serial down → up cycle. The backend reuses the instance's recorded
// SpliceVersion and infers the original profile set from the
// compose-files on disk, so a restart does not silently upgrade,
// regenerate credentials, or shed observability sidecars.
//
// Same async shape as resumeInstance: 202 with an events_url the UI
// hands to the existing progress modal. 404 if the name isn't in
// the registry; 409 INSTANCE_CREATING if a down/up/restart job is
// already in flight for the same name (the second click is a no-op
// by design — the original cycle continues uninterrupted).
export async function recreateInstance(name: string): Promise<ResumeAcceptedResponse> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/recreate`,
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

// Metrics summary.
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
    mediator_p50_seconds?: number;
    mediator_p95_seconds?: number;
    mediator_p99_seconds?: number;
    jvm_heap_used_bytes?: number;
    postgres_conn_count?: number;
  };
  // latency mirrors the CLI's `--format json` block: mediator
  // approval-duration percentiles in milliseconds. Nullable per
  // field so an empty Prometheus stays distinguishable from a
  // true 0 ms (matters during cold-start).
  latency?: {
    p50_ms?: number | null;
    p95_ms?: number | null;
    p99_ms?: number | null;
  };
  // dashboards.grafana_url is the deep link to the bundled
  // canton-localnet dashboard when the observability profile is
  // running, "" otherwise — the frontend renders a "raise
  // observability" CTA when empty.
  dashboards?: {
    grafana_url?: string;
  };
}

// fetchMetricsSummary returns the headline panel data. The
// caller MUST handle ApiError with body.code === "OBSERVABILITY_PROFILE_OFF"
// to render the "raise observability" empty state — that's not
// a hard failure, just a missing profile.
// DAR Manager.
//
// The Web UI lists DARs uploaded to a participant. Role defaults
// to app-user (the common dev target). The backend reads the
// per-role admin port from state.json so the browser
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
// progress (the same pattern as BackupRestore).
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

// DAR package-tree inspect.
//
// The inspect endpoint returns the deep Daml-LF structure of every
// .dalf inside the DAR: modules, templates (+ choices), interfaces
// (+ choices + methods), data types. The frontend renders this as
// a tree in DARPackageTree.tsx.
export interface DARTemplateContents {
  name: string;
  choices?: string[];
}
export interface DARIfaceContents {
  name: string;
  choices?: string[];
  methods?: string[];
}
export interface DARModuleContents {
  name: string;
  templates?: DARTemplateContents[];
  interfaces?: DARIfaceContents[];
  data_types?: string[];
}
export interface DARPackageInspect {
  package_id: string;
  name: string;
  version: string;
  lf_version: string;
  size_bytes: number;
  is_main: boolean;
  contents?: { modules: DARModuleContents[] };
}
export interface DARInspectResponse {
  schema_version: number;
  instance: string;
  role: Role;
  main: string;
  name?: string;
  version?: string;
  sha256: string;
  packages: DARPackageInspect[];
}

export const fetchDARInspect = (
  instance: string,
  mainID: string,
  role: Role = "app-user",
) =>
  apiFetch<DARInspectResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/${encodeURIComponent(mainID)}/inspect?role=${role}`,
  );

// Structural diff between two DARs (by main package id).
export interface DARDiffTemplateAdded {
  module: string;
  name: string;
  choices?: string[];
}
export interface DARDiffTemplateChanged {
  module: string;
  name: string;
  choices_added: string[];
  choices_removed: string[];
}
export interface DARDiffIfaceAdded {
  module: string;
  name: string;
  choices?: string[];
  methods?: string[];
}
export interface DARDiffIfaceChanged {
  module: string;
  name: string;
  choices_added: string[];
  choices_removed: string[];
  methods_added: string[];
  methods_removed: string[];
}
export interface DARDiffSide {
  main: string;
  name: string;
  version: string;
}
export interface DARDiffResponse {
  schema_version: number;
  instance: string;
  role: Role;
  a?: DARDiffSide;
  b?: DARDiffSide;
  modules_added: string[];
  modules_removed: string[];
  templates_added: DARDiffTemplateAdded[];
  templates_removed: DARDiffTemplateAdded[];
  templates_changed: DARDiffTemplateChanged[];
  interfaces_added: DARDiffIfaceAdded[];
  interfaces_removed: DARDiffIfaceAdded[];
  interfaces_changed: DARDiffIfaceChanged[];
}

export const fetchDARDiff = (
  instance: string,
  a: string,
  b: string,
  role: Role = "app-user",
) =>
  apiFetch<DARDiffResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/diff?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}&role=${role}`,
  );

// Per-participant vetting state + toggle.
export interface DARVettingRow {
  role: Role;
  vetted: boolean;
  error?: string;
}
export interface DARVettingResponse {
  schema_version: number;
  instance: string;
  main: string;
  participants: DARVettingRow[];
}

export const fetchDARVetting = (instance: string, mainID: string) =>
  apiFetch<DARVettingResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/${encodeURIComponent(mainID)}/vetting`,
  );

export interface DARVettingToggleResponse {
  schema_version: number;
  instance: string;
  main: string;
  role: Role;
  vetted: boolean;
}

export const setDARVetting = (
  instance: string,
  mainID: string,
  role: Role,
  vetted: boolean,
) =>
  apiFetch<DARVettingToggleResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/${encodeURIComponent(mainID)}/vetting/${role}`,
    {
      method: "POST",
      body: JSON.stringify({ vetted }),
    },
  );

// Hot-deploy watch indicator. SSE; not a fetch.
export interface DARWatchEvent {
  instance: string;
  dar_id?: string;
  event:
    | "rebuild_started"
    | "rebuild_finished"
    | "upload_finished"
    | "watch_started"
    | "watch_stopped";
  at: number;
  detail?: string;
}

// subscribeDARWatch opens an EventSource on the watch SSE endpoint.
// The caller is responsible for closing the returned EventSource
// (typically on component unmount).
export function subscribeDARWatch(
  instance: string,
  darID: string,
  onEvent: (ev: DARWatchEvent) => void,
): EventSource {
  const url =
    `/api/dar/watch/events?instance=${encodeURIComponent(instance)}` +
    (darID ? `&dar=${encodeURIComponent(darID)}` : "");
  const es = new EventSource(url);
  es.addEventListener("message", (e) => {
    try {
      const ev = JSON.parse(e.data) as DARWatchEvent;
      onEvent(ev);
    } catch {
      // Drop malformed payloads silently — the backend pins the
      // event-enum on the publish side, so a parse failure here
      // would indicate a wire-format change worth surfacing in
      // the network tab, not the UI.
    }
  });
  return es;
}

// Explorer ACS snapshot.
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

// Contract-detail drawer payload. Returned by
// GET /api/instances/{name}/contracts/{contract_id}; backed by
// EventQueryService.GetEventsByContractId on the participant.
export interface ContractDetail {
  contract_id: string;
  template_id?: string;
  package_name?: string;
  payload?: Record<string, unknown>;
  signatories: string[];
  observers: string[];
  created_at?: string;
  created_offset?: number;
  created_update_id?: string;
  archived: boolean;
  archived_at?: string;
  archived_offset?: number;
  archived_update_id?: string;
}

export interface ContractDetailResponse {
  schema_version: number;
  instance: string;
  role: Role;
  contract: ContractDetail;
}

export const fetchContractDetail = (
  name: string,
  contractId: string,
  role: Role = "app-user",
) =>
  apiFetch<ContractDetailResponse>(
    `/api/instances/${encodeURIComponent(name)}/contracts/${encodeURIComponent(contractId)}?role=${role}`,
  );

// Explorer live SSE stream events. The /contracts/stream
// endpoint emits one frame per ACS delta. Backend caps subscriptions
// at 10k events then flushes a final `truncated` payload — the
// frontend treats that as a signal to fall back to snapshot
// reconciliation.
export interface ContractStreamEvent {
  event: "created" | "archived" | "truncated";
  contract_id?: string;
  template?: string;
  signatories?: string[];
  observers?: string[];
  offset?: number;
  at?: number;
  update_id?: string;
  reason?: string;
}

// openContractsStream is a thin EventSource wrapper. The caller is
// responsible for closing the returned source on unmount; the
// backend cancels its upstream gRPC subscription via context as
// soon as the HTTP request goroutine returns.
//
// `since` is the offset the stream should resume FROM (exclusive).
// Callers should pass the `ledger_end` returned by the snapshot
// endpoint so the snapshot→stream handoff is a single atomic offset
// boundary — any create/archive between the snapshot and the
// stream's open would otherwise be missed until the periodic
// reconciliation refresh. Omit to begin from the live ledger end
// (strict-tail semantics — direct curl, smoke tests).
export function openContractsStream(
  name: string,
  role: Role = "app-user",
  since?: number,
): EventSource {
  const sinceParam =
    typeof since === "number" && Number.isFinite(since) && since >= 0
      ? `&since=${since}`
      : "";
  return new EventSource(
    `/api/instances/${encodeURIComponent(name)}/contracts/stream?role=${role}${sinceParam}`,
  );
}

// Transactions + Timeline.
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

// ObservabilityToggleResponse mirrors the POST
// /api/instances/{name}/observability response. `enabled` is the
// legacy synonym (true iff BOTH sidecars are on); the per-component
// booleans are canonical. Ports are present only when the matching
// sidecar is up.
export interface ObservabilityToggleResponse {
  schema_version: number;
  instance: string;
  prometheus: boolean;
  grafana: boolean;
  enabled: boolean;
  prometheus_ui?: number;
  grafana_ui?: number;
  warning?: string;
}

// setObservability toggles the Prometheus + Grafana sidecars on a
// running instance via the per-component HTTP body. Routed through
// apiFetch (the single chokepoint) so the envelope decoding +
// ApiError mapping apply uniformly. The CLI mirror is
// `dpm localnet observability enable|disable`.
export function setObservability(
  name: string,
  want: { prometheus: boolean; grafana: boolean },
): Promise<ObservabilityToggleResponse> {
  return apiFetch<ObservabilityToggleResponse>(
    `/api/instances/${encodeURIComponent(name)}/observability`,
    {
      method: "POST",
      body: JSON.stringify(want),
    },
  );
}

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

export async function stopInstance(name: string): Promise<void> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/down`,
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
}

// pauseInstance / resumeInstance invoke POST /api/instances/{name}/pause
// | /resume — docker compose pause/unpause. Near-instant; 204
// on success. Pause is valid only when running, resume only when paused.
export async function pauseInstance(name: string): Promise<void> {
  await postInstanceAction(name, "pause");
}

export async function unpauseInstance(name: string): Promise<void> {
  await postInstanceAction(name, "resume");
}

async function postInstanceAction(name: string, action: string): Promise<void> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/${action}`,
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
  // stable machine-readable code, populated when the
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

// ── Agent Skills ─────────────────────────────────────────
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
// Tokens — Web UI client for /api/tokens.
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

// DEFAULT_ROLE matches the backend default in roleFromQuery — keep the
// two in sync so the live-ledger endpoint discovery and JWT minting
// pick the same participant for unrestricted UI calls.
const DEFAULT_ROLE = "app-user";

// tokenQuery builds `?instance=...&role=...` (role omitted when it
// would just repeat the default). Centralised so every token endpoint
// forwards the role the same way the backend's handleTokenMint /
// handleTokenTransfer already expect.
function tokenQuery(instance: string, role?: string): string {
  const params = new URLSearchParams({ instance });
  if (role && role !== DEFAULT_ROLE) params.set("role", role);
  return params.toString();
}

export const fetchTokens = (instance: string, role?: string) =>
  apiFetch<TokensListResponse>(`/api/tokens?${tokenQuery(instance, role)}`);

export const fetchHoldings = (
  instance: string,
  symbol: string,
  party?: string,
  role?: string,
) => {
  const params = new URLSearchParams({ instance });
  if (party) params.set("party", party);
  if (role && role !== DEFAULT_ROLE) params.set("role", role);
  return apiFetch<TokenHoldingsResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/holdings?${params}`,
  );
};

// token workspace — ACS-derived lenses (instrument discovery,
// balance matrix, per-holding UTXO rows). These hit the live ledger;
// when no endpoint is recorded the backend falls back to the recorded
// token list (so `instruments` may be absent — callers handle both).

// InstrumentRef is an on-chain-discovered instrument (workspace.go).
export interface InstrumentRef {
  admin: string;
  instrument_id: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  standard?: string; // "Splice Amulet" | "CIP-0112 v2"
  on_ledger: boolean;
}

// HoldingContract is one HoldingV2 UTXO. A balance = sum of these.
export interface HoldingContract {
  contract_id: string;
  party: string;
  admin: string;
  instrument_id: string;
  amount: string;
  locked: boolean;
}

export interface MatrixCell {
  party: string;
  instrument_id: string;
  amount: string;
}

export interface BalanceMatrix {
  parties: string[];
  instruments: InstrumentRef[];
  cells: MatrixCell[];
  totals: MatrixCell[];
}

interface InstrumentsResponse {
  schema_version: number;
  instruments?: InstrumentRef[];
  tokens?: TokenRef[]; // fallback shape when no live endpoint
}

interface MatrixResponse {
  schema_version: number;
  matrix: BalanceMatrix;
}

// PartyRef — one registered party alias → its on-ledger party id.
// The workspace's god-mode party registry.
export interface PartyRef {
  alias: string;
  party_id: string;
  role: string;
  is_local?: boolean;
  created_at: string;
}

interface PartiesResponse {
  schema_version: number;
  parties: PartyRef[];
}

// fetchParties lists the instance's registered party aliases (seeding the
// role parties when a live endpoint is available).
export const fetchParties = (instance: string, role = "app-user") =>
  apiFetch<PartiesResponse>(
    `/api/parties?instance=${encodeURIComponent(instance)}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.parties);

// createParty allocates a party under an alias and grants the role's user
// act/read-as for it.
export const createParty = (instance: string, alias: string, role = "app-user") =>
  apiFetch<PartyRef>(`/api/parties?instance=${encodeURIComponent(instance)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ alias, role }),
  });

// removeParty forgets an alias (the on-ledger party persists).
export const removeParty = (instance: string, alias: string) =>
  apiFetchVoid(
    `/api/parties/${encodeURIComponent(alias)}?instance=${encodeURIComponent(instance)}`,
    { method: "DELETE" },
  );

// AliasMap is partyID → alias, built from fetchParties for client-side
// labelling of party ids in the matrix / holdings / activity views.
export type AliasMap = Record<string, string>;

export const aliasMapFrom = (parties: PartyRef[]): AliasMap => {
  const m: AliasMap = {};
  for (const p of parties) if (p.party_id) m[p.party_id] = p.alias;
  return m;
};

interface HoldingContractsResponse {
  schema_version: number;
  contracts: HoldingContract[];
}

// fetchInstruments returns ACS-discovered instruments. Falls back to the
// recorded TokenRef list (mapped into InstrumentRef shape) when the
// backend couldn't reach the ledger.
export async function fetchInstruments(
  instance: string,
  role = "app-user",
): Promise<InstrumentRef[]> {
  const r = await apiFetch<InstrumentsResponse>(
    `/api/tokens?instance=${encodeURIComponent(instance)}&role=${encodeURIComponent(role)}`,
  );
  if (r.instruments) return r.instruments;
  return (r.tokens ?? []).map((t) => ({
    admin: t.issuer_party,
    instrument_id: t.instrument_id,
    name: t.name,
    symbol: t.symbol,
    decimals: t.decimals,
    standard: t.symbol === "Amulet" ? "Splice Amulet" : "CIP-0112 v2",
    on_ledger: t.status === "on-ledger",
  }));
}

export const fetchMatrix = (instance: string, role = "app-user") =>
  apiFetch<MatrixResponse>(
    `/api/tokens/matrix?instance=${encodeURIComponent(instance)}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.matrix);

// HolderRow / InstrumentSummary — the instrument-first KPI view.
// Supply + holder/contract counts + per-holder distribution,
// derived from one ACS scan (workspace.go).
export interface HolderRow {
  party: string;
  balance: string;
  contract_count: number;
  pct_of_supply: string;
}

export interface InstrumentSummary {
  instrument_id: string;
  admin: string;
  total_supply: string;
  holder_count: number;
  contract_count: number;
  holders: HolderRow[];
}

interface InstrumentSummaryResponse {
  schema_version: number;
  summary: InstrumentSummary;
}

export const fetchInstrumentSummary = (
  instance: string,
  symbol: string,
  role = "app-user",
) =>
  apiFetch<InstrumentSummaryResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/summary?instance=${encodeURIComponent(
      instance,
    )}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.summary);

// PartyDelta / ActivityEvent — the instrument activity feed
// (Activity tab). Each event is one ledger transaction netted into
// senders/receivers + a kind (mint | burn | transfer), reconstructed
// from HoldingV2 create/archive events (no off-ledger registry).
export interface PartyDelta {
  party: string;
  amount: string;
}

export interface ActivityEvent {
  offset: number;
  update_id: string;
  record_time: string;
  instrument_id: string;
  kind: "mint" | "burn" | "transfer";
  amount: string;
  senders?: PartyDelta[];
  receivers?: PartyDelta[];
}

interface ActivityResponse {
  schema_version: number;
  events: ActivityEvent[];
}

export const fetchActivity = (
  instance: string,
  symbol: string,
  role = "app-user",
  limit = 50,
) =>
  apiFetch<ActivityResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/activity?instance=${encodeURIComponent(
      instance,
    )}&role=${encodeURIComponent(role)}&limit=${limit}`,
  ).then((r) => r.events);

export const fetchHoldingContracts = (
  instance: string,
  symbol: string,
  party: string,
  role = "app-user",
) => {
  const params = new URLSearchParams({
    instance,
    role,
    party,
    expand: "contracts",
  });
  return apiFetch<HoldingContractsResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/holdings?${params}`,
  ).then((r) => r.contracts);
};

export interface TokenCreateInput {
  name: string;
  symbol: string;
  decimals: number;
  initial_supply: string;
  issuer: string;
}

// idempotencyHeader returns a fresh per-submission Idempotency-Key
// header for the value-moving token POSTs. The server dedupes retries
// of the SAME key (idempotency.go), so we mint one key per logical
// submission — a user-initiated retry that re-invokes the API function
// gets a new key (a genuinely new attempt), while a transport-level
// retry of the same fetch would reuse it. crypto.randomUUID is
// available in every browser the UI targets (and in jsdom under test).
function idempotencyHeader(): Record<string, string> {
  return { "Idempotency-Key": crypto.randomUUID() };
}

export const createToken = (
  instance: string,
  body: TokenCreateInput,
  role?: string,
) =>
  apiFetch<TokenRef>(
    `/api/tokens?${tokenQuery(instance, role)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", ...idempotencyHeader() },
      body: JSON.stringify(body),
    },
  );

export const mintToken = (
  instance: string,
  symbol: string,
  to: string,
  amount: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/mint?${tokenQuery(instance, role)}`,
    { to, amount },
    idempotencyHeader(),
  );

export const transferToken = (
  instance: string,
  symbol: string,
  from: string,
  to: string,
  amount: string,
  reason?: string,
  role?: string,
  autoAccept?: boolean,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/transfer?${tokenQuery(instance, role)}`,
    { from, to, amount, reason: reason ?? "", auto_accept: !!autoAccept },
    idempotencyHeader(),
  );

// faucetToken funds a party from a well-known source,
// auto-accepted. Empty source defaults to the role's funded party.
export const faucetToken = (
  instance: string,
  symbol: string,
  to: string,
  amount: string,
  source?: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/faucet?${tokenQuery(instance, role)}`,
    { to, amount, source: source ?? "" },
    idempotencyHeader(),
  );

// TransferPlan — dry-run coin selection. Which Holding
// contracts a transfer would consume, the change, and whether the
// sender can cover it. Read-only; no ledger mutation.
export interface TransferPlan {
  instrument: string;
  from: string;
  amount: string;
  inputs: { contract_id: string; amount: string }[];
  total_input: string;
  change: string;
  sufficient: boolean;
  shortfall?: string;
}

export async function planTransfer(
  instance: string,
  symbol: string,
  from: string,
  amount: string,
  role = "app-user",
): Promise<TransferPlan> {
  const params = new URLSearchParams({ instance, role, plan: "1" });
  const resp = await fetch(
    `/api/tokens/${encodeURIComponent(symbol)}/transfer?${params}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: window.location.origin },
      body: JSON.stringify({ from, to: from, amount }),
    },
  );
  if (!resp.ok) {
    throw new ApiError(resp.status, { code: "PLAN_FAILED", error: "could not compute transfer plan" });
  }
  const body = (await resp.json()) as { plan: TransferPlan };
  return body.plan;
}

export const burnToken = (
  instance: string,
  symbol: string,
  from: string,
  amount: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/${encodeURIComponent(symbol)}/burn?${tokenQuery(instance, role)}`,
    { from, amount },
    idempotencyHeader(),
  );

export const acceptTransfer = (
  instance: string,
  transferInstructionID: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/transfers/${encodeURIComponent(transferInstructionID)}/accept?${tokenQuery(instance, role)}`,
    {},
    idempotencyHeader(),
  );

// apiFetchVoid is a thin POST wrapper for 204-returning handlers. The
// mint/transfer/burn/accept endpoints return 204 on success and an
// ApiError on failure — no body to decode either way.
//
// extraHeaders lets value-moving callers attach an Idempotency-Key so a
// network-blip retry or double-click can't mint/transfer/burn twice —
// the server-side idempotency middleware (internal/ui/handlers/
// idempotency.go) is opt-in on exactly that header.
async function apiFetchVoid(
  path: string,
  body: unknown,
  extraHeaders?: Record<string, string>,
): Promise<void> {
  const resp = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...extraHeaders },
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
