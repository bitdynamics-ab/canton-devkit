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
      () => expect(screen.queryByText(/No instruments recorded yet/i)).toBeInTheDocument(),
      { timeout: 4000 },
    );
  });

  it("validates the symbol client-side in the create modal", async () => {
    stubFetch([]);
    renderTokens();
    const openBtn = await screen.findByRole("button", { name: /create token/i });
    await userEvent.click(openBtn);

    const symbol = await screen.findByLabelText("Symbol");
    await userEvent.type(symbol, "bad symbol!"); // space + punctuation → invalid
    expect(screen.getByText(/1–16 chars/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();

    await userEvent.clear(symbol);
    await userEvent.type(symbol, "RTK");
    expect(screen.queryByText(/1–16 chars/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).not.toBeDisabled();
  });

  it("surfaces a mint failure exactly once, inline in the modal", async () => {
    // Token list loads; the mint POST fails. The fix removed the
    // top-of-screen notice so the message renders only inside the modal.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        const json = (b: unknown, s = 200) =>
          Promise.resolve(new Response(JSON.stringify(b), { status: s, headers: { "Content-Type": "application/json" } }));
        if (url.startsWith("/api/version")) return json({ name: "canton-devkit", schema_version: 1 });
        if (url.startsWith("/api/instances")) return json({ schema_version: 1, instances: [{ name: "demo", status: "running" }] });
        if (url.includes("/holdings")) return json({ schema_version: 1, holdings: [] });
        if (init?.method === "POST" && url.includes("/mint")) {
          return json({ code: "INVALID_REQUEST", error: "amount must be greater than zero" }, 400);
        }
        if (url.startsWith("/api/tokens")) {
          return json({
            schema_version: 1,
            tokens: [{ name: "Retail Token", symbol: "RTK", decimals: 6, initial_supply: "1000000", issuer_party: "alice::abc", instrument_id: "abcd1234567890", created_at: "2026-05-25T10:00:00Z", status: "recorded" }],
          });
        }
        return Promise.resolve(new Response(null, { status: 204 }));
      }),
    );
    renderTokens();
    await userEvent.click(await screen.findByRole("button", { name: /Mint/ }));
    // Fill the two fields and submit.
    const inputs = screen.getAllByRole("textbox");
    await userEvent.type(inputs[0], "bob");
    await userEvent.type(inputs[1], "0");
    await userEvent.click(screen.getByRole("button", { name: /Submit/ }));

    await waitFor(() =>
      expect(screen.getAllByText(/amount must be greater than zero/).length).toBe(1),
    );
  });
});
