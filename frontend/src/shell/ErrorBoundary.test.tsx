import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ErrorBoundary } from "./ErrorBoundary";

// ErrorBoundary contract:
//
//   1. children that throw on render produce the fallback,
//      not a white screen
//   2. fallback shows the error message + a Retry button
//   3. Retry forces a clean remount so a fixed child renders
//   4. Changing routeKey clears the error (covers the
//      "navigate away from a crashed screen" UX)
//   5. siblings outside the boundary stay rendered (parent UI
//      survives a child crash)

function Bomb({ explode }: { explode: boolean }) {
  if (explode) {
    throw new Error("boom from Bomb");
  }
  return <div>bomb is calm</div>;
}

describe("ErrorBoundary", () => {
  // Suppress the noisy console.error React produces for caught
  // errors — without this, every test prints its own stack trace
  // and makes the test output unreadable.
  let consoleError: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => {
    consoleError.mockRestore();
  });

  it("renders children when no error is thrown", () => {
    render(
      <ErrorBoundary>
        <Bomb explode={false} />
      </ErrorBoundary>,
    );
    expect(screen.getByText("bomb is calm")).toBeInTheDocument();
  });

  it("renders the fallback when a child throws", () => {
    render(
      <ErrorBoundary>
        <Bomb explode={true} />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(/boom from Bomb/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });

  it("logs the error so the dev-tools console captures the stack", () => {
    render(
      <ErrorBoundary>
        <Bomb explode={true} />
      </ErrorBoundary>,
    );
    // React itself logs the error too; we just check OUR explicit
    // console.error fired at least once with the right prefix.
    const matched = consoleError.mock.calls.some(
      (c) =>
        typeof c[0] === "string" &&
        c[0].includes("ErrorBoundary caught a render error"),
    );
    expect(matched).toBe(true);
  });

  it("Retry clears the error and re-renders the children (now passing)", async () => {
    // The "fixed" path: parent flips explode=false before user
    // clicks Retry. Without the resetCount-driven remount, a
    // stale closure could re-throw immediately.
    function Harness() {
      const [explode, setExplode] = useState(true);
      return (
        <>
          <button onClick={() => setExplode(false)}>fix it</button>
          <ErrorBoundary>
            <Bomb explode={explode} />
          </ErrorBoundary>
        </>
      );
    }
    render(<Harness />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    // First fix the underlying state, then click Retry.
    await userEvent.click(screen.getByText("fix it"));
    await userEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(screen.getByText("bomb is calm")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("clears the error when routeKey changes (navigated away)", () => {
    function Harness({ rk, explode }: { rk: string; explode: boolean }) {
      return (
        <ErrorBoundary routeKey={rk}>
          <Bomb explode={explode} />
        </ErrorBoundary>
      );
    }
    const { rerender } = render(<Harness rk="/a" explode={true} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    // Simulate route change to a screen that doesn't crash.
    rerender(<Harness rk="/b" explode={false} />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("bomb is calm")).toBeInTheDocument();
  });

  it("does NOT swallow errors from siblings outside the boundary", () => {
    // Pin: siblings outside the boundary keep rendering normally
    // when the boundary catches an inner error.
    render(
      <>
        <div>sibling-stays-rendered</div>
        <ErrorBoundary>
          <Bomb explode={true} />
        </ErrorBoundary>
      </>,
    );
    expect(screen.getByText("sibling-stays-rendered")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });
});
