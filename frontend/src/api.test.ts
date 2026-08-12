import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  SCHEMA_VERSION,
  acceptTransfer,
  apiFetch,
  burnToken,
  createToken,
  downloadSnapshot,
  faucetToken,
  fetchContractDetail,
  issueJwt,
  mintToken,
  openContractsStream,
  planTransfer,
  setObservability,
  transferToken,
} from "./api";

// apiFetch is the single chokepoint for every backend call.
// Cover the four classes of behaviour that matter:
//
//   1. absolute-path guard — same-origin contract enforced
//      client-side so a typo can't escape to a third-party URL.
//   2. JSON happy path
//   3. error envelope decoding into ApiError (preserves code,
//      detail, remediation per the handler shape)
//   4. POST request bodies trigger Content-Type: application/json
//      via the conditional header — issueJwt is the canonical
//      caller always receives a usable LocalNet JWT.

describe("SCHEMA_VERSION", () => {
  // Pinned constant. The Go test internal/ui/frontend_schema_test.go
  // greps for `SCHEMA_VERSION = N` and asserts equality with
  // types.SchemaVersion. This test pins the JS side independently
  // so a refactor that moves the constant to a function or computed
  // value would break here (locally) before breaking the Go grep.
  it("is a positive integer", () => {
    expect(SCHEMA_VERSION).toBeGreaterThan(0);
    expect(Number.isInteger(SCHEMA_VERSION)).toBe(true);
  });
});

describe("apiFetch", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("rejects non-absolute paths before fetching", async () => {
    // The error must be thrown synchronously enough that no
    // network call happens — verify via a fetch spy that never
    // gets called.
    const spy = vi.fn();
    vi.stubGlobal("fetch", spy);
    await expect(apiFetch("api/version")).rejects.toThrow(/absolute/);
    expect(spy).not.toHaveBeenCalled();
  });

  it("decodes JSON bodies on 2xx", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ name: "canton-devkit", schema_version: 1 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    const got = await apiFetch<{ name: string }>("/api/version");
    expect(got.name).toBe("canton-devkit");
  });

  it("decodes the error envelope into ApiError", async () => {
    // Mirror the handlers/errorBody shape.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: "INSTANCE_NOT_FOUND",
            error: "instance demo not found",
            detail: "the registry has no record of an instance named demo",
            remediation: ["run dpm localnet list to see what's registered"],
          }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    await expect(apiFetch("/api/instances/demo")).rejects.toMatchObject({
      status: 404,
      code: "INSTANCE_NOT_FOUND",
      message: "instance demo not found",
      detail: expect.stringContaining("no record"),
      remediation: expect.arrayContaining([expect.stringContaining("dpm localnet list")]),
    });
  });

  it("falls back to a synthetic envelope when the server returns non-JSON 5xx", async () => {
    // Defensive — proxy errors / nginx HTML pages shouldn't crash
    // the UI. The catch-all envelope keeps the screen renderable.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("", { status: 502, statusText: "Bad Gateway" }),
      ),
    );
    const err = await apiFetch("/api/instances").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(502);
    expect((err as ApiError).code).toBe("UNKNOWN");
  });

  it("falls back to ApiError when the server returns a NON-EMPTY non-JSON body", async () => {
    // Regression: a proxy 502 HTML page (or a plain-text panic) is a
    // non-empty body that is NOT our JSON envelope. The parse must
    // not throw a raw SyntaxError — that would escape every screen's
    // `e instanceof ApiError` branch and surface as an opaque
    // "failed to load" with no status/code.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("<html><body>502 Bad Gateway</body></html>", {
          status: 502,
          statusText: "Bad Gateway",
          headers: { "Content-Type": "text/html" },
        }),
      ),
    );
    const err = await apiFetch("/api/instances").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(502);
    expect((err as ApiError).code).toBe("UNKNOWN");
  });

  it("returns undefined (not a thrown SyntaxError) on a malformed 2xx body", async () => {
    // A 2xx with a non-JSON body is pathological but must not crash
    // the caller with a raw SyntaxError; it decodes to undefined.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("not json at all", {
          status: 200,
          headers: { "Content-Type": "text/plain" },
        }),
      ),
    );
    await expect(apiFetch("/api/version")).resolves.toBeUndefined();
  });
});

