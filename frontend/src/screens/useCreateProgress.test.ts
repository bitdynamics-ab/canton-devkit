import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  INITIAL_STATE,
  parseEventId,
  reducer,
  useCreateProgress,
} from "./useCreateProgress";
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

describe("parseEventId", () => {
  it("parses a numeric SSE id", () => {
    expect(parseEventId("7")).toBe(7);
  });
  it("returns null for a missing id (apply unconditionally)", () => {
    expect(parseEventId(undefined)).toBeNull();
    expect(parseEventId("")).toBeNull();
  });
  it("returns null for a non-numeric id", () => {
    expect(parseEventId("abc")).toBeNull();
    expect(parseEventId("1.5")).toBeNull();
  });
});

// FakeEventSource lets a test drive onmessage with controlled
// `lastEventId` values, including a simulated reconnect where the
// server replays the whole ring buffer (the real per-instance
// handler does SubscribeWithReplay and ignores Last-Event-ID).
class FakeEventSource {
  static last: FakeEventSource | null = null;
  url: string;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  closed = false;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.last = this;
  }
  close() {
    this.closed = true;
  }
  // Helper: deliver one frame with a server sequence id.
  emit(id: number, payload: CreateProgressEvent) {
    this.onmessage?.({
      data: JSON.stringify(payload),
      lastEventId: String(id),
    } as MessageEvent);
  }
}

describe("useCreateProgress SSE dedupe on reconnect", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("drops replayed events after a reconnect so output/warn don't duplicate", () => {
    vi.stubGlobal(
      "EventSource",
      FakeEventSource as unknown as typeof EventSource,
    );
    const { result } = renderHook(() =>
      useCreateProgress("/api/instances/dev/events"),
    );
    const es = FakeEventSource.last!;

    // First connection: ids 1..3 arrive.
    act(() => {
      es.emit(1, { kind: "output", stream: "stdout", text: "line A" });
      es.emit(2, { kind: "warn", message: "uncurated tag" });
      es.emit(3, { kind: "output", stream: "stdout", text: "line B" });
    });
    expect(result.current.terminal).toEqual([
      { stream: "stdout", text: "line A" },
      { stream: "stdout", text: "line B" },
    ]);
    expect(result.current.warnings).toEqual(["uncurated tag"]);

    // Transient drop → EventSource reconnects and the handler
    // replays the WHOLE buffer (ids 1..3 again), then continues
    // with a fresh id 4. The replayed 1..3 must be ignored.
    act(() => {
      es.emit(1, { kind: "output", stream: "stdout", text: "line A" });
      es.emit(2, { kind: "warn", message: "uncurated tag" });
      es.emit(3, { kind: "output", stream: "stdout", text: "line B" });
      es.emit(4, { kind: "output", stream: "stdout", text: "line C" });
    });

    // No duplicates: only the genuinely-new id 4 was appended.
    expect(result.current.terminal).toEqual([
      { stream: "stdout", text: "line A" },
      { stream: "stdout", text: "line B" },
      { stream: "stdout", text: "line C" },
    ]);
    expect(result.current.warnings).toEqual(["uncurated tag"]);
  });

  it("applies every event when frames carry no id (no dedupe possible)", () => {
    vi.stubGlobal(
      "EventSource",
      FakeEventSource as unknown as typeof EventSource,
    );
    const { result } = renderHook(() =>
      useCreateProgress("/api/instances/dev/events"),
    );
    const es = FakeEventSource.last!;
    act(() => {
      // lastEventId === "" → parseEventId returns null → applied.
      es.onmessage?.({
        data: JSON.stringify({ kind: "warn", message: "one" }),
        lastEventId: "",
      } as MessageEvent);
      es.onmessage?.({
        data: JSON.stringify({ kind: "warn", message: "two" }),
        lastEventId: "",
      } as MessageEvent);
    });
    expect(result.current.warnings).toEqual(["one", "two"]);
  });
});
