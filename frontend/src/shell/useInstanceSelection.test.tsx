import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import {
  InstanceSelectionProvider,
  useInstanceSelection,
} from "./useInstanceSelection";
import type { ReactNode } from "react";

// useInstanceSelection — auto-pick rules + URL authority.
//
// The auto-pick rule is the one piece of derived logic worth
// testing: URL value wins if it maps to a known instance,
// otherwise first running, otherwise null. Pre-lift this lived
// in Dashboard local state; now it serves every screen, so
// regressions become cross-cutting.

function wrap(initialEntries: string[] = ["/"]): React.FC<{ children: ReactNode }> {
  return function Wrapper({ children }) {
    return (
      <MemoryRouter initialEntries={initialEntries}>
        <InstanceSelectionProvider>{children}</InstanceSelectionProvider>
      </MemoryRouter>
    );
  };
}

function mockInstances(instances: Array<{ name: string; status: string }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            schema_version: 1,
            instances: instances.map((i) => ({
              name: i.name,
              status: i.status,
              splice_version: "0.4.12",
              ports: "",
              started_ago: "",
            })),
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    ),
  );
}

describe("useInstanceSelection auto-pick", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("prefers the URL pick when it maps to a known instance", async () => {
    mockInstances([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/?instance=hubble"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    // URL says hubble even though it's stopped — URL wins.
    expect(result.current.selected).toBe("hubble");
  });

  it("falls back to the first running instance when URL pick is unknown", async () => {
    mockInstances([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/?instance=ghost-instance"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.selected).toBe("demo");
  });

  it("falls back to first running when URL has no pick at all", async () => {
    mockInstances([
      { name: "hubble", status: "stopped" },
      { name: "demo", status: "running" },
    ]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.selected).toBe("demo");
  });

  it("returns null when no instance is running and no URL pick is set", async () => {
    mockInstances([{ name: "hubble", status: "stopped" }]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.selected).toBeNull();
  });

  it("returns null when the registry is empty", async () => {
    mockInstances([]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/?instance=demo"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.selected).toBeNull();
    expect(result.current.instances).toHaveLength(0);
  });

  it("select() updates the URL so the pick survives a hard reload", async () => {
    mockInstances([
      { name: "demo", status: "running" },
      { name: "hubble", status: "running" },
    ]);
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/"]),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.selected).toBe("demo");
    act(() => result.current.select("hubble"));
    // The hook's derived `selected` reflects the new URL pick on
    // the next render — this is the "URL is authoritative" contract.
    expect(result.current.selected).toBe("hubble");
  });

  it("re-polls every 15s so externally-killed containers update the dashboard", async () => {
    // Repro for the user-reported bug: stop containers via Docker
    // Desktop → registry status flips (backend reconciler) but the
    // Dashboard was rendering the mount-time snapshot forever.
    // After the fix, the hook polls /api/instances on a 15s timer
    // and the next snapshot replaces the stale view.
    vi.useFakeTimers();
    let callCount = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(() => {
        callCount++;
        const status = callCount === 1 ? "running" : "stopped";
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instances: [
                {
                  name: "demo",
                  status,
                  splice_version: "0.6.4",
                  ports: "",
                  started_ago: "",
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }),
    );
    const { result } = renderHook(() => useInstanceSelection(), {
      wrapper: wrap(["/"]),
    });
    // First fetch: status=running
    await vi.waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.instances[0]?.status).toBe("running");
    // Advance one tick → second fetch fires with status=stopped
    await act(async () => {
      vi.advanceTimersByTime(15_000);
    });
    await vi.waitFor(() =>
      expect(result.current.instances[0]?.status).toBe("stopped"),
    );
    expect(callCount).toBeGreaterThanOrEqual(2);
    vi.useRealTimers();
  });

  it("throws if used outside the Provider — guards against silent bugs", () => {
    // Suppress React's error-boundary log; we expect the throw.
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => renderHook(() => useInstanceSelection())).toThrow(
      /InstanceSelectionProvider/,
    );
    spy.mockRestore();
  });
});
