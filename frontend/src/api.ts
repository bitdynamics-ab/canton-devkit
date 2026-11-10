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

export interface VersionResponse {
  name: string;
  schema_version: number;
}

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

export interface Endpoint {
  key: string;
  label: string;
  url: string;
  port?: number;
  scheme?: string;
  reachability?: "ok" | "unreachable";
  reachability_detail?: string;
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
  endpoints?: Endpoint[];
}

export const fetchVersion = () => apiFetch<VersionResponse>("/api/version");

export const fetchInstances = () => apiFetch<ListResponse>("/api/instances");

export const fetchInstance = (name: string) =>
  apiFetch<Instance>(`/api/instances/${encodeURIComponent(name)}`);

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
  cpu_percent?: number;
  mem_used_bytes?: number;
  mem_limit_bytes?: number;
}

export const fetchContainers = (name: string) =>
  apiFetch<ContainersResponse>(`/api/instances/${encodeURIComponent(name)}/containers`);

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
    }
    throw new ApiError(resp.status, body);
  }
}

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
    }
    throw new ApiError(resp.status, body);
  }
  return resp.text();
}

export interface JwtRequest {
  role?: string;
  ttl_seconds?: number;
  audience?: string;
}

export interface JwtResponse {
  schema_version: number;
  token: string;
  party: string;
  audience: string;
  role: string;
  warning_dev_secret: string;
  expires_in_seconds?: number;
}

export function issueJwt(
  name: string,
  req: JwtRequest,
): Promise<JwtResponse> {
  return apiFetch<JwtResponse>(
    `/api/instances/${encodeURIComponent(name)}/jwt`,
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}

export type AppConfigFormat = "env" | "json" | "yaml";

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

export interface AppConfigPayload {
  schema_version: number;
  instance: string;
  vars: Record<string, string>;
}

export const fetchAppConfigJSON = (name: string) =>
  apiFetch<AppConfigPayload>(
    `/api/instances/${encodeURIComponent(name)}/app-config?format=json`,
  );

export interface SpliceVersionEntry {
  tag: string;
  status: "latest" | "supported" | "available" | "drifted" | "catalogued-only";
  major: string;
  commit: string;
  note?: string;
  v2_capable?: boolean;
}

export interface SpliceVersionsResponse {
  schema_version: number;
  latest_alias: string;
  versions: SpliceVersionEntry[];
}

export const fetchSpliceVersions = () =>
  apiFetch<SpliceVersionsResponse>("/api/splice/versions");

export interface CreateInstanceRequest {
  name: string;
  version?: string;
  allow_uncurated?: boolean;
  profiles?: string[];
  port_base?: number;
  observability_mode?: string;
}

export interface CreateInstanceAcceptedResponse {
  schema_version: number;
  instance: string;
  events_url: string;
}

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
    try { body = JSON.parse(text); } catch {}
    throw new ApiError(resp.status, body);
  }
  return JSON.parse(text) as CreateInstanceAcceptedResponse;
}

export interface PreflightReport {
  schema_version: number;
  ok: boolean;
  sections: PreflightSection[];
  summary?: string;
  error_code?: ErrorCode;
}

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

export class PreflightFailedError extends Error {
  report: PreflightReport;
  constructor(report: PreflightReport) {
    super(report.summary || "system requirements not met");
    this.report = report;
  }
}

export const fetchPreflight = (version: string) =>
  apiFetch<PreflightReport>(
    `/api/preflight?version=${encodeURIComponent(version)}`,
  );

export const fetchDoctor = (version?: string) =>
  apiFetch<PreflightReport>(
    version
      ? `/api/doctor?version=${encodeURIComponent(version)}`
      : "/api/doctor",
  );

// Error detection relies on an asymmetry: on success the server sends
// Content-Disposition: attachment, the body goes to the download
// manager, and the iframe never navigates (no `load` event). On
// failure the server sends an inline JSON error document, the iframe
// navigates to it, and `load` fires — we can't read the cross-document
// body, so we reject with a generic ApiError. Success is detected by
// absence: a settle timeout resolves once no `load` arrived.
const DOWNLOAD_SETTLE_MS = 1200;

