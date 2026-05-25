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
