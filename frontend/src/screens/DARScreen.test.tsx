// DARScreen vitest. Exercises the package tree, vetting panel, and
// watch indicator integrations. Follows the same
// render-with-stubbed-fetch pattern as TokensScreen.test.tsx.

import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { DARScreen } from "./DARScreen";

afterEach(() => vi.unstubAllGlobals());

// stubFetch is a tiny dispatcher matching URLs to canned JSON. The
// DAR screen mounts inside InstanceSelectionProvider, which itself
// fetches /api/instances on mount; we have to handle that or the
// provider gets stuck in its loading state and the DAR screen
// renders the "no instance" empty state.
function stubFetch(handlers: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      // Longest-prefix-match so e.g. /api/instances/demo/dar wins
      // over /api/instances when both are registered.
      const key = Object.keys(handlers)
        .filter((k) => url.startsWith(k))
        .sort((a, b) => b.length - a.length)[0];
      if (!key) {
        return Promise.resolve(
          new Response(JSON.stringify({ error: "no stub for " + url }), {
            status: 404,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      // Allow per-handler to discriminate by method via callable form.
      const val = handlers[key];
      const body =
        typeof val === "function"
          ? (val as (u: string, i?: RequestInit) => unknown)(url, init)
          : val;
      if (body instanceof Response) return Promise.resolve(body);
      return Promise.resolve(
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );
}

// EventSource is not implemented in jsdom; stub it so the watch
// indicator's useEffect doesn't blow up the test runtime. The fake
// captures the most recent listener so individual tests can fire
// synthetic events into the component.
class FakeEventSource {
  static last: FakeEventSource | null = null;
  url: string;
  listeners: Record<string, (e: MessageEvent) => void> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.last = this;
  }
  addEventListener(name: string, fn: (e: MessageEvent) => void) {
    this.listeners[name] = fn;
  }
  close() {
    /* no-op */
  }
  fire(name: string, data: unknown) {
    const fn = this.listeners[name];
    if (fn) fn({ data: JSON.stringify(data) } as MessageEvent);
  }
}

// stubXHR replaces XMLHttpRequest with a fake that resolves send()
// with a canned status + body. uploadDARs() uses XHR (for progress),
// not fetch, so the fetch stub above can't intercept it. Returns a
// handle whose sendCount lets a test assert send() was (or wasn't)
// reached.
function stubXHR(resp: { status: number; responseText: string }) {
  const handle = { sendCount: 0 };
  class FakeXHR {
    status = 0;
    responseText = "";
    private loadFns: Array<() => void> = [];
    upload = { addEventListener: (_: string, __: (e: unknown) => void) => {} };
    open(_method: string, _url: string) {
      /* no-op */
    }
    addEventListener(name: string, fn: () => void) {
      if (name === "load") this.loadFns.push(fn);
    }
    send(_body?: unknown) {
      handle.sendCount++;
      this.status = resp.status;
      this.responseText = resp.responseText;
      // Fire load asynchronously so the Promise in uploadDARs settles
      // on a later microtask, like a real request.
      queueMicrotask(() => this.loadFns.forEach((fn) => fn()));
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR as unknown as typeof XMLHttpRequest);
  return handle;
}

// darFile builds a small valid-named .dar File for the upload input.
function darFile(name: string): File {
  return new File([new Uint8Array([1, 2, 3])], name, {
    type: "application/octet-stream",
  });
}

// oversizeDarFile builds a .dar File whose reported size exceeds the
// client cap without allocating the bytes — jsdom derives size from
// content, so we override the size getter directly.
function oversizeDarFile(name: string, size: number): File {
  const f = darFile(name);
  Object.defineProperty(f, "size", { value: size });
  return f;
}

function withProviders(ui: React.ReactElement) {
  return (
    <MemoryRouter>
      <InstanceSelectionProvider>{ui}</InstanceSelectionProvider>
    </MemoryRouter>
  );
}

const fakeDAR = {
  schema_version: 1,
  instance: "demo",
  role: "app-provider",
  dars: [
    { main: "a".repeat(64), name: "demo-pkg", version: "1.0.0" },
    { main: "b".repeat(64), name: "other-pkg", version: "0.1.0" },
  ],
};

describe("DARScreen", () => {
  it("replaces the loading label with an accessible error state", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/instances/demo/dar": () =>
        new Response(
          JSON.stringify({ code: "INTERNAL", error: "list dars" }),
          {
            status: 502,
            headers: { "Content-Type": "application/json" },
          },
        ),
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
    });

    render(withProviders(<DARScreen />));

    expect(await screen.findByRole("alert")).toHaveTextContent("list dars");
    expect(screen.getByText("could not load packages on demo")).toBeInTheDocument();
    expect(screen.queryByText("loading…")).not.toBeInTheDocument();
  });

  it("renders the watch-mode indicator with the Idle badge by default", async () => {
    // Replace EventSource before the screen mounts so its useEffect
    // picks up the stub.
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
    });

    render(withProviders(<DARScreen />));

    // Wait for the package list to render, then assert the watch
    // card defaults to Idle.
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/Idle/i)).toBeInTheDocument();
    // The card shows the suggested CLI command.
    expect(screen.getByText(/dpm localnet dar watch/)).toBeInTheDocument();
  });

  it("flips the watch badge to Watching after a watch_started SSE event", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
    });

    render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );

    // Fire a watch_started event into the latest EventSource.
    const es = FakeEventSource.last!;
    es.fire("message", {
      instance: "demo",
      event: "watch_started",
      at: Math.floor(Date.now() / 1000),
      detail: "/proj",
    });

    await waitFor(() =>
      expect(screen.getByText(/Watching/i)).toBeInTheDocument(),
    );
  });

  it("shows the partial-upload banner when one participant fails", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
    });
    // uploadDARs uses XMLHttpRequest; stub it so send() resolves with a
    // 200 partial-failure envelope (one role ok, one role failed) —
    // the backend's documented partial-success shape.
    stubXHR({
      status: 200,
      responseText: JSON.stringify({
        schema_version: 1,
        instance: "demo",
        total_uploaded: 1,
        results: [
          { role: "app-user", ok: true, dar_ids: ["id1"], count: 1 },
          { role: "sv", ok: false, count: 0, error: "participant_admin port not recorded for role sv" },
        ],
      }),
    });

    const user = userEvent.setup();
    const { container } = render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );

    const fileInput = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await user.upload(fileInput, darFile("app.dar"));

    // The partial banner (role="alert") names the X/Y participant
    // count and surfaces the failing role's error.
    await waitFor(() =>
      expect(screen.getByText(/Partial upload/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/1\/2 participants succeeded/i)).toBeInTheDocument();
    expect(
      screen.getByText(/port not recorded for role sv/i),
    ).toBeInTheDocument();
  });

  it("shows the success banner when every participant succeeds", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
    });
    stubXHR({
      status: 200,
      responseText: JSON.stringify({
        schema_version: 1,
        instance: "demo",
        total_uploaded: 3,
        results: [
          { role: "app-user", ok: true, dar_ids: ["id1"], count: 1 },
          { role: "app-provider", ok: true, dar_ids: ["id2"], count: 1 },
          { role: "sv", ok: true, dar_ids: ["id3"], count: 1 },
        ],
      }),
    });

    const user = userEvent.setup();
    const { container } = render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );
    const fileInput = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await user.upload(fileInput, darFile("app.dar"));

    await waitFor(() =>
      expect(screen.getByText(/Uploaded 3 packages/i)).toBeInTheDocument(),
    );
    // The success banner uses role=status; the partial alert must NOT
    // be present.
    expect(screen.queryByText(/Partial upload/i)).not.toBeInTheDocument();
  });

  it("rejects an over-cap file client-side before any upload starts", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
    });
    // If the client gate works, XHR.send must never be called — fail
    // loudly if it is.
    const sent = stubXHR({ status: 200, responseText: "{}" });

    const user = userEvent.setup();
    const { container } = render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );
    const fileInput = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    // 65 MiB — just over the 64 MiB client cap (mirrors darUploadMax).
    await user.upload(fileInput, oversizeDarFile("huge.dar", 65 * 1024 * 1024));

    await waitFor(() =>
      expect(screen.getByText(/per-file cap is 64 MiB/i)).toBeInTheDocument(),
    );
    expect(sent.sendCount).toBe(0);
  });

  it("renders REAL per-participant vetting dots in the package list, not a hardcoded badge", async () => {
    // Each row's column must reflect the real GET …/vetting response
    // per participant, not a static badge.
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const mainA = "a".repeat(64);
    const mainB = "b".repeat(64);
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      // The vetting URLs are longer than the list URL, so the
      // longest-prefix matcher routes them here first.
      [`/api/instances/demo/dar/${mainA}/vetting`]: {
        schema_version: 1,
        instance: "demo",
        main: mainA,
        participants: [
          { role: "app-user", vetted: true },
          { role: "app-provider", vetted: false },
          { role: "sv", vetted: true },
        ],
      },
      [`/api/instances/demo/dar/${mainB}/vetting`]: {
        schema_version: 1,
        instance: "demo",
        main: mainB,
        participants: [
          { role: "app-user", vetted: false },
          { role: "app-provider", vetted: false },
          { role: "sv", error: "port not recorded" },
        ],
      },
      "/api/instances/demo/dar": fakeDAR,
    });

    render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );

    // Row A: app-user vetted. The cell carries a per-participant title
    // attribute we can assert on (real state, not a static label).
    await waitFor(() => {
      expect(screen.getByTitle("app-user: vetted")).toBeInTheDocument();
    });
    // app-provider is NOT vetted on either row — the exact case the
    // old hardcoded "vetted" badge got wrong. Both rows render it.
    expect(
      screen.getAllByTitle("app-provider: not vetted").length,
    ).toBeGreaterThanOrEqual(2);
    // Row B: app-user is unvetted (distinct from row A's vetted state),
    // proving the cell is per-row real data, not a constant.
    expect(screen.getByTitle("app-user: not vetted")).toBeInTheDocument();
    // Row B: a participant that couldn't be probed surfaces its error,
    // not a false "vetted".
    expect(screen.getByTitle(/sv: port not recorded/i)).toBeInTheDocument();
  });

  it("loads per-participant vetting state when a row is selected", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const vetURL = `/api/instances/demo/dar/${"a".repeat(64)}/vetting`;
    stubFetch({
      "/api/version": { name: "canton-devkit", schema_version: 1 },
      "/api/instances": {
        schema_version: 1,
        instances: [{ name: "demo", status: "running" }],
      },
      "/api/instances/demo/dar": fakeDAR,
      [vetURL]: {
        schema_version: 1,
        instance: "demo",
        main: "a".repeat(64),
        participants: [
          { role: "app-user", vetted: true },
          { role: "app-provider", vetted: false },
          { role: "sv", vetted: true },
        ],
      },
      // The inspect endpoint is also fired by the package tree
      // when a row is selected. Stub it to a minimal payload so
      // the tree renders without throwing.
      [`/api/instances/demo/dar/${"a".repeat(64)}/inspect`]: {
        schema_version: 1,
        instance: "demo",
        role: "app-user",
        main: "a".repeat(64),
        sha256: "deadbeef",
        packages: [],
      },
    });

    const user = userEvent.setup();
    render(withProviders(<DARScreen />));
    await waitFor(() =>
      expect(screen.getByText(/demo-pkg/i)).toBeInTheDocument(),
    );
    // Click the row to open the inspect drawer.
    await user.click(screen.getByText(/demo-pkg/i));

    // The vetting panel should appear with three rows.
    await waitFor(() => {
      expect(screen.getAllByText(/app-user/i).length).toBeGreaterThan(0);
    });
    // At least one switch is rendered (the vetting toggles).
    const switches = screen.getAllByRole("switch");
    // Three vetting switches + three "vet on upload" switches = 6 minimum.
    expect(switches.length).toBeGreaterThanOrEqual(3);
  });
});
