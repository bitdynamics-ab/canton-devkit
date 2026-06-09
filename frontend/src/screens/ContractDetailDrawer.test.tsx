import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ContractDetailDrawer } from "./ContractDetailDrawer";
import type { ContractRow } from "../api";

// ContractDetailDrawer
//
// The drawer renders synchronously from the ACS row props while
// the deep-view fetch is in flight, then re-renders with the
// /api/.../contracts/{cid} payload (signatories, observers,
// payload tree, archive metadata). Keyboard navigation (Esc /
// J / K) invokes the parent's onClose / onPrev / onNext.

const baseRow: ContractRow = {
  contract_id: "00abcd1234ef:0",
  template_id: "pkg:Module:Entity",
  payload: { amount: 100, owner: "alice::1234" },
  signatories: ["alice::1234"],
  observers: ["bob::5678"],
  created_at: "2026-06-08T10:00:00Z",
  package_name: "demo-pkg",
};

function mockDetail(detail: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          schema_version: 1,
          instance: "demo",
          role: "app-user",
          contract: detail,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
}

describe("ContractDetailDrawer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders row-level data immediately while deep-view loads", async () => {
    mockDetail({
      contract_id: baseRow.contract_id,
      template_id: baseRow.template_id,
      payload: { amount: 100 },
      signatories: ["alice::1234"],
      observers: ["bob::5678"],
      archived: false,
    });
    render(
      <ContractDetailDrawer
        instance="demo"
        role="app-user"
        row={baseRow}
        onClose={() => {}}
      />,
    );
    // Row-level fields are visible synchronously
    expect(screen.getByText("Module:Entity")).toBeInTheDocument();
    expect(screen.getByText(/00abcd1234ef:0/)).toBeInTheDocument();
    // Deep-view fetch lands → archived pill still says "active"
    await waitFor(() => {
      expect(screen.getByText("active")).toBeInTheDocument();
    });
  });

  it("shows archived pill when the deep view marks the contract archived", async () => {
    mockDetail({
      contract_id: baseRow.contract_id,
      template_id: baseRow.template_id,
      signatories: ["alice::1234"],
      observers: [],
      archived: true,
      archived_offset: 12345,
    });
    render(
      <ContractDetailDrawer
        instance="demo"
        role="app-user"
        row={baseRow}
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(screen.getByText("archived")).toBeInTheDocument();
    });
  });

  it("invokes onClose when Esc is pressed", async () => {
    mockDetail({
      contract_id: baseRow.contract_id,
      signatories: [],
      observers: [],
      archived: false,
    });
    const onClose = vi.fn();
    render(
      <ContractDetailDrawer
        instance="demo"
        role="app-user"
        row={baseRow}
        onClose={onClose}
      />,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onNext on J and onPrev on K", async () => {
    mockDetail({
      contract_id: baseRow.contract_id,
      signatories: [],
      observers: [],
      archived: false,
    });
    const onNext = vi.fn();
    const onPrev = vi.fn();
    render(
      <ContractDetailDrawer
        instance="demo"
        role="app-user"
        row={baseRow}
        onClose={() => {}}
        onNext={onNext}
        onPrev={onPrev}
      />,
    );
    await userEvent.keyboard("j");
    await userEvent.keyboard("k");
    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onPrev).toHaveBeenCalledTimes(1);
  });

  it("renders error message when the deep-view request fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: "NOT_FOUND",
            error: "contract not visible to this party set",
          }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    render(
      <ContractDetailDrawer
        instance="demo"
        role="app-user"
        row={baseRow}
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(
        screen.getByText(/contract not visible/i),
      ).toBeInTheDocument();
    });
  });
});