describe("planTransfer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("preserves structured validation errors from the preview endpoint", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: "INVALID_REQUEST",
            error: 'amount "browser transfer" is not a valid decimal',
          }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    await expect(planTransfer("demo", "DEMO", "alice::abc", "browser transfer")).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message: 'amount "browser transfer" is not a valid decimal',
    });
  });
});

describe("issueJwt", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("always requests the raw LocalNet JWT", async () => {
    // Use mockImplementation so each call gets a fresh Response —
    // fetch's body is one-shot and the second .text() throws
    // "Body is unusable" if we share a single instance.
    const fetchSpy = vi.fn().mockImplementation(
      () =>
        Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
               token: "header.payload.signature",
              party: "alice::abc",
              audience: "https://canton.network.global",
              role: "app-provider",
              warning_dev_secret: "dev secret in use",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
    );
    vi.stubGlobal("fetch", fetchSpy);

    await issueJwt("demo", { role: "app-provider" });
    await issueJwt("demo", { role: "app-provider" });

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    const [firstURL] = fetchSpy.mock.calls[0];
    const [secondURL] = fetchSpy.mock.calls[1];
    expect(firstURL).toBe("/api/instances/demo/jwt");
    expect(secondURL).toBe("/api/instances/demo/jwt");
  });

  it("url-encodes the instance name (defence against path traversal)", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ schema_version: 1, token: "header.payload.signature", party: "x", audience: "y", role: "z", warning_dev_secret: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchSpy);
    await issueJwt("../etc/passwd", { role: "app-provider" });
    const [url] = fetchSpy.mock.calls[0];
    // %2F = '/', so '..%2Fetc%2Fpasswd' — never reaches the
    // server as a literal slash that could be mis-routed.
    expect(url).toBe("/api/instances/..%2Fetc%2Fpasswd/jwt");
  });
});

describe("setObservability", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs the per-component body through apiFetch and decodes the envelope", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          schema_version: 1,
          instance: "demo",
          prometheus: true,
          grafana: true,
          enabled: true,
          prometheus_ui: 19090,
          grafana_ui: 13000,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchSpy);

    const res = await setObservability("demo", { prometheus: true, grafana: true });

    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe("/api/instances/demo/observability");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ prometheus: true, grafana: true });
    // Routed through apiFetch → Content-Type is set for a body request.
    expect(init.headers["Content-Type"]).toBe("application/json");
    expect(res.prometheus).toBe(true);
    expect(res.grafana_ui).toBe(13000);
  });

  it("maps an error envelope to ApiError (does not bypass the chokepoint)", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ code: "OBSERVABILITY_TOGGLE_FAIL", error: "docker compose up failed" }),
        { status: 502, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchSpy);
    await expect(
      setObservability("demo", { prometheus: true, grafana: false }),
    ).rejects.toBeInstanceOf(ApiError);
  });

  it("url-encodes the instance name", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ schema_version: 1, instance: "x", prometheus: false, grafana: false, enabled: false }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchSpy);
    await setObservability("../etc/passwd", { prometheus: false, grafana: false });
    const [url] = fetchSpy.mock.calls[0];
    expect(url).toBe("/api/instances/..%2Fetc%2Fpasswd/observability");
  });
});

// Explorer contract-detail + SSE clients.
describe("fetchContractDetail", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("encodes both instance and contract id components in the path", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          schema_version: 1,
          instance: "demo",
          role: "app-user",
          contract: {
            contract_id: "00abc:0",
            signatories: [],
            observers: [],
            archived: false,
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchSpy);
    await fetchContractDetail("demo", "00abc:0", "app-user");
    const [url] = fetchSpy.mock.calls[0];
    // : is an unreserved character per RFC 3986 in path segments
    // but encodeURIComponent escapes it as %3A — that's fine,
    // the backend's PathValue decode handles it.
    expect(url).toBe("/api/instances/demo/contracts/00abc%3A0?role=app-user");
  });

  it("propagates 404 NOT_FOUND from the backend as an ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: "NOT_FOUND",
            error: "contract not visible to this party set",
          }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    await expect(
      fetchContractDetail("demo", "missing", "app-user"),
    ).rejects.toThrow(/not visible/);
  });
});

