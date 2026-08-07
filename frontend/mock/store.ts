import { readFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const SCHEMA_VERSION = 1;
export const DEFAULT_INSTANCE = "demo";

const mockDir = dirname(fileURLToPath(import.meta.url));
export const FIXTURES_DIR = join(mockDir, "fixtures");

export type JsonRecord = Record<string, unknown>;

function loadFixture<T extends JsonRecord>(name: string, fallback: T): T {
  const path = join(FIXTURES_DIR, name);
  if (!existsSync(path)) return structuredClone(fallback);
  return JSON.parse(readFileSync(path, "utf8")) as T;
}

export interface MockStore {
  version: JsonRecord;
  instances: JsonRecord;
  instanceDetail: JsonRecord;
  containers: JsonRecord;
  contracts: JsonRecord;
  contractDetail: JsonRecord;
  transactions: JsonRecord;
  txReplay: JsonRecord;
  dar: JsonRecord;
  darInspect: JsonRecord;
  darVetting: JsonRecord;
  metricsSummary: JsonRecord;
  metricsRange: JsonRecord;
  tokens: JsonRecord;
  tokenIdentity: JsonRecord;
  tokensMatrix: JsonRecord;
  tokenAllocations: JsonRecord;
  tokenTransfers: JsonRecord;
  tokenSummary: JsonRecord;
  tokenActivity: JsonRecord;
  tokenHoldings: JsonRecord;
  parties: JsonRecord;
  doctor: JsonRecord;
  preflight: JsonRecord;
  spliceVersions: JsonRecord;
  skills: JsonRecord;
  /** In-flight create/up progress keyed by instance name. */
  progressQueues: Map<string, JsonRecord[]>;
}

const emptyList = { schema_version: SCHEMA_VERSION, instances: [] as JsonRecord[] };

export function createStore(fixtureDir = FIXTURES_DIR): MockStore {
  const load = <T extends JsonRecord>(name: string, fallback: T) => {
    const path = join(fixtureDir, name);
    if (!existsSync(path)) return structuredClone(fallback);
    return JSON.parse(readFileSync(path, "utf8")) as T;
  };

  return {
    version: load("version.json", {
      name: "canton-devkit (mock)",
      schema_version: SCHEMA_VERSION,
    }),
    instances: load("instances.json", emptyList),
    instanceDetail: load("instance-demo.json", {
      schema_version: SCHEMA_VERSION,
      name: DEFAULT_INSTANCE,
      status: "running",
      splice_version: "0.6.4",
      created_at: new Date().toISOString(),
      compose_project: `canton-${DEFAULT_INSTANCE}`,
      docker_network: DEFAULT_INSTANCE,
      container_prefix: `${DEFAULT_INSTANCE}-`,
      project_dir: "/tmp/mock",
      data_dir: "/tmp/mock/data",
      endpoints: [],
    }),
    containers: load("containers.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      containers: [],
      healthy_count: 0,
      starting_count: 0,
      unhealthy_count: 0,
      restarting_count: 0,
      exited_count: 0,
    }),
    contracts: load("contracts.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      contracts: [],
      count: 0,
    }),
    contractDetail: load("contract-detail.json", {
      schema_version: SCHEMA_VERSION,
      contract: {},
    }),
    transactions: load("transactions.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      transactions: [],
      count: 0,
    }),
    txReplay: load("tx-replay.json", { schema_version: SCHEMA_VERSION, events: [] }),
    dar: load("dar.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      dars: [],
    }),
    darInspect: load("dar-inspect.json", { schema_version: SCHEMA_VERSION, packages: [] }),
    darVetting: load("dar-vetting.json", { schema_version: SCHEMA_VERSION, roles: [] }),
    metricsSummary: load("metrics-summary.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      metrics: {},
    }),
    metricsRange: load("metrics-range.json", { status: "success", data: { result: [] } }),
    tokens: load("tokens.json", { schema_version: SCHEMA_VERSION, instruments: [] }),
    tokenIdentity: load("token-identity.json", {
      schema_version: SCHEMA_VERSION,
      instance: DEFAULT_INSTANCE,
      available_roles: ["app-user", "app-provider", "sv"],
      current_role: "app-user",
    }),
    tokensMatrix: load("tokens-matrix.json", { schema_version: SCHEMA_VERSION, matrix: {} }),
    tokenAllocations: load("token-allocations.json", {
      schema_version: SCHEMA_VERSION,
      allocations: [],
      aliases: {},
    }),
    tokenTransfers: load("token-transfers.json", {
      schema_version: SCHEMA_VERSION,
      pending_transfers: [],
      truncated: false,
      aliases: {},
    }),
    tokenSummary: load("token-summary.json", { schema_version: SCHEMA_VERSION, summary: {} }),
    tokenActivity: load("token-activity.json", { schema_version: SCHEMA_VERSION, events: [] }),
    tokenHoldings: load("token-holdings.json", {
      schema_version: SCHEMA_VERSION,
      source: "ledger",
      holdings: [],
    }),
    parties: load("parties.json", { schema_version: SCHEMA_VERSION, parties: [] }),
    doctor: load("doctor.json", { schema_version: SCHEMA_VERSION, ok: true, sections: [] }),
    preflight: load("preflight.json", { schema_version: SCHEMA_VERSION, ok: true, sections: [] }),
    spliceVersions: load("splice-versions.json", {
      schema_version: SCHEMA_VERSION,
      latest_alias: "0.6.4",
      versions: [],
    }),
    skills: load("skills.json", { schema_version: SCHEMA_VERSION, skills: [] }),
    progressQueues: new Map(),
  };
}

export function instanceNames(store: MockStore): string[] {
  const list = store.instances.instances;
  if (!Array.isArray(list)) return [];
  return list
    .map((i) => (typeof i === "object" && i && "name" in i ? String(i.name) : ""))
    .filter(Boolean);
}

export function findInstanceSummary(store: MockStore, name: string): JsonRecord | undefined {
  const list = store.instances.instances;
  if (!Array.isArray(list)) return undefined;
  return list.find(
    (i) => typeof i === "object" && i && "name" in i && String(i.name) === name,
  ) as JsonRecord | undefined;
}

export function upsertInstanceSummary(store: MockStore, summary: JsonRecord): void {
  const list = (store.instances.instances as JsonRecord[]) ?? [];
  const name = String(summary.name);
  const idx = list.findIndex((i) => String(i.name) === name);
  if (idx >= 0) list[idx] = summary;
  else list.push(summary);
  store.instances.instances = list;
}

export function removeInstance(store: MockStore, name: string): boolean {
  const before = instanceNames(store).length;
  store.instances.instances = (store.instances.instances as JsonRecord[]).filter(
    (i) => String(i.name) !== name,
  );
  store.progressQueues.delete(name);
  return instanceNames(store).length < before;
}

export function setInstanceStatus(store: MockStore, name: string, status: string): void {
  const summary = findInstanceSummary(store, name);
  if (summary) summary.status = status;
  if (String(store.instanceDetail.name) === name) {
    store.instanceDetail.status = status;
  }
}

export { loadFixture };
