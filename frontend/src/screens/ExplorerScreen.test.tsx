import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { ExplorerScreen } from "./ExplorerScreen";

// ExplorerScreen — transaction filters (#24) + tx replay (#20).
//
// We drive the real screen with a routed fetch stub. The contracts
// snapshot resolves so the Contracts/Transactions/Timeline toggle is
// present; switching to Transactions exercises the filter bar (which
// re-fetches with the query params) and the per-row Replay button
// (which opens the TxReplayDrawer).

function Providers({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter initialEntries={["/?instance=demo"]}>
      <InstanceSelectionProvider>{children}</InstanceSelectionProvider>
    </MemoryRouter>
  );
}

const TX_ROWS = [
  {
    kind: "transaction",
    offset: 30,
    update_id: "tx-30",
    command_id: "cmd-30",
    workflow_id: "wf-30",
    record_time: "2026-06-08T10:00:00Z",
    event_count: 1,
    events: [
      { kind: "create", contract_id: "c30", template: "pkg:Token:Holding", witnesses: ["alice::1"] },
    ],
  },
];

// urlsSeen records every transactions-list request so a test can
// assert the filter query params the screen built.
const urlsSeen: string[] = [];

function stubFetch() {
  urlsSeen.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((rawUrl: string) => {
      const url = typeof rawUrl === "string" ? rawUrl : (rawUrl as URL).toString();
      const json = (body: unknown, status = 200) =>
        Promise.resolve(
          new Response(JSON.stringify(body), {
            status,
            headers: { "Content-Type": "application/json" },
          }),
        );
      if (url === "/api/instances" || url.startsWith("/api/instances?")) {
        return json({
          schema_version: 1,
          instances: [{ name: "demo", status: "running", splice_version: "0.6.4", ports: "", started_ago: "1m" }],
        });
      }
      // Replay endpoint — matched before the generic transactions list.
      if (url.includes("/transactions/") && url.includes("/replay")) {
        return json({
          schema_version: 1,
          instance: "demo",
          update_id: "tx-30",
          offset: 30,
          event_count: 1,
          events: [
            { kind: "created", node_id: 0, contract_id: "c30", template_id: "pkg:Token:Holding", signatories: ["alice::1"] },
          ],
        });
      }
      if (url.includes("/transactions")) {
        urlsSeen.push(url);
        return json({
          schema_version: 1,
          instance: "demo",
          role: "app-user",
          ledger_end: 30,
          count: TX_ROWS.length,
          scanned_from: 0,
          window_truncated: false,
          transactions: TX_ROWS,
        });
      }
      if (url.includes("/contracts")) {
        return json({
          schema_version: 1,
          instance: "demo",
          role: "app-user",
          ledger_end: 30,
          contracts: [],
        });
      }
      return json({ code: "NOT_FOUND", error: "not stubbed: " + url }, 404);
    }),
  );
}

async function gotoTransactions() {
  render(
    <Providers>
      <ExplorerScreen />
    </Providers>,
  );
  // Wait for the contracts snapshot to resolve so the view toggle is up.
  await waitFor(() => {
    expect(screen.getByRole("button", { name: "transactions" })).toBeInTheDocument();
  });
  await userEvent.click(screen.getByRole("button", { name: "transactions" }));
  await waitFor(() => {
    expect(screen.getByText("Transactions")).toBeInTheDocument();
  });
}

describe("ExplorerScreen — transactions filters + replay", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("forwards party / template / offset filters to the transactions endpoint (#24)", async () => {
    stubFetch();
    await gotoTransactions();

    await userEvent.type(screen.getByLabelText("party id (comma-sep)"), "alice::1");
    await userEvent.type(screen.getByLabelText("Module:Entity (comma-sep)"), "Token:Holding");
    await userEvent.type(screen.getByLabelText("from offset"), "5");
    await userEvent.type(screen.getByLabelText("to offset"), "40");
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => {
      const filtered = urlsSeen.find(
        (u) => u.includes("party=alice") && u.includes("template=Token") && u.includes("from=5") && u.includes("to=40"),
      );
      expect(filtered, `urls seen: ${urlsSeen.join("\n")}`).toBeTruthy();
    });
  });

  it("opens the replay drawer for a transaction row (#20)", async () => {
    stubFetch();
    await gotoTransactions();

    // The row's Replay button opens the per-party projection drawer.
    await userEvent.click(screen.getByRole("button", { name: "replay" }));

    await waitFor(() => {
      expect(screen.getByText("Replay · per-party projection")).toBeInTheDocument();
    });
    // The drawer fetched + rendered the projected created event. The
    // event-count summary is unique to the replay drawer.
    await waitFor(() => {
      expect(screen.getByText(/1 event visible/)).toBeInTheDocument();
    });
  });

  it("does not present the dead time-range buttons in the ACS sidebar (#25/#78)", async () => {
    stubFetch();
    render(
      <Providers>
        <ExplorerScreen />
      </Providers>,
    );
    await waitFor(() => {
      expect(screen.getByText("Active Contract Set")).toBeInTheDocument();
    });
    // The old dead buttons (Live / 5m / 1h / 24h) must be gone; a
    // manual refresh affordance replaces them.
    expect(screen.queryByRole("button", { name: "5m" })).toBeNull();
    expect(screen.queryByRole("button", { name: "24h" })).toBeNull();
    expect(screen.getByRole("button", { name: "Refresh snapshot" })).toBeInTheDocument();
  });
});
