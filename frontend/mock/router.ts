import type { IncomingMessage, ServerResponse } from "node:http";
import {
  jsonResponse,
  noContent,
  notFound,
  parseJsonBody,
  readRequestBody,
  textResponse,
} from "./http.ts";
import {
  DEFAULT_INSTANCE,
  SCHEMA_VERSION,
  createStore,
  findInstanceSummary,
  instanceNames,
  removeInstance,
  setInstanceStatus,
  upsertInstanceSummary,
  type MockStore,
} from "./store.ts";
import {
  handleContractsStreamSse,
  handleDarWatchSse,
  handleInstanceProgressSse,
  queueCreateProgress,
} from "./sse.ts";

export type MockRouter = ReturnType<typeof createMockRouter>;

export function createMockRouter(fixtureDir?: string) {
  const store = createStore(fixtureDir);
  return {
    store,
    handle(req: IncomingMessage, res: ServerResponse, rawUrl: string): boolean {
      const url = new URL(rawUrl, "http://127.0.0.1");
      const path = url.pathname;
      const method = (req.method ?? "GET").toUpperCase();

      if (path === "/healthz" && method === "GET") {
        textResponse(res, 200, "ok");
        return true;
      }

      if (path === "/api/version" && method === "GET") {
        jsonResponse(res, 200, store.version);
        return true;
      }

      if (path === "/events" && method === "GET") {
        // Global hub — minimal keepalive-only stream.
        res.writeHead(200, {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache",
        });
        const timer = setInterval(() => res.write(": keepalive\n\n"), 30000);
        res.on("close", () => clearInterval(timer));
        return true;
      }

      if (path === "/api/instances" && method === "GET") {
        jsonResponse(res, 200, store.instances);
        return true;
      }

      if (path === "/api/instances" && method === "POST") {
        void handleCreateInstance(req, res, store);
        return true;
      }

      if (path === "/api/instances/restore" && method === "POST") {
        void handleRestore(req, res, store);
        return true;
      }

      const instMatch = path.match(/^\/api\/instances\/([^/]+)(\/.*)?$/);
      if (instMatch) {
        const name = decodeURIComponent(instMatch[1]);
        const rest = instMatch[2] ?? "";
        if (handleInstanceRoute(req, res, store, name, rest, method, url)) return true;
      }

      if (path === "/api/doctor" && method === "GET") {
        jsonResponse(res, 200, store.doctor);
        return true;
      }

      if (path === "/api/preflight" && method === "GET") {
        jsonResponse(res, 200, store.preflight);
        return true;
      }

      if (path === "/api/splice/versions" && method === "GET") {
        jsonResponse(res, 200, store.spliceVersions);
        return true;
      }

      if (path === "/api/skills" && method === "GET") {
        jsonResponse(res, 200, store.skills);
        return true;
      }

      if (path === "/api/skills/install" && method === "POST") {
        jsonResponse(res, 200, {
          schema_version: SCHEMA_VERSION,
          installed: ["improve-e2e-tests"],
        });
        return true;
      }

      if (path === "/api/dar/watch/publish" && method === "POST") {
        noContent(res);
        return true;
      }

      if (path === "/api/dar/watch/events" && method === "GET") {
        const instance = url.searchParams.get("instance") ?? DEFAULT_INSTANCE;
        const dar = url.searchParams.get("dar") ?? "token-dar";
        handleDarWatchSse(res, instance, dar);
        return true;
      }

      if (path.startsWith("/api/tokens")) {
        if (handleTokensRoute(req, res, store, path, method, url)) return true;
      }

      if (path.startsWith("/api/parties")) {
        if (handlePartiesRoute(req, res, store, path, method, url)) return true;
      }

      notFound(res);
      return true;
    },
  };
}

async function handleCreateInstance(
  req: IncomingMessage,
  res: ServerResponse,
  store: MockStore,
): Promise<void> {
  const body = parseJsonBody<{ name?: string; version?: string }>(await readRequestBody(req));
  const name = body?.name ?? `mock-${Date.now()}`;
  const version = body?.version ?? "0.6.4";
  upsertInstanceSummary(store, {
    name,
    status: "creating",
    splice_version: version,
    ports: "",
    started_ago: "just now",
  });
  queueCreateProgress(store, name);
  jsonResponse(res, 202, {
    schema_version: SCHEMA_VERSION,
    instance: name,
    events_url: `/api/instances/${encodeURIComponent(name)}/events`,
  });
}

