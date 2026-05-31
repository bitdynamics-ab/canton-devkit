import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { TokensScreen } from "./TokensScreen";

// TokensScreen tests — minimal smoke that the screen mounts under the
// shared providers App.tsx uses, and that the list/empty paths render.
// The interactive modal flows are exercised by the live UI on a
// running LocalNet, not by unit tests; replicating the InstanceSelection
// + apiFetch + react-router timing here is brittle and low-value.

afterEach(() => vi.unstubAllGlobals());

function stubFetch(tokens: Array<{ symbol: string; name: string }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      const json = (body: unknown, status = 200) =>
        Promise.resolve(
          new Response(JSON.stringify(body), {
            status,
            headers: { "Content-Type": "application/json" },
          }),
        );
      if (url.startsWith("/api/version")) {
        return json({ name: "canton-devkit", schema_version: 1 });
      }
      if (url.startsWith("/api/instances")) {
        return json({
          schema_version: 1,
          instances: [{ name: "demo", status: "running" }],
        });
      }
      if (url.startsWith("/api/tokens/matrix")) {
        return json({
          schema_version: 1,
          matrix: {
            parties: ["alice::abc", "bob::def"],
            instruments: [
              { admin: "alice::abc", instrument_id: "RTK", symbol: "RTK", standard: "CIP-0112 v2", on_ledger: true },
            ],
            cells: [{ party: "bob::def", instrument_id: "RTK", amount: "1275.0" }],
            totals: [{ party: "", instrument_id: "RTK", amount: "1275.0" }],
          },
        });
      }
      if (url.startsWith("/api/tokens/") && url.includes("/holdings")) {
        return json({ schema_version: 1, holdings: [] });
      }
      if (url.startsWith("/api/tokens")) {
        return json({
          schema_version: 1,
          tokens: tokens.map((t) => ({
            name: t.name,
            symbol: t.symbol,
            decimals: 6,
            initial_supply: "1000000",
            issuer_party: "alice::abc",
            instrument_id: "abcd1234567890",
            created_at: "2026-05-25T10:00:00Z",
            status: "recorded",
          })),
        });
      }
      return Promise.resolve(new Response(null, { status: 204 }));
    }),
  );
}

function renderTokens() {
  return render(
    <MemoryRouter initialEntries={["/tokens?instance=demo"]}>
      <InstanceSelectionProvider>
        <TokensScreen />
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("TokensScreen", () => {
  it("renders the Tokens header even before an instance resolves", () => {
    stubFetch([]);
    renderTokens();
    expect(screen.getByText("Tokens")).toBeInTheDocument();
  });

  it("lists instruments from /api/tokens once they load", async () => {
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await waitFor(
      () => expect(screen.queryAllByText(/Retail Token/).length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
  });

  it("shows the empty-state prompt when no instruments exist on the instance", async () => {
    stubFetch([]);
    renderTokens();
    await waitFor(
      () => expect(screen.queryByText(/No instruments on/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
  });

  it("disables Burn with the BIT-216 reason (no deployable token supports it)", async () => {
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    const burn = await screen.findByRole("button", { name: /Burn/i }, { timeout: 4000 });
    expect(burn).toBeDisabled();
    expect(burn.getAttribute("title") ?? "").toMatch(/AllocationV2|DvP|BIT-216/);
  });

  it("switches to the Holdings matrix lens and renders the pivot", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Holdings matrix/i }, { timeout: 4000 }));
    // totals row + the summed cell appear
    await waitFor(
      () => expect(screen.queryByText(/Σ total/)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.queryAllByText("1275.0").length).toBeGreaterThan(0);
  });
});