export function downloadSnapshot(name: string): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const form = document.createElement("form");
    form.method = "POST";
    form.action = `/api/instances/${encodeURIComponent(name)}/snapshot`;
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
        } catch {
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
    }
    throw new ApiError(resp.status, body);
  }
  return (await resp.json()) as ResumeAcceptedResponse;
}

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
    }
    throw new ApiError(resp.status, body);
  }
  return (await resp.json()) as ResumeAcceptedResponse;
}

export interface MetricsSummary {
  schema_version: number;
  instance: string;
  scope?: string;
  metrics: {
    ledger_tps_5m?: number;
    mediator_p50_seconds?: number;
    mediator_p95_seconds?: number;
    mediator_p99_seconds?: number;
    jvm_heap_used_bytes?: number;
    postgres_conn_count?: number;
  };
  latency?: {
    p50_ms?: number | null;
    p95_ms?: number | null;
    p99_ms?: number | null;
  };
  dashboards?: {
    grafana_url?: string;
  };
}

export type Role = "app-user" | "app-provider" | "sv";

export interface DARRow {
  main: string;
  name: string;
  version: string;
  description?: string;
  vetted?: boolean;
}

export interface DARListResponse {
  schema_version: number;
  instance: string;
  role: Role;
  dars: DARRow[];
}