async function handleRestore(
  req: IncomingMessage,
  res: ServerResponse,
  store: MockStore,
): Promise<void> {
  await readRequestBody(req);
  const name = `restored-${Date.now()}`;
  upsertInstanceSummary(store, {
    name,
    status: "running",
    splice_version: "0.6.4",
    ports: "",
    started_ago: "just now",
  });
  jsonResponse(res, 200, { name, restored: true });
}

function handleInstanceRoute(
  req: IncomingMessage,
  res: ServerResponse,
  store: MockStore,
  name: string,
  rest: string,
  method: string,
  url: URL,
): boolean {
  if (rest === "" && method === "GET") {
    if (!findInstanceSummary(store, name)) {
      notFound(res);
      return true;
    }
    const detail = structuredClone(store.instanceDetail);
    detail.name = name;
    jsonResponse(res, 200, detail);
    return true;
  }

  if (rest === "" && method === "DELETE") {
    removeInstance(store, name);
    noContent(res);
    return true;
  }

  if (rest === "/events" && method === "GET") {
    handleInstanceProgressSse(res, store, name);
    return true;
  }

  if (rest === "/up" && method === "DELETE") {
    noContent(res);
    return true;
  }

  if (rest === "/up" && method === "POST") {
    setInstanceStatus(store, name, "running");
    queueCreateProgress(store, name);
    jsonResponse(res, 202, {
      schema_version: SCHEMA_VERSION,
      instance: name,
      events_url: `/api/instances/${encodeURIComponent(name)}/events`,
    });
    return true;
  }

  if (rest === "/recreate" && method === "POST") {
    setInstanceStatus(store, name, "creating");
    queueCreateProgress(store, name);
    jsonResponse(res, 202, {
      schema_version: SCHEMA_VERSION,
      instance: name,
      events_url: `/api/instances/${encodeURIComponent(name)}/events`,
    });
    return true;
  }

  if (rest === "/start" && method === "POST") {
    setInstanceStatus(store, name, "running");
    noContent(res);
    return true;
  }

  if (
    (rest === "/stop" || rest === "/down" || rest === "/pause") &&
    method === "POST"
  ) {
    setInstanceStatus(store, name, rest === "/pause" ? "paused" : "stopped");
    noContent(res);
    return true;
  }

  if (rest === "/resume" && method === "POST") {
    setInstanceStatus(store, name, "running");
    noContent(res);
    return true;
  }

  if (rest === "/observability" && method === "POST") {
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      instance: name,
      prometheus: true,
      grafana: true,
      enabled: true,
      prometheus_ui: "http://127.0.0.1:9090",
      grafana_ui: "http://127.0.0.1:3000",
    });
    return true;
  }

  if (rest === "/containers" && method === "GET") {
    const body = structuredClone(store.containers);
    body.instance = name;
    jsonResponse(res, 200, body);
    return true;
  }

  const logsMatch = rest.match(/^\/containers\/([^/]+)\/logs$/);
  if (logsMatch && method === "GET") {
    const container = decodeURIComponent(logsMatch[1]);
    textResponse(
      res,
      200,
      `[mock] logs for ${container}\n2026-05-30T10:00:00Z INFO  participant started\n`,
    );
    return true;
  }

  const restartMatch = rest.match(/^\/containers\/([^/]+)\/restart$/);
  if (restartMatch && method === "POST") {
    noContent(res);
    return true;
  }

  if (rest === "/jwt" && method === "POST") {
    const includeJwt = url.searchParams.get("include_jwt") === "true";
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      token: includeJwt ? "mock.jwt.token" : "<redacted>",
      redacted: !includeJwt,
      party: "alice::abc",
      audience: "https://canton.network.global",
      role: "app-user",
      warning_dev_secret: "LocalNet HS256 secret — dev only",
      expires_in_seconds: 3600,
    });
    return true;
  }

  if (rest === "/app-config" && method === "GET") {
    const format = url.searchParams.get("format") ?? "json";
    if (format === "env") {
      textResponse(res, 200, "CANTON_PARTICIPANT_URL=http://127.0.0.1:60475\n");
      return true;
    }
    if (format === "yaml") {
      textResponse(res, 200, "participant_url: http://127.0.0.1:60475\n");
      return true;
    }
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      instance: name,
      env: { CANTON_PARTICIPANT_URL: "http://127.0.0.1:60475" },
    });
    return true;
  }

  if (rest === "/contracts" && method === "GET") {
    const body = structuredClone(store.contracts);
    body.instance = name;
    jsonResponse(res, 200, body);
    return true;
  }

  if (rest === "/contracts/stream" && method === "GET") {
    handleContractsStreamSse(res);
    return true;
  }

  const contractDetailMatch = rest.match(/^\/contracts\/([^/]+)$/);
  if (contractDetailMatch && method === "GET") {
    const detail = structuredClone(store.contractDetail);
    if (detail.contract && typeof detail.contract === "object") {
      (detail.contract as Record<string, unknown>).contract_id = decodeURIComponent(
        contractDetailMatch[1],
      );
    }
    jsonResponse(res, 200, detail);
    return true;
  }

  if (rest === "/transactions" && method === "GET") {
    const body = structuredClone(store.transactions);
    body.instance = name;
    jsonResponse(res, 200, body);
    return true;
  }

  const txReplayMatch = rest.match(/^\/transactions\/([^/]+)\/replay$/);
  if (txReplayMatch && method === "GET") {
    const replay = structuredClone(store.txReplay);
    replay.update_id = decodeURIComponent(txReplayMatch[1]);
    jsonResponse(res, 200, replay);
    return true;
  }

  if (rest === "/dar" && method === "GET") {
    const body = structuredClone(store.dar);
    body.instance = name;
    jsonResponse(res, 200, body);
    return true;
  }

  if (rest === "/dar" && method === "POST") {
    void readRequestBody(req).then(() => {
      jsonResponse(res, 200, {
        schema_version: SCHEMA_VERSION,
        uploaded: [{ id: "new-dar", name: "uploaded", version: "1.0.0" }],
      });
    });
    return true;
  }

  const darInspectMatch = rest.match(/^\/dar\/([^/]+)\/inspect$/);
  if (darInspectMatch && method === "GET") {
    const body = structuredClone(store.darInspect);
    body.dar_id = decodeURIComponent(darInspectMatch[1]);
    jsonResponse(res, 200, body);
    return true;
  }

  if (rest === "/dar/diff" && method === "GET") {
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      added: [],
      removed: [],
      changed: [],
    });
    return true;
  }

  const darVettingGetMatch = rest.match(/^\/dar\/([^/]+)\/vetting$/);
  if (darVettingGetMatch && method === "GET") {
    const body = structuredClone(store.darVetting);
    body.dar_id = decodeURIComponent(darVettingGetMatch[1]);
    jsonResponse(res, 200, body);
    return true;
  }

  const darVettingPostMatch = rest.match(/^\/dar\/([^/]+)\/vetting\/([^/]+)$/);
  if (darVettingPostMatch && method === "POST") {
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      dar_id: decodeURIComponent(darVettingPostMatch[1]),
      role: decodeURIComponent(darVettingPostMatch[2]),
      vetted: true,
    });
    return true;
  }

  if (rest === "/metrics/summary" && method === "GET") {
    const body = structuredClone(store.metricsSummary);
    body.instance = name;
    jsonResponse(res, 200, body);
    return true;
  }

  if (rest === "/metrics/range" && method === "GET") {
    jsonResponse(res, 200, store.metricsRange);
    return true;
  }

  if (rest === "/metrics" && method === "GET") {
    jsonResponse(res, 200, { status: "success", data: { result: [] } });
    return true;
  }

  const snapshotMatch = rest === "/snapshot" && method === "POST";
  if (snapshotMatch) {
    void readRequestBody(req).then(() => {
      res.writeHead(200, {
        "Content-Type": "application/gzip",
        "Content-Disposition": `attachment; filename="${name}-snapshot.tar.gz"`,
      });
      res.end(Buffer.from("mock-snapshot"));
    });
    return true;
  }

  return false;
}

