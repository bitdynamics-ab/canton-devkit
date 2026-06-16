import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TxReplayDrawer } from "./TxReplayDrawer";

// TxReplayDrawer — the per-party visibility projection.
//
// Renders the LEDGER_EFFECTS event tree of one transaction projected
// through a party set, and lets the user re-project through a
// different party. Backed by GET .../transactions/{id}/replay.

function replayResponse(events: unknown[]) {
  return {
    schema_version: 1,
    instance: "demo",
    parties: ["alice::1"],
    update_id: "tx-42",
    offset: 42,
    workflow_id: "wf-1",
    event_count: events.length,
    events,
  };
}

function stubReplay(body: unknown, status = 200) {
  const fn = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("TxReplayDrawer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders the projected event tree (created + exercised)", async () => {
    stubReplay(
      replayResponse([
        {
          kind: "created",
          node_id: 0,
          contract_id: "c-created-aaaa",
          template_id: "pkg:Token:Holding",
          signatories: ["alice::1"],
        },
        {
          kind: "exercised",
          node_id: 1,
          contract_id: "c-exercised-bbbb",
          template_id: "pkg:Token:Holding",
          choice: "Transfer",
          consuming: true,
          acting_parties: ["alice::1"],
        },
      ]),
    );
    render(
      <TxReplayDrawer
        instance="demo"
        role="app-user"
        updateId="tx-42"
        partyOptions={["alice::1", "bob::2"]}
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(screen.getByText("created")).toBeInTheDocument();
    });
    expect(screen.getByText("exercised")).toBeInTheDocument();
    // The exercised choice + consuming flag render in the detail.
    expect(screen.getByText(/Transfer \(consuming\)/)).toBeInTheDocument();
    // The event count summary reflects 2 visible events.
    expect(screen.getByText(/2 events visible/)).toBeInTheDocument();
  });

  it("re-fetches with the chosen party when the selector changes", async () => {
    const fn = stubReplay(replayResponse([]));
    render(
      <TxReplayDrawer
        instance="demo"
        role="app-user"
        updateId="tx-42"
        partyOptions={["alice::1", "bob::2"]}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(fn).toHaveBeenCalled());
    // Initial call uses the JWT default (no party param).
    expect(fn.mock.calls[0][0]).not.toContain("party=");

    await userEvent.selectOptions(
      screen.getByLabelText("Project replay through party"),
      "bob::2",
    );
    await waitFor(() => {
      const last = fn.mock.calls[fn.mock.calls.length - 1][0] as string;
      expect(last).toContain("party=bob");
    });
  });

  it("shows a not-visible message on 404", async () => {
    stubReplay(
      { code: "NOT_FOUND", error: "transaction not visible to this party set" },
      404,
    );
    render(
      <TxReplayDrawer
        instance="demo"
        role="app-user"
        updateId="missing"
        partyOptions={[]}
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(screen.getByText(/not visible to the selected party/i)).toBeInTheDocument();
    });
  });

  it("invokes onClose on Escape", async () => {
    stubReplay(replayResponse([]));
    const onClose = vi.fn();
    render(
      <TxReplayDrawer
        instance="demo"
        role="app-user"
        updateId="tx-42"
        partyOptions={[]}
        onClose={onClose}
      />,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});