export const fetchDARList = (name: string, role: Role = "app-provider") =>
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
        } catch {
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
      }
      reject(new ApiError(xhr.status, body));
    });
    xhr.addEventListener("error", () =>
      reject(new ApiError(0, { code: "NETWORK", error: "network error" })),
    );
    xhr.send(fd);
  });
}

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
  role: Role = "app-provider",
) =>
  apiFetch<DARInspectResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/${encodeURIComponent(mainID)}/inspect?role=${role}`,
  );

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
  role: Role = "app-provider",
) =>
  apiFetch<DARDiffResponse>(
    `/api/instances/${encodeURIComponent(instance)}/dar/diff?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}&role=${role}`,
  );

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
    }
  });
  return es;
}

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
  role: Role = "app-provider",
  limit = 100,
) =>
  apiFetch<ContractsListResponse>(
    `/api/instances/${encodeURIComponent(name)}/contracts?role=${role}&limit=${limit}`,
  );

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
  role: Role = "app-provider",
) =>
  apiFetch<ContractDetailResponse>(
    `/api/instances/${encodeURIComponent(name)}/contracts/${encodeURIComponent(contractId)}?role=${role}`,
  );

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

export function openContractsStream(
  name: string,
  role: Role = "app-provider",
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
  scanned_from?: number;
  window_truncated?: boolean;
}

export interface TransactionFilters {
  parties?: string[];
  templates?: string[];
  from?: number;
  to?: number;
}

export const fetchTransactions = (
  name: string,
  role: Role = "app-provider",
  limit = 100,
  filters: TransactionFilters = {},
) => {
  const params = new URLSearchParams({ role, limit: String(limit) });
  if (filters.parties && filters.parties.length > 0) {
    params.set("party", filters.parties.join(","));
  }
  if (filters.templates && filters.templates.length > 0) {
    params.set("template", filters.templates.join(","));
  }
  if (typeof filters.from === "number" && Number.isFinite(filters.from)) {
    params.set("from", String(filters.from));
  }
  if (typeof filters.to === "number" && Number.isFinite(filters.to)) {
    params.set("to", String(filters.to));
  }
  return apiFetch<TransactionsListResponse>(
    `/api/instances/${encodeURIComponent(name)}/transactions?${params.toString()}`,
  );
};

export interface TxReplayEvent {
  kind: "created" | "exercised" | "archived";
  node_id: number;
  contract_id: string;
  template_id?: string;
  choice?: string;
  acting_parties?: string[];
  consuming?: boolean;
  signatories?: string[];
  observers?: string[];
}

export interface TxReplayResponse {
  schema_version: number;
  instance: string;
  parties?: string[];
  update_id: string;
  offset: number;
  workflow_id?: string;
  effective_at?: string;
  event_count: number;
  events: TxReplayEvent[];
}

export const fetchTxReplay = (
  name: string,
  updateId: string,
  role: Role = "app-provider",
  parties?: string[],
) => {
  const params = new URLSearchParams({ role });
  if (parties && parties.length > 0) params.set("party", parties.join(","));
  return apiFetch<TxReplayResponse>(
    `/api/instances/${encodeURIComponent(name)}/transactions/${encodeURIComponent(
      updateId,
    )}/replay?${params.toString()}`,
  );
};

export const fetchMetricsSummary = (name: string, signal?: AbortSignal) =>
  apiFetch<MetricsSummary>(
    `/api/instances/${encodeURIComponent(name)}/metrics/summary`,
    { signal },
  );

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

export async function downInstance(name: string): Promise<void> {
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
    }
    throw new ApiError(resp.status, body);
  }
}

export async function stopInstance(name: string): Promise<void> {
  await postInstanceAction(name, "stop");
}

export async function startInstance(
  name: string,
): Promise<ResumeAcceptedResponse | null> {
  const resp = await fetch(
    `/api/instances/${encodeURIComponent(name)}/start`,
    { method: "POST" },
  );
  if (!resp.ok) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
    }
    throw new ApiError(resp.status, body);
  }
  if (resp.status === 202) {
    return (await resp.json()) as ResumeAcceptedResponse;
  }
  return null;
}

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
    }
    throw new ApiError(resp.status, body);
  }
}

export async function removeInstance(name: string, force = false): Promise<void> {
  const qs = force ? "?force=true" : "";
  const resp = await fetch(`/api/instances/${encodeURIComponent(name)}${qs}`, {
    method: "DELETE",
  });
  if (!resp.ok && resp.status !== 404) {
    const text = await resp.text();
    let body: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      body = JSON.parse(text);
    } catch {
    }
    throw new ApiError(resp.status, body);
  }
}

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
    }
    throw new ApiError(resp.status, body);
  }
}

export type CreateProgressEvent =
  | StepStartedEvent
  | StepProgressEvent
  | StepFinishedEvent
  | StepFailedEvent
  | WarnEvent
  | DoneEvent
  | OutputEvent
  | CancelledEvent;

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
  skipped: string[];
}

export const fetchSkills = () => apiFetch<SkillsListResponse>("/api/skills");

export const installSkills = (target: "claude" | "codex", force = false) =>
  apiFetch<SkillsInstallResponse>("/api/skills/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ target, force }),
  });

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

export type HoldingSource = "ledger" | "registry";

export interface TokenHolding {
  instrument_symbol?: string;
  instrument_id: string;
  party: string;
  amount: string;
  source: HoldingSource;
}

export interface TokensListResponse {
  schema_version: number;
  endpoint: string;
  role: string;
  tokens: TokenRef[];
}

export interface TokenHoldingsResponse {
  schema_version: number;
  endpoint: string;
  role: string;
  source: HoldingSource;
  holdings: TokenHolding[];
  truncated?: boolean;
}

export type TokenActivitySource = "event_log" | "transaction";

export interface TokenTransferLeg {
  transfer_leg_id: string;
  side: string;
  otherside: string;
  amount: string;
  instrument_id: string;
}

export interface TokenActivityEvent {
  source: TokenActivitySource;
  update_id: string;
  offset: number;
  record_time: string;
  instrument_id: string;
  account: string;
  admin: string;
  consumed_holding_count: number;
  created_holding_count: number;
  transfer_legs: TokenTransferLeg[];
  reason?: string;
}

export type AllocationStatus =
  | "pending"
  | "ready"
  | "settled"
  | "cancelled"
  | "withdrawn";

export interface Allocation {
  contract_id: string;
  status: AllocationStatus;
  settlement_id: string;
  admin: string;
  authorizer: string;
  executors: string[];
  committed: boolean;
  settlement_deadline?: string;
  transfer_legs: TokenTransferLeg[];
  created_at?: string;
}

export interface AllocationSummary {
  contract_id: string;
  status: AllocationStatus;
  settlement_id: string;
  authorizer: string;
  leg_count: number;
  committed: boolean;
}

export interface TransferSummary {
  contract_id: string;
  sender: string;
  receiver: string;
  instrument_id: string;
  amount: string;
  generation: string;
  requested_at?: string;
  execute_before?: string;
}

export interface BatchActionResult {
  kind: string;
  ok: boolean;
  detail?: string;
}

export interface BatchResult {
  update_id: string;
  actions: BatchActionResult[];
  ok: boolean;
}

const DEFAULT_ROLE = "app-provider";

function tokenQuery(instance: string, role?: string): string {
  const params = new URLSearchParams({ instance });
  if (role && role !== DEFAULT_ROLE) params.set("role", role);
  return params.toString();
}

export const fetchTokens = (instance: string, role?: string) =>
  apiFetch<TokensListResponse>(`/api/tokens?${tokenQuery(instance, role)}`);

export interface TokenIdentity {
  schema_version: number;
  instance: string;
  available_roles: Role[];
  current_role: Role;
}

export const fetchTokenIdentity = (instance: string, role?: string) =>
  apiFetch<TokenIdentity>(`/api/tokens/identity?${tokenQuery(instance, role)}`);

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

export interface InstrumentRef {
  admin: string;
  instrument_id: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  standard?: string;
  generation?: string;
  on_ledger: boolean;
}

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
  tokens?: TokenRef[];
}

interface MatrixResponse {
  schema_version: number;
  matrix: BalanceMatrix;
}

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

export const fetchParties = (instance: string, role = "app-provider") =>
  apiFetch<PartiesResponse>(
    `/api/parties?instance=${encodeURIComponent(instance)}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.parties);