function handleTokensRoute(
  _req: IncomingMessage,
  res: ServerResponse,
  store: MockStore,
  path: string,
  method: string,
  url: URL,
): boolean {
  if (path === "/api/tokens" && method === "GET") {
    jsonResponse(res, 200, store.tokens);
    return true;
  }

  if (path === "/api/tokens/matrix" && method === "GET") {
    jsonResponse(res, 200, store.tokensMatrix);
    return true;
  }

  if (path === "/api/tokens" && method === "POST") {
    jsonResponse(res, 201, {
      schema_version: SCHEMA_VERSION,
      symbol: "NEW",
      name: "New Token",
      instrument_id: "NEW",
    });
    return true;
  }

  if (path === "/api/tokens/demo" && method === "POST") {
    jsonResponse(res, 201, {
      schema_version: SCHEMA_VERSION,
      symbol: "DEMO",
      instrument_id: "DEMO",
      minted: "1000",
    });
    return true;
  }

  const symbolMatch = path.match(/^\/api\/tokens\/([^/]+)(\/.*)?$/);
  if (!symbolMatch) return false;
  const symbol = decodeURIComponent(symbolMatch[1]);
  const sub = symbolMatch[2] ?? "";

  if (sub === "" && method === "GET") {
    jsonResponse(res, 200, {
      schema_version: SCHEMA_VERSION,
      symbol,
      instrument_id: symbol,
      admin: "alice::abc",
    });
    return true;
  }

  if (sub === "/summary" && method === "GET") {
    jsonResponse(res, 200, store.tokenSummary);
    return true;
  }

  if (sub === "/activity" && method === "GET") {
    jsonResponse(res, 200, store.tokenActivity);
    return true;
  }

  if (sub === "/holdings" && method === "GET") {
    jsonResponse(res, 200, store.tokenHoldings);
    return true;
  }

  if (sub === "/transfer" && method === "POST") {
    if (url.searchParams.get("plan") === "1") {
      jsonResponse(res, 200, {
        schema_version: SCHEMA_VERSION,
        plan: {
          instrument: symbol,
          from: "bob",
          amount: "100",
          inputs: [{ contract_id: "00abc123", amount: "100.0" }],
          total_input: "100.0",
          change: "0.0",
          sufficient: true,
        },
      });
      return true;
    }
    jsonResponse(res, 201, { schema_version: SCHEMA_VERSION, transfer_id: "tx-mock-1" });
    return true;
  }

  if (
    (sub === "/mint" || sub === "/burn" || sub === "/faucet") &&
    method === "POST"
  ) {
    jsonResponse(res, sub === "/mint" ? 201 : 200, {
      schema_version: SCHEMA_VERSION,
      symbol,
      amount: "100",
    });
    return true;
  }

  const acceptMatch = path.match(/^\/api\/tokens\/transfers\/([^/]+)\/accept$/);
  if (acceptMatch && method === "POST") {
    jsonResponse(res, 200, { schema_version: SCHEMA_VERSION, accepted: true });
    return true;
  }

  return false;
}

