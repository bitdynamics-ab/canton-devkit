import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { CommandPalette, filter } from "./CommandPalette";
import { InstanceSelectionProvider } from "./useInstanceSelection";

// Pure-function tests for the palette's filter — the only piece
// of the palette with non-trivial logic that's worth pinning in
// isolation. The keyboard / open / close flow is a smoke test
// best done in a manual pass (or a future Playwright run); doing
// it here would mostly exercise jsdom's event quirks.
//
// The contract the filter pins:
//   1. empty query → everything, in original order
//   2. case-insensitive substring on label OR hint
//   3. preserves input order (no reranking — grouping stays stable)
//   4. trimmed (whitespace pad doesn't tank the search)

interface TestAction {
  id: string;
  group: "Navigate" | "Switch instance";
  label: string;
  hint?: string;
  perform: () => void;
}

const ACTIONS: TestAction[] = [
  { id: "nav-overview", group: "Navigate", label: "Overview", hint: "/", perform: () => {} },
  { id: "nav-explorer", group: "Navigate", label: "Explorer", hint: "/explorer", perform: () => {} },
  { id: "nav-dar", group: "Navigate", label: "DAR Manager", hint: "/dar", perform: () => {} },
  { id: "inst-demo", group: "Switch instance", label: "demo", hint: "running · 0.4.12", perform: () => {} },
  { id: "inst-hubble", group: "Switch instance", label: "hubble", hint: "stopped · 0.4.11", perform: () => {} },
];

describe("CommandPalette filter", () => {
  it("returns everything for an empty query", () => {
    expect(filter(ACTIONS, "")).toHaveLength(ACTIONS.length);
  });

  it("returns everything for a whitespace-only query", () => {
    // Easy to break with a naive `if (!query)` check that
    // wouldn't trim. The palette receives raw input; padding
    // a stray space mid-type shouldn't blank the list.
    expect(filter(ACTIONS, "   ")).toHaveLength(ACTIONS.length);
  });

  it("matches case-insensitively against the label", () => {
    expect(filter(ACTIONS, "EXPLORER").map((a) => a.id)).toEqual(["nav-explorer"]);
  });

  it("matches against the hint as well as the label", () => {
    // "running" only appears in demo's hint, not the label.
    expect(filter(ACTIONS, "running").map((a) => a.id)).toEqual(["inst-demo"]);
  });

  it("returns multiple matches in input order", () => {
    // Both demo and explorer contain "e" — input order preserved.
    const got = filter(ACTIONS, "e").map((a) => a.id);
    expect(got).toEqual([
      "nav-overview",
      "nav-explorer",
      "nav-dar", // "DAR Manager"
      "inst-demo",
      "inst-hubble", // "stopped"
    ]);
  });

  it("returns [] when nothing matches", () => {
    expect(filter(ACTIONS, "xyzzy")).toEqual([]);
  });
});

// Integration test for the palette → router navigation path. Every
// destination carries `?instance=` so navigating through a host-level screen
// cannot silently reset the selected LocalNet.
function LocationProbe() {
  // Surfaces the current pathname + search so assertions can read
  // exactly what navigate() landed on.
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderPalette(initialPath: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/instances/")) {
        return Promise.resolve(new Response("{}", { status: 200 }));
      }
      if (url.includes("/api/instances")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instances: [
                {
                  name: "demo",
                  status: "running",
                  splice_version: "0.4.12",
                  ports: "",
                  started_ago: "",
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return Promise.reject(new Error(`unmocked fetch: ${url}`));
    }),
  );
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <InstanceSelectionProvider>
        <CommandPalette />
        <Routes>
          <Route path="*" element={<LocationProbe />} />
        </Routes>
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("CommandPalette — nav preserves ?instance=", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("threads ?instance= when navigating to an instance route", async () => {
    renderPalette("/?instance=demo");
    const user = userEvent.setup();

    // ⌘K to open.
    await user.keyboard("{Meta>}k{/Meta}");
    await screen.findByRole("dialog", { name: /command palette/i });

    // Click the Wallet row.
    const wallet = await screen.findByRole("option", { name: /Wallet/ });
    await user.click(wallet);

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/wallet?instance=demo",
      );
    });
  });

  it("preserves ?instance= on host-level routes too", async () => {
    renderPalette("/wallet?instance=demo");
    const user = userEvent.setup();

    await user.keyboard("{Meta>}k{/Meta}");
    await screen.findByRole("dialog", { name: /command palette/i });

    const overview = await screen.findByRole("option", { name: /Overview/ });
    await user.click(overview);

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/?instance=demo",
      );
    });
  });
});
