import { describe, expect, it } from "vitest";
import { INITIAL_STATE, reducer } from "./useCreateProgress";
import type { CreateProgressEvent } from "../api";

// Reducer tests — pure, no React, no DOM. The SSE plumbing is
// thin (EventSource → JSON.parse → dispatch); the interesting
// behaviour is the state-transition logic, which lives here.

describe("useCreateProgress reducer", () => {
  it("starts at the initial state", () => {
    expect(INITIAL_STATE.banner.kind).toBe("running");
    expect(INITIAL_STATE.startedAt).toBeNull();
    expect(INITIAL_STATE.warnings).toHaveLength(0);
    expect(INITIAL_STATE.terminal).toHaveLength(0);
    // Every step starts pending.
    Object.values(INITIAL_STATE.perStep).forEach((s) =>
      expect(s.status).toBe("pending"),
    );
  });

  it("step.started flips a step to active and stamps startedAt", () => {
    const next = reducer(INITIAL_STATE, {
      kind: "step.started",
      step: "preflight",
    });
    expect(next.perStep.preflight.status).toBe("active");
    expect(next.startedAt).not.toBeNull();
  });

  it("step.finished flips to done with detail", () => {
    let state = reducer(INITIAL_STATE, {
      kind: "step.started",
      step: "fetch_splice",
    });
    state = reducer(state, {
      kind: "step.finished",
      step: "fetch_splice",
      detail: "cache hit · 0.4s",
    });
    expect(state.perStep.fetch_splice.status).toBe("done");
    expect(state.perStep.fetch_splice.detail).toBe("cache hit · 0.4s");
  });

  it("step.failed flips to fail AND sets the failed banner", () => {
    const state = reducer(INITIAL_STATE, {
      kind: "step.failed",
      step: "start_services",
      summary: "compose up exited 1",
      cause: "container nginx not healthy",
    });
    expect(state.perStep.start_services.status).toBe("fail");
    expect(state.banner.kind).toBe("failed");
    if (state.banner.kind === "failed") {
      expect(state.banner.step).toBe("start_services");
      expect(state.banner.summary).toBe("compose up exited 1");
      expect(state.banner.cause).toBe("container nginx not healthy");
    }
  });

  it("cancelled wins over a subsequent step.failed (UX ordering)", () => {
    // The DELETE handler publishes cancelled BEFORE the natural
    // failure stream catches up. The banner must reflect the
    // user-initiated cancel, not the generic "interrupted" that
    // RunUp emits when ctx.Err() fires. This pins the reducer's
    // ordering rule that keeps cancelled sticky.
    let state = reducer(INITIAL_STATE, {
      kind: "cancelled",
      reason: "user requested",
    });
    expect(state.banner.kind).toBe("cancelled");
    state = reducer(state, {
      kind: "step.failed",
      step: "start_services",
      summary: "Interrupted while starting services",
    });
    expect(state.banner.kind).toBe("cancelled");
    // Per-step still marks fail — only the banner is sticky.
    expect(state.perStep.start_services.status).toBe("fail");
  });

  it("done sets the success banner", () => {
    const state = reducer(INITIAL_STATE, {
      kind: "done",
      detail: 'Canton LocalNet "demo" is ready.',
    });
    expect(state.banner.kind).toBe("done");
    if (state.banner.kind === "done") {
      expect(state.banner.detail).toContain("ready");
    }
  });

  it("warn appends to the warnings list (no banner change)", () => {
    const state = reducer(INITIAL_STATE, {
      kind: "warn",
      message: "uncurated tag",
    });
    expect(state.warnings).toEqual(["uncurated tag"]);
    expect(state.banner.kind).toBe("running");
  });

  it("output appends terminal lines tagged by stream", () => {
    let state = reducer(INITIAL_STATE, {
      kind: "output",
      stream: "stdout",
      text: "Starting Canton LocalNet …",
    });
    state = reducer(state, {
      kind: "output",
      stream: "stderr",
      text: "warning: dev secret in use",
    });
    expect(state.terminal).toEqual([
      { stream: "stdout", text: "Starting Canton LocalNet …" },
      { stream: "stderr", text: "warning: dev secret in use" },
    ]);
  });

  it("step.progress updates detail + percent on the active step", () => {
    let state = reducer(INITIAL_STATE, {
      kind: "step.started",
      step: "start_services",
    });
    state = reducer(state, {
      kind: "step.progress",
      step: "start_services",
      detail: "11/15 containers up",
      percent: 73,
    });
    const s = state.perStep.start_services;
    expect(s.status).toBe("active");
    expect(s.detail).toBe("11/15 containers up");
    expect(s.percent).toBe(73);
  });

  it("unknown event kind is ignored (forward compat)", () => {
    const before = INITIAL_STATE;
    // Type-assertion to bypass the union check — the runtime
    // case is exactly what we want to test.
    const after = reducer(before, {
      kind: "future.event.we.do.not.know",
    } as unknown as CreateProgressEvent);
    expect(after).toBe(before);
  });

  it("startedAt is set on the FIRST event and never changes", () => {
    const t1 = reducer(INITIAL_STATE, { kind: "warn", message: "first" });
    expect(t1.startedAt).not.toBeNull();
    const firstStamp = t1.startedAt!;
    // wait a tick by manipulating Date.now indirectly — even
    // identical times the reducer should preserve the first.
    const t2 = reducer(t1, { kind: "warn", message: "second" });
    expect(t2.startedAt).toBe(firstStamp);
  });
});