export const createParty = (instance: string, alias: string, role = "app-provider") =>
  apiFetch<PartyRef>(`/api/parties?instance=${encodeURIComponent(instance)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ alias, role }),
  });

export const removeParty = (instance: string, alias: string) =>
  apiFetchVoid(
    `/api/parties/${encodeURIComponent(alias)}?instance=${encodeURIComponent(instance)}`,
    { method: "DELETE" },
  );

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

export async function fetchInstruments(
  instance: string,
  role = "app-provider",
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
    standard:
      t.symbol === "Amulet" ? "Splice Amulet" : "Token Standard V2 (CIP-0112)",
    generation: t.symbol === "Amulet" ? "v1" : "v2",
    on_ledger: t.status === "on-ledger",
  }));
}

export const fetchMatrix = (instance: string, role = "app-provider") =>
  apiFetch<MatrixResponse>(
    `/api/tokens/matrix?instance=${encodeURIComponent(instance)}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.matrix);

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
  declared_supply?: string;
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
  role = "app-provider",
) =>
  apiFetch<InstrumentSummaryResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/summary?instance=${encodeURIComponent(
      instance,
    )}&role=${encodeURIComponent(role)}`,
  ).then((r) => r.summary);

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
  source: TokenActivitySource;
  amount: string;
  senders?: PartyDelta[];
  receivers?: PartyDelta[];
}

interface ActivityResponse {
  schema_version: number;
  events: ActivityEvent[];
  truncated?: boolean;
}

export interface ActivityPage {
  events: ActivityEvent[];
  truncated: boolean;
}

export const fetchActivity = (
  instance: string,
  symbol: string,
  role = "app-provider",
  limit = 50,
): Promise<ActivityPage> =>
  apiFetch<ActivityResponse>(
    `/api/tokens/${encodeURIComponent(symbol)}/activity?instance=${encodeURIComponent(
      instance,
    )}&role=${encodeURIComponent(role)}&limit=${limit}`,
  ).then((r) => ({ events: r.events, truncated: !!r.truncated }));

export const fetchHoldingContracts = (
  instance: string,
  symbol: string,
  party: string,
  role = "app-provider",
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

export interface DemoResult {
  token: TokenRef;
  issuer: PartyRef;
  holder?: PartyRef;
  seeded: boolean;
}

export const launchDemoToken = (
  instance: string,
  opts?: { symbol?: string; initial_supply?: string; decimals?: number },
  role?: string,
): Promise<DemoResult> =>
  apiFetch<DemoResult>(`/api/tokens/demo?${tokenQuery(instance, role)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...idempotencyHeader() },
    body: JSON.stringify(opts ?? {}),
  });

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

