import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  SCHEMA_VERSION,
  apiFetch,
  downloadSnapshot,
  fetchContractDetail,
  issueJwt,
  openContractsStream,
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
//      caller and its include_jwt query toggle is part of the
//      redacted-by-default contract.

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

describe("issueJwt", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("appends ?include_jwt=true only when explicitly opted in", async () => {
    // Use mockImplementation so each call gets a fresh Response —
    // fetch's body is one-shot and the second .text() throws
    // "Body is unusable" if we share a single instance.
    const fetchSpy = vi.fn().mockImplementation(
      () =>
        Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              token: "<redacted>",
              redacted: true,
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

    await issueJwt("demo", { role: "app-provider" }, false);
    await issueJwt("demo", { role: "app-provider" }, true);

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    const [redactedURL] = fetchSpy.mock.calls[0];
    const [revealedURL] = fetchSpy.mock.calls[1];
    expect(redactedURL).toBe("/api/instances/demo/jwt");
    expect(revealedURL).toBe("/api/instances/demo/jwt?include_jwt=true");
  });

  it("url-encodes the instance name (defence against path traversal)", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ schema_version: 1, token: "<redacted>", party: "x", audience: "y", role: "z", warning_dev_secret: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchSpy);
    await issueJwt("../etc/passwd", { role: "app-provider" }, false);
    const [url] = fetchSpy.mock.calls[0];
    // %2F = '/', so '..%2Fetc%2Fpasswd' — never reaches the
    // server as a literal slash that could be mis-routed.
    expect(url).toBe("/api/instances/..%2Fetc%2Fpasswd/jwt");
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