function handlePartiesRoute(
  req: IncomingMessage,
  res: ServerResponse,
  store: MockStore,
  path: string,
  method: string,
  _url: URL,
): boolean {
  if (path === "/api/parties" && method === "GET") {
    jsonResponse(res, 200, store.parties);
    return true;
  }

  if (path === "/api/parties" && method === "POST") {
    void readRequestBody(req).then((raw) => {
      const body = parseJsonBody<{ alias?: string }>(raw);
      const party = {
        alias: body?.alias ?? "new-party",
        party_id: `${body?.alias ?? "new"}::mock`,
        role: "app-user",
        is_local: true,
        created_at: new Date().toISOString(),
      };
      const list = (store.parties.parties as unknown[]) ?? [];
      list.push(party);
      store.parties.parties = list;
      jsonResponse(res, 201, { schema_version: SCHEMA_VERSION, ...party });
    });
    return true;
  }

  const deleteMatch = path.match(/^\/api\/parties\/([^/]+)$/);
  if (deleteMatch && method === "DELETE") {
    const alias = decodeURIComponent(deleteMatch[1]);
    store.parties.parties = ((store.parties.parties as { alias: string }[]) ?? []).filter(
      (p) => p.alias !== alias,
    );
    noContent(res);
    return true;
  }

  return false;
}

export { instanceNames };
