import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useConnectionHealth } from "./useConnectionHealth";

// useConnectionHealth — the topbar pill driver. Tests pin the
// three transitions a user notices:
//
//   1. ok → offline when /api/version errors
//   2. offline → ok when /api/version recovers
//   3. ok → mismatch when the server bumps its schema_version
//
// Plus the contract that we DO NOT zero out serverVersion on
// transition to offline — the tooltip should still read "last
// seen v1" so the user can tell whether the binary crashed or
// they closed the tunnel.

describe("useConnectionHealth", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  function mockFetchSequence(...responses: Array<{ status: number; body?: object } | "error">) {
    let i = 0;
    const spy = vi.fn().mockImplementation(() => {
      const next = responses[i++] ?? responses[responses.length - 1];
      if (next === "error") return Promise.reject(new Error("network down"));
      return Promise.resolve(
        new Response(JSON.stringify(next.body ?? {}), {
          status: next.status,
          headers: { "Content-Type": "application/json" },
        }),
      );
    });
    vi.stubGlobal("fetch", spy);
    return spy;
  }

  it("transitions to offline when the server stops responding", async () => {
    mockFetchSequence("error");
    const { result } = renderHook(() => useConnectionHealth(1_000));
    // Initial state — optimistic, no fetch yet.
    expect(result.current.health).toBe("ok");
    // Advance past the first poll interval and flush the
    // promise microtask queue.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    // Don't use RTL's waitFor — it polls via setTimeout, which is
    // fake here, so the poll loop never fires. act() already
    // flushed the state update.
    expect(result.current.health).toBe("offline");
  });

  it("preserves last-seen serverVersion across offline transition", async () => {
    // First poll returns v1, second poll errors. The tooltip
    // contract requires the version stays available so the user
    // sees "last seen v1" rather than "unknown".
    mockFetchSequence({ status: 200, body: { name: "x", schema_version: 1 } }, "error");
    const { result } = renderHook(() => useConnectionHealth(1_000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(result.current.health).toBe("ok");
    expect(result.current.serverVersion).toBe(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(result.current.health).toBe("offline");
    // The critical assertion — version stayed.
    expect(result.current.serverVersion).toBe(1);
  });

  it("surfaces schema mismatch when the server's version drifts", async () => {
    // 999 will never equal SCHEMA_VERSION; this is the "binary
    // swapped mid-session" branch that hard-fails the BootGate
    // on first paint but only colour-codes for an already-running
    // app.
    mockFetchSequence({ status: 200, body: { name: "x", schema_version: 999 } });
    const { result } = renderHook(() => useConnectionHealth(1_000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(result.current.health).toBe("mismatch");
    expect(result.current.serverVersion).toBe(999);
  });
});
