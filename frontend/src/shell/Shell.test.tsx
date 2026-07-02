import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { Shell } from "./Shell";
import { InstanceSelectionProvider } from "./useInstanceSelection";

// Shell tests — exercise the InstanceSwitcher dropdown, the
// CommandPalette open/close flow, and the SkipLink target wiring.
// Each is a small but real piece of interactive UX that's easy
// to break silently in a refactor.
//
// What we DON'T test here:
//   - HealthPill (covered by useConnectionHealth.test.ts)
//   - filter() (covered by CommandPalette.test.tsx)
//   - useInstanceSelection auto-pick (covered by its own suite)
//
// What we DO test:
//   1. InstanceSwitcher renders the selected instance
//   2. InstanceSwitcher: open → click another instance →
//      selection changes (the onMouseDown-beats-onBlur race)
//   3. CommandPalette: ⌘K opens, Esc closes
//   4. SkipLink targets the #main-content element

function renderShell(initialPath = "/") {
  // Mock fetch for the instance list + version handshake so
  // useInstanceSelection + useConnectionHealth don't error out.
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/instances/")) {
        // single-instance detail endpoint not exercised here
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
                  ports: "5011:5011",
                  started_ago: "2m",
                },
                {
                  name: "hubble",
                  status: "stopped",
                  splice_version: "0.4.11",
                  ports: "",
                  started_ago: "",
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/api/version")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ name: "canton-devkit", schema_version: 1 }),
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
        <Shell>
          <Routes>
            <Route path="/" element={<div>home content</div>} />
            <Route path="/explorer" element={<div>explorer content</div>} />
          </Routes>
        </Shell>
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("Shell — InstanceSwitcher", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the auto-selected instance name in the pill", async () => {
    renderShell();
    // First-running rule picks "demo" since hubble is stopped.
    await waitFor(() => {
      expect(screen.getByText("demo")).toBeInTheDocument();
    });
  });

  it("clicking a dropdown row switches the selection", async () => {
    renderShell();
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());

    // Open the dropdown. The trigger label includes "instance:"
    // followed by the current name.
    const trigger = screen.getByRole("button", { expanded: false });
    await userEvent.click(trigger);

    // Listbox renders with both instances. Scope subsequent
    // queries to it so we don't accidentally match the trigger.
    const listbox = await screen.findByRole("listbox");
    const hubbleOpt = within(listbox).getByRole("option", { name: /hubble/ });

    // The onMouseDown-beats-onBlur race: if the menu closed on blur
    // before the click fired, this would never register.
    // userEvent.click does mouseDown+mouseUp+click, so this exercises
    // the real path.
    await userEvent.click(hubbleOpt);

    // The trigger's strong tag now shows "hubble" — the new
    // selected instance.
    await waitFor(() => {
      const newTrigger = screen.getByRole("button", { expanded: false });
      expect(newTrigger).toHaveTextContent("hubble");
    });
  });
});

describe("Shell — CommandPalette hotkey", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("⌘K opens the palette; Esc closes it", async () => {
    renderShell();
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());

    // Palette dialog is absent at first.
    expect(screen.queryByRole("dialog", { name: /command palette/i })).not.toBeInTheDocument();

    // Press ⌘K (use Meta — userEvent maps {Meta} to e.metaKey).
    const user = userEvent.setup();
    await user.keyboard("{Meta>}k{/Meta}");

    expect(await screen.findByRole("dialog", { name: /command palette/i })).toBeInTheDocument();

    // Esc dismisses.
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: /command palette/i })).not.toBeInTheDocument();
    });
  });
});

describe("Shell — sidebar nav preserves ?instance=", () => {
  afterEach(() => vi.unstubAllGlobals());

  // The bug: clicking a sidebar tab on a per-instance route used to
  // drop the ?instance= query, bouncing the user to "No instance
  // selected" on every nav. Pin the fix: sidebar links for
  // instance-scoped routes must thread the current instance.
  it("threads ?instance= into instance-scoped routes (Explorer, Wallet, DAR, Metrics, Tokens)", async () => {
    renderShell("/wallet?instance=demo");
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());

    for (const label of ["Explorer", "DAR Manager", "Metrics", "Tokens", "Wallet"]) {
      const link = screen.getByRole("link", { name: label });
      const href = link.getAttribute("href") || "";
      expect(href, `${label} dropped the instance param`).toContain("instance=demo");
    }
  });

  it("leaves instance-INDEPENDENT routes (Overview, Agent Skills) without ?instance=", async () => {
    renderShell("/wallet?instance=demo");
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());

    for (const label of ["Overview", "Agent Skills"]) {
      const link = screen.getByRole("link", { name: label });
      const href = link.getAttribute("href") || "";
      expect(href, `${label} should not carry instance param`).not.toContain("instance=");
    }
  });

  it("omits ?instance= entirely when no instance is in URL", async () => {
    renderShell("/");
    // No auto-pick assertion needed — just verify the link is bare.
    await waitFor(() =>
      expect(screen.getByRole("link", { name: "Explorer" })).toBeInTheDocument(),
    );
    const href = screen.getByRole("link", { name: "Explorer" }).getAttribute("href") || "";
    expect(href).not.toContain("instance=");
  });
});

describe("Shell — SkipLink", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("the SkipLink targets #main-content (matches the <main> id)", async () => {
    renderShell();
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());

    const link = screen.getByRole("link", { name: /skip to main content/i });
    expect(link).toHaveAttribute("href", "#main-content");

    // The target must exist for the link to make sense — without
    // this the link is dead weight. Pin the contract here so a
    // future refactor that renames the id breaks the test, not
    // the keyboard user.
    const main = document.getElementById("main-content");
    expect(main).not.toBeNull();
    expect(main?.tagName.toLowerCase()).toBe("main");
  });
});