// The server-side idempotency middleware is opt-in on the
// Idempotency-Key header (internal/ui/handlers/idempotency.go). It was
// dead code until the client started sending the header — these tests
// pin that every value-moving token mutation now does, so a retry /
// double-click can't mint/transfer/burn twice.
describe("token mutations send an Idempotency-Key", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const headerOf = (init: RequestInit | undefined): string | undefined => {
    const h = init?.headers as Record<string, string> | undefined;
    return h?.["Idempotency-Key"];
  };

  it("attaches a fresh UUID Idempotency-Key to each mutating POST", async () => {
    // Deterministic, monotonic UUIDs so we can assert uniqueness.
    let n = 0;
    vi.stubGlobal("crypto", {
      ...globalThis.crypto,
      randomUUID: () => `uuid-${++n}` as `${string}-${string}-${string}-${string}-${string}`,
    });
    const fetchSpy = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(new Response(JSON.stringify({}), { status: 200 })),
      );
    vi.stubGlobal("fetch", fetchSpy);

    await createToken("demo", {
      name: "Retail",
      symbol: "RTK",
      decimals: 6,
      initial_supply: "1000",
      issuer: "alice::abc",
    });
    await mintToken("demo", "RTK", "alice::abc", "10");
    await transferToken("demo", "RTK", "alice::abc", "bob::def", "5");
    await faucetToken("demo", "RTK", "alice::abc", "1");
    await burnToken("demo", "RTK", "alice::abc", "1");
    await acceptTransfer("demo", "ti-1");

    expect(fetchSpy).toHaveBeenCalledTimes(6);
    const keys = fetchSpy.mock.calls.map(([, init]) => headerOf(init));
    for (const k of keys) {
      expect(k).toMatch(/^uuid-\d+$/);
    }
    // Each submission gets its own key (no collisions across verbs).
    expect(new Set(keys).size).toBe(6);
  });
});

describe("openContractsStream", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("constructs an EventSource against /contracts/stream with role", () => {
    const seen: string[] = [];
    class FakeEventSource {
      url: string;
      constructor(url: string) {
        this.url = url;
        seen.push(url);
      }
      close() {}
      addEventListener() {}
      removeEventListener() {}
    }
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const es = openContractsStream("demo", "app-provider");
    expect(seen).toEqual([
      "/api/instances/demo/contracts/stream?role=app-provider",
    ]);
    es.close();
  });
});

describe("downloadSnapshot", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document
      .querySelectorAll('iframe[name="_dpm_dl"]')
      .forEach((n) => n.remove());
  });

  it("rejects with an ApiError when the hidden iframe navigates (server error body)", async () => {
    // On failure the server returns an inline JSON error body with no
    // Content-Disposition, so the hidden iframe navigates to it and
    // fires `load`. That asymmetry is how we detect a failed download
    // without buffering the (possibly huge) success tar.
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(
      function (this: HTMLFormElement) {
        const frame = document.querySelector(
          'iframe[name="_dpm_dl"]',
        ) as HTMLIFrameElement;
        // Simulate the browser rendering the error document.
        frame.dispatchEvent(new Event("load"));
      },
    );
    const err = await downloadSnapshot("ghost").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("SNAPSHOT_DOWNLOAD_FAILED");
  });

  it("resolves (no error) when no navigation happens within the settle window", async () => {
    // Success path: the body is handed to the download manager, the
    // iframe never navigates, so no `load` fires and the settle timer
    // resolves cleanly.
    vi.useFakeTimers();
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(
      () => {},
    );
    const p = downloadSnapshot("pebble");
    let resolved = false;
    void p.then(() => {
      resolved = true;
    });
    // Before the settle window elapses the promise is still pending.
    await vi.advanceTimersByTimeAsync(100);
    expect(resolved).toBe(false);
    // After the settle window it resolves without rejecting.
    await vi.advanceTimersByTimeAsync(2000);
    await expect(p).resolves.toBeUndefined();
  });
});
