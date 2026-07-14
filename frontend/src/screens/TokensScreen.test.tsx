import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { TokensScreen, createErrorText } from "./TokensScreen";
import { ApiError } from "../api";

// TokensScreen tests — smoke that the screen mounts under the shared
// providers and the list/empty/modal paths render.

afterEach(() => vi.unstubAllGlobals());

// holdingsResponse drives the source banner: pass { source: "registry",
// rows } to exercise the pseudo-balance disclaimer. Defaults to a live
// empty ACS (no banner).
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
      if (url.startsWith("/api/tokens/identity")) {
        return json({
          schema_version: 1,
          instance: "demo",
          available_roles: ["app-user", "app-provider", "sv"],
          current_role: "app-user",
        });
      }
      if (url.startsWith("/api/tokens/matrix")) {
        return json({
          schema_version: 1,
          matrix: {
            parties: ["alice::abc", "bob::def"],
            instruments: [
              { admin: "alice::abc", instrument_id: "RTK", symbol: "RTK", standard: "CIP-0112 v2", on_ledger: true },
              { admin: "alice::abc", instrument_id: "GOV", symbol: "GOV", standard: "CIP-0112 v2", on_ledger: true },
            ],
            cells: [
              { party: "bob::def", instrument_id: "RTK", amount: "1275.0" },
              { party: "alice::abc", instrument_id: "GOV", amount: "42.0" },
            ],
            totals: [
              { party: "", instrument_id: "RTK", amount: "1275.0" },
              { party: "", instrument_id: "GOV", amount: "42.0" },
            ],
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
        // Honour ?limit so pagination tests can drive a full-page → "Load
        // more" state: synthesize `limit` newest-first rows.
        const limit = Number(new URL(url, "http://x").searchParams.get("limit") ?? "50");
        const pool = 120; // more rows than one page → "Load more" appears
        const n = Math.min(limit, pool);
        const events = Array.from({ length: n }, (_, i) => ({
          offset: 2000 - i, // descending → newest first
          update_id: `u${2000 - i}`,
          record_time: "2026-05-30T17:00:00Z",
          instrument_id: "RTK",
          kind: i === 0 ? "mint" : "transfer",
          source: "event_log",
          amount: "100",
          senders: i === 0 ? undefined : [{ party: "bob::def", amount: "100" }],
          receivers: [{ party: "alice::abc", amount: "100" }],
        }));
        return json({ schema_version: 1, events, truncated: false });
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
      if (url.includes("/api/tokens/demo")) {
        return json(
          {
            token: {
              name: "Demo Token", symbol: "DEMO", decimals: 6, initial_supply: "1000000",
              issuer_party: "demo-issuer::pid", instrument_id: "DEMO",
              created_at: "2026-06-16T10:00:00Z", status: "on-ledger",
            },
            issuer: { alias: "demo-issuer", party_id: "demo-issuer::pid", role: "app-user", created_at: "2026-06-16T10:00:00Z" },
            holder: { alias: "demo-holder", party_id: "demo-holder::pid", role: "app-user", created_at: "2026-06-16T10:00:00Z" },
            seeded: true,
          },
          201,
        );
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
      () => expect(screen.queryByText(/No tokens on/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    // The empty state leads with the one-click demo launch.
    expect(screen.getByRole("button", { name: /Launch demo token/i })).toBeInTheDocument();
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
    await user.click(await screen.findByRole("button", { name: "Transfer" }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryByText(/Auto-accept/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
  });

  it("shows the coin-selection preview in the transfer modal", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: "Transfer" }, { timeout: 4000 }));
    // From is now a PartyPicker dropdown; pick a registered party, then
    // fill Amount → the dry-run plan fires (debounced).
    await user.selectOptions(
      await screen.findByRole("combobox", { name: /from party/i }),
      "bob::def",
    );
    await user.type(await screen.findByRole("textbox", { name: /amount/i }), "100");
    await waitFor(
      () => expect(screen.queryByText(/Coin selection preview/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.queryByText(/change/i)).toBeInTheDocument();
  });

  it("party picker lists registered aliases and offers inline create", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    // The Faucet modal's "To party" is a PartyPicker dropdown, not a
    // raw party-id input — registered aliases from GET /api/parties
    // appear as options so the user never pastes a fingerprinted id.
    await user.click(await screen.findByRole("button", { name: /Faucet/i }, { timeout: 4000 }));
    const toParty = await screen.findByRole("combobox", { name: /to party/i });
    expect(within(toParty).getByRole("option", { name: /treasury/i })).toBeInTheDocument();
    expect(within(toParty).getByRole("option", { name: /app-user/i })).toBeInTheDocument();
    // Choosing "create new party" switches the picker to an inline alias
    // input (POST /api/parties), avoiding the separate Parties modal.
    await user.selectOptions(toParty, "__create__");
    expect(await screen.findByPlaceholderText(/new alias/i)).toBeInTheDocument();
  });

  it("launches a demo token in one click", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    // The "⚡ Launch demo" button POSTs /api/tokens/demo, which provisions
    // an issuer + instrument + funded holder server-side and returns the
    // DemoResult; the screen confirms with a success notice.
    await user.click(await screen.findByRole("button", { name: /Launch demo/i }, { timeout: 4000 }));
    await waitFor(
      () => expect(screen.queryByText(/Launched DEMO/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
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

  // Newest-first + paginated: a "Showing N … newest first" affordance
  // and a "Load more" that grows the requested limit.
  it("paginates the Activity feed with a Load more button", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /^activity$/i }, { timeout: 4000 }));
    // First page: the "showing N, newest first" count is present.
    await waitFor(
      () => expect(screen.queryByText(/newest first/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.queryByText(/Showing 50 movements/i)).toBeInTheDocument();
    // A full page → the feed offers "Load more"; clicking it grows the
    // page and re-requests a larger limit (stub caps at 120).
    const loadMore = await screen.findByRole("button", { name: /Load more/i });
    await user.click(loadMore);
    await waitFor(
      () => expect(screen.queryByText(/Showing 100 movements/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
  });

  it("offers a copy-party-id control per row in the party manager", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Parties/i }, { timeout: 4000 }));
    // Each registered party row exposes an accessible "Copy party id …"
    // button that copies the FULL alias::fingerprint id.
    const copyBtn = await screen.findByRole(
      "button",
      { name: /copy party id for treasury/i },
      { timeout: 4000 },
    );
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    await user.click(copyBtn);
    expect(writeText).toHaveBeenCalledWith("bob::def");
    // Feedback flips the label to "Copied!".
    await waitFor(
      () => expect(screen.queryAllByText(/Copied!/i).length).toBeGreaterThan(0),
      { timeout: 2000 },
    );
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

  it("filters the matrix to one token's column via the chips", async () => {
    const user = userEvent.setup();
    stubFetch([
      { symbol: "RTK", name: "Retail Token" },
      { symbol: "GOV", name: "Gov Token" },
    ]);
    renderTokens();
    await user.click(await screen.findByRole("button", { name: /Holdings matrix/i }, { timeout: 4000 }));
    // the All-tokens + per-token chips render
    await waitFor(
      () => expect(screen.getByRole("button", { name: "All tokens" })).toBeInTheDocument(),
      { timeout: 4000 },
    );
    // filter to GOV → its cell shows, RTK's column drops out
    await user.click(screen.getByRole("button", { name: "GOV" }));
    await waitFor(
      () => expect(screen.queryAllByText("42.0").length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    expect(screen.queryByText("1275.0")).not.toBeInTheDocument();
    // All tokens → the full grid returns (RTK's 1275.0 shows in both its
    // cell and the Σ total row, hence queryAllByText)
    await user.click(screen.getByRole("button", { name: "All tokens" }));
    await waitFor(
      () => expect(screen.queryAllByText("1275.0").length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    expect(screen.queryAllByText("42.0").length).toBeGreaterThan(0);
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

  // When the holdings endpoint reports source="registry" (no live
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

  // Selecting "app-provider" must re-plumb the role: the screen refetches
  // with role=app-provider; before the switch, calls omit role (app-user default).
  it("threads the selected identity through token API calls", async () => {
    const user = userEvent.setup();
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();

    const fetchMock = global.fetch as unknown as ReturnType<typeof vi.fn>;
    const calledWith = (needle: string) =>
      fetchMock.mock.calls.some(([u]) => String(u).includes(needle));

    // Instruments load under the default identity first.
    await waitFor(
      () => expect(screen.queryAllByText(/Retail Token/).length).toBeGreaterThan(0),
      { timeout: 4000 },
    );
    // Default calls carry no explicit role (app-user is the omitted default).
    expect(calledWith("role=app-provider")).toBe(false);

    // Switch identity via the header segmented control.
    await user.click(
      await screen.findByRole("button", { name: /app-provider/i }, { timeout: 4000 }),
    );

    // The switch triggers a refetch of the token list under the new role.
    await waitFor(
      () => expect(calledWith("/api/tokens?instance=demo&role=app-provider")).toBe(true),
      { timeout: 4000 },
    );
  });

  it("populates the switcher from GET /api/tokens/identity", async () => {
    stubFetch([{ symbol: "RTK", name: "Retail Token" }]);
    renderTokens();
    // available_roles from the identity endpoint render as switcher buttons.
    await waitFor(
      () => expect(screen.getByRole("button", { name: /app-provider/i })).toBeInTheDocument(),
      { timeout: 4000 },
    );
    expect(screen.getByRole("button", { name: /^sv$/i })).toBeInTheDocument();
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

// createErrorText: the on-ledger create 412 (TEST_TOKEN_DAR_UNAVAILABLE)
// must surface the actionable token-standard-v2 remedy in the create
// modal, not the raw backend message — matching the NEEDS_V2_LOCALNET
// remediation pattern.
describe("createErrorText", () => {
  it("maps the on-ledger DAR 412 to the token-standard-v2 remedy", () => {
    const e = new ApiError(412, {
      code: "TEST_TOKEN_DAR_UNAVAILABLE",
      error: "test-token DAR not available for this Splice version",
    });
    expect(createErrorText(e)).toMatch(/token-standard-v2/);
  });
  it("passes the raw server message through for other API errors", () => {
    const e = new ApiError(409, { code: "SYMBOL_IN_USE", error: "symbol RTK already in use" });
    expect(createErrorText(e)).toBe("symbol RTK already in use");
  });
  it("falls back to a generic message for a non-API error", () => {
    expect(createErrorText(new Error("boom"))).toBe("create failed");
  });
});