export const transferToken = async (
  instance: string,
  symbol: string,
  from: string,
  to: string,
  amount: string,
  reason?: string,
  role?: string,
  autoAccept?: boolean,
  atomic?: boolean,
): Promise<{ transferInstructionId: string; settled: boolean }> => {
  const resp = await fetch(
    `/api/tokens/${encodeURIComponent(symbol)}/transfer?${tokenQuery(instance, role)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", ...idempotencyHeader() },
      body: JSON.stringify({
        from,
        to,
        amount,
        reason: reason ?? "",
        auto_accept: !!autoAccept,
        atomic: !!atomic,
      }),
    },
  );
  if (!resp.ok) {
    let parsed: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      parsed = await resp.json();
    } catch {
    }
    throw new ApiError(resp.status, parsed);
  }
  const body = (await resp.json().catch(() => ({}))) as {
    transfer_instruction_id?: string;
    settled?: boolean;
  };
  return { transferInstructionId: body.transfer_instruction_id ?? "", settled: !!body.settled };
};

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
  role = "app-provider",
): Promise<TransferPlan> {
  const params = new URLSearchParams({ instance, role, plan: "1" });
  const body = await apiFetch<{ plan: TransferPlan }>(
    `/api/tokens/${encodeURIComponent(symbol)}/transfer?${params}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: window.location.origin },
      body: JSON.stringify({ from, to: from, amount }),
    },
  );
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
  party?: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    `/api/tokens/transfers/${encodeURIComponent(transferInstructionID)}/accept?${tokenQuery(instance, role)}${
      party ? `&party=${encodeURIComponent(party)}` : ""
    }`,
    {},
    idempotencyHeader(),
  );

interface AllocationsListResponse {
  schema_version: number;
  endpoint: string;
  role: string;
  allocations: AllocationSummary[];
  aliases: AliasMap;
}

export async function fetchAllocations(
  instance: string,
  party?: string,
  role?: string,
): Promise<{ allocations: AllocationSummary[]; aliases: AliasMap }> {
  const params = new URLSearchParams({ instance });
  if (party) params.set("party", party);
  if (role && role !== DEFAULT_ROLE) params.set("role", role);
  const r = await apiFetch<AllocationsListResponse>(
    `/api/tokens/allocations?${params}`,
  );
  return { allocations: r.allocations ?? [], aliases: r.aliases ?? {} };
}

interface PendingTransfersListResponse {
  schema_version: number;
  endpoint: string;
  role: string;
  pending_transfers: TransferSummary[];
  truncated?: boolean;
  aliases: AliasMap;
}

export async function fetchPendingTransfers(
  instance: string,
  party?: string,
  role?: string,
): Promise<{ transfers: TransferSummary[]; truncated: boolean; aliases: AliasMap }> {
  const params = new URLSearchParams({ instance });
  if (party) params.set("party", party);
  if (role && role !== DEFAULT_ROLE) params.set("role", role);
  const r = await apiFetch<PendingTransfersListResponse>(
    `/api/tokens/transfers?${params}`,
  );
  return {
    transfers: r.pending_transfers ?? [],
    truncated: r.truncated ?? false,
    aliases: r.aliases ?? {},
  };
}

export interface AllocateInput {
  from: string;
  to: string;
  amount: string;
  executor?: string;
  settlement_deadline?: string;
  committed?: boolean;
}

export async function allocateToken(
  instance: string,
  symbol: string,
  body: AllocateInput,
  role?: string,
): Promise<{ allocationId: string }> {
  const resp = await fetch(
    `/api/tokens/${encodeURIComponent(symbol)}/allocate?${tokenQuery(instance, role)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", ...idempotencyHeader() },
      body: JSON.stringify(body),
    },
  );
  if (!resp.ok) {
    let parsed: ApiErrorBody = { code: "UNKNOWN", error: resp.statusText };
    try {
      parsed = await resp.json();
    } catch {
    }
    throw new ApiError(resp.status, parsed);
  }
  const b = (await resp.json().catch(() => ({}))) as { allocation_id?: string };
  return { allocationId: b.allocation_id ?? "" };
}

export const withdrawAllocation = (
  instance: string,
  allocationID: string,
  party?: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    allocationActionURL(instance, allocationID, "withdraw", party, role),
    {},
    idempotencyHeader(),
  );

export const cancelAllocation = (
  instance: string,
  allocationID: string,
  party?: string,
  role?: string,
): Promise<void> =>
  apiFetchVoid(
    allocationActionURL(instance, allocationID, "cancel", party, role),
    {},
    idempotencyHeader(),
  );

function allocationActionURL(
  instance: string,
  allocationID: string,
  action: "withdraw" | "cancel",
  party?: string,
  role?: string,
): string {
  return `/api/tokens/allocations/${encodeURIComponent(allocationID)}/${action}?${tokenQuery(
    instance,
    role,
  )}${party ? `&party=${encodeURIComponent(party)}` : ""}`;
}

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
  }
  throw new ApiError(resp.status, parsed);
}
