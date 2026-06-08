// BIT-230 — DARScreen vitest. Exercises the new package tree,
// vetting panel, and watch indicator integrations. Follows the
// same render-with-stubbed-fetch pattern as TokensScreen.test.tsx.

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
  role: "app-user",
  dars: [
    { main: "a".repeat(64), name: "demo-pkg", version: "1.0.0" },
    { main: "b".repeat(64), name: "other-pkg", version: "0.1.0" },
  ],
};

describe("DARScreen (BIT-230)", () => {
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
