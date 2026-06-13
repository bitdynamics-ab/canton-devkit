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

// holdingsResponse lets a test drive the #63 source banner: pass
// { source: "registry", rows: [...] } to exercise the pseudo-balance
// disclaimer. Defaults to a live empty ACS (no banner) so existing
// callers are unaffected.
function stubFetch(
  tokens: Array<{ symbol: string; name: string }>,
  holdingsResponse?: {
    source: "ledger" | "registry";
    rows: Array<{ party: string; amount: string }>;
  },
) {
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
      if (url.includes("/transfer") && url.includes("plan=1")) {
        return json({
          schema_version: 1,
          plan: {
            instrument: "RTK", from: "bob", amount: "100",
            inputs: [{ contract_id: "00abc123def456", amount: "275.0" }],
            total_input: "275.0", change: "175.0", sufficient: true,
          },
        });
      }
      if (url.startsWith("/api/parties")) {
        return json({
          schema_version: 1,
          parties: [
            { alias: "app-user", party_id: "alice::abc", role: "app-user", is_local: true, created_at: "2026-05-30T10:00:00Z" },
            { alias: "treasury", party_id: "bob::def", role: "app-user", is_local: true, created_at: "2026-05-30T11:00:00Z" },
          ],
        });
      }
      if (url.startsWith("/api/tokens/") && url.includes("/activity")) {
        return json({
          schema_version: 1,
          events: [
            {
              offset: 1106, update_id: "u1106", record_time: "2026-05-30T16:50:45Z",
              instrument_id: "RTK", kind: "mint", amount: "1000",
              receivers: [{ party: "bob::def", amount: "1000" }],
            },
            {
              offset: 1200, update_id: "u1200", record_time: "2026-05-30T17:00:00Z",
              instrument_id: "RTK", kind: "transfer", amount: "100",
              senders: [{ party: "bob::def", amount: "100" }],
              receivers: [{ party: "alice::abc", amount: "100" }],
            },
          ],
        });
      }
      if (url.startsWith("/api/tokens/") && url.includes("/summary")) {
        return json({
          schema_version: 1,
          summary: {
            instrument_id: "RTK",
            admin: "alice::abc",
            total_supply: "1275.0",
            holder_count: 2,
            contract_count: 3,
            holders: [
              { party: "bob::def", balance: "1275.0", contract_count: 3, pct_of_supply: "100.0" },
              { party: "alice::abc", balance: "0.0", contract_count: 0, pct_of_supply: "0.0" },
            ],
          },
        });
      }
      if (url.startsWith("/api/tokens/") && url.includes("/holdings")) {
        const src = holdingsResponse?.source ?? "ledger";
        const rows = (holdingsResponse?.rows ?? []).map((r) => ({
          instrument_id: "RTK",
          party: r.party,
          amount: r.amount,
          source: src,
        }));
        return json({ schema_version: 1, source: src, holdings: rows });
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

  it("disables Burn for a non-native (recorded / Amulet) instrument", async () => {
    // The stubbed RTK comes via the tokens fallback with status "recorded"
    // (on_ledger=false), so Burn — like Mint — is gated to native on-ledger
    // CIP-0112 v2 tokens.
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    const burn = await screen.findByRole("button", { name: /Burn/i }, { timeout: 4000 });
    expect(burn).toBeDisabled();
    expect(burn.getAttribute("title") ?? "").toMatch(/native|Amulet|CIP-0112/i);
  });

  it("opens the faucet modal from the instrument action rail", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Faucet/i }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryByText(/Faucet RTK/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    // funded-party hint on the optional source field
    expect(screen.queryByText(/defaults to funded party/i)).toBeInTheDocument();
  });

  it("offers an auto-accept toggle in the transfer modal", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: "→ Transfer" }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryByText(/Auto-accept/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
  });

  it("shows the coin-selection preview in the transfer modal", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: "→ Transfer" }, { timeout: 4000 }));
    // fill From + Amount → the dry-run plan fires (debounced)
    const inputs = await screen.findAllByRole("textbox");
    await user.type(inputs[0], "bob");
    await user.type(inputs[2], "100");
    await waitFor(
      () => expect(screen.queryByText(/Coin selection preview/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.queryByText(/change/i)).toBeInTheDocument();
  });

  it("renders the instrument KPI strip and holder distribution", async () => {
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    // KPI strip — supply + holder/contract counts.
    await waitFor(
      () => expect(screen.queryAllByText(/Total supply/i).length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    expect(screen.queryAllByText(/Holding contracts/i).length).toBeGreaterThan(0);
    // Holder distribution table — share-of-supply.
    expect(screen.queryAllByText(/Holder distribution/i).length).toBeGreaterThan(0);
    expect(screen.queryAllByText(/100\.0%/).length).toBeGreaterThan(0);
  });

  it("switches to the Activity tab and renders the mint/transfer feed", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    // Wait for the detail pane (Overview tab) to mount, then switch tabs.
    await user.click(await screen.findByRole("button", { name: /^activity$/i }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryAllByText(/mint/i).length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    expect(screen.queryAllByText(/transfer/i).length).toBeGreaterThan(0);
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

  it("labels parties by their registered alias in the matrix", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Holdings matrix/i }, { timeout: 4000 }));
    // bob::def is aliased "treasury" → the matrix shows the alias, not "bob".
    await waitFor(
      () => expect(screen.queryAllByText(/treasury/).length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
  });

  it("opens the party manager and lists registered aliases", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Parties/i }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryByPlaceholderText(/new alias/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.queryAllByText(/treasury/).length).toBeGreaterThan(0);
  });

  // #63: when the holdings endpoint reports source="registry" (no live
  // ledger), the table must carry an explicit "pseudo-balance, not
  // on-ledger" disclaimer so the fabricated rows can't be mistaken for
  // real holdings.
  it("shows the registry pseudo-balance disclaimer when holdings are not on-ledger", async () => {
    stubFetch(
      [{ symbol: "RTK", name: "Retail Token" }],
      { source: "registry", rows: [{ party: "alice::abc", amount: "1000000" }] },
    );
    renderTokens();
    await waitFor(
      () => expect(screen.queryByText(/registry pseudo-balances/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    // The "not on-ledger holdings" copy is split across <b> tags, so the
    // trailing fragment lands in its own text node — match that.
    expect(screen.queryByText(/on-ledger holdings/i)).toBeInTheDocument();
  });

  it("does NOT show the disclaimer when holdings are live on-ledger", async () => {
    stubFetch(
      [{ symbol: "RTK", name: "Retail Token" }],
      { source: "ledger", rows: [{ party: "alice::abc", amount: "42.0" }] },
    );
    renderTokens();
    // Wait for the live amount to render, then assert the banner is absent.
    await waitFor(
      () => expect(screen.queryAllByText("42.0").length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    expect(screen.queryByText(/registry pseudo-balances/i)).not.toBeInTheDocument();
  });
});
