import { useEffect, useReducer, useRef } from "react";
import {
  STEP_ORDER,
  type CreateProgressEvent,
  type StepName,
} from "../api";

// useCreateProgress is the state hook CreateLocalNetModal uses while
// an up is in-flight. It subscribes to the per-instance SSE topic at
// `/api/instances/{name}/events`, dispatches each typed event into a
// reducer, and exposes a snapshot the UI renders.
//
// useReducer (not multiple useStates) keeps the SSE wiring thin and
// lets the step-transition logic be tested as a pure function.

export type StepStatus = "pending" | "active" | "done" | "fail";

export interface StepState {
  status: StepStatus;
  detail?: string;
  percent?: number;
  // failure detail surfaces in the failure banner; per-step we
  // only carry summary for the matching step row.
  summary?: string;
}

export type BannerState =
  | { kind: "running" }
  | { kind: "done"; detail?: string }
  | { kind: "failed"; step?: StepName; summary?: string; cause?: string; errorCode?: import("../api").ErrorCode }
  | { kind: "cancelled"; reason?: string };

export interface ProgressState {
  perStep: Record<StepName, StepState>;
  terminal: TerminalLine[];
  banner: BannerState;
  warnings: string[];
  // null until the first event arrives; the modal uses this to
  // render "connecting…" before any step shows up.
  startedAt: number | null;
}

export interface TerminalLine {
  stream: "stdout" | "stderr";
  text: string;
}

const INITIAL_PER_STEP: Record<StepName, StepState> = STEP_ORDER.reduce(
  (acc, name) => {
    acc[name] = { status: "pending" };
    return acc;
  },
  {} as Record<StepName, StepState>,
);

function freshState(): ProgressState {
  // New perStep / terminal / warnings every time so a reset never
  // shares mutable arrays/maps with a prior create, and so React
  // always sees a new state object when eventsUrl changes.
  return {
    perStep: { ...INITIAL_PER_STEP },
    terminal: [],
    banner: { kind: "running" },
    warnings: [],
    startedAt: null,
  };
}

export const INITIAL_STATE: ProgressState = freshState();

// ProgressAction is the reducer input: SSE events plus an internal
// reset used when the modal switches to a new instance stream.
export type ProgressAction = CreateProgressEvent | { kind: "reset" };

// reducer is exported so tests can drive event sequences without any
// React or DOM machinery. SSE event kinds ARE actions; "reset" clears
// state between successive creates.
export function reducer(state: ProgressState, ev: ProgressAction): ProgressState {
  if (ev.kind === "reset") {
    return freshState();
  }
  // The first event stamps startedAt — this drives the modal's
  // "elapsed" counter without a mount-time timer that would be off by
  // the SSE connect latency.
  const startedAt = state.startedAt ?? Date.now();

  switch (ev.kind) {
    case "step.started":
      return {
        ...state,
        startedAt,
        perStep: {
          ...state.perStep,
          [ev.step]: { status: "active", detail: ev.detail },
        },
      };
    case "step.progress":
      return {
        ...state,
        startedAt,
        perStep: {
          ...state.perStep,
          [ev.step]: {
            ...state.perStep[ev.step],
            status: "active",
            detail: ev.detail,
            percent: ev.percent,
          },
        },
      };
    case "step.finished":
      return {
        ...state,
        startedAt,
        perStep: {
          ...state.perStep,
          [ev.step]: { status: "done", detail: ev.detail },
        },
      };
    case "step.failed":
      return {
        ...state,
        startedAt,
        perStep: {
          ...state.perStep,
          [ev.step]: {
            status: "fail",
            summary: ev.summary,
          },
        },
        // A cancelled banner is sticky — it wins over step.failed.
        banner:
          state.banner.kind === "cancelled"
            ? state.banner
            : {
                kind: "failed",
                step: ev.step,
                summary: ev.summary,
                cause: ev.cause,
                errorCode: ev.error_code,
              },
      };
    case "warn":
      return {
        ...state,
        startedAt,
        warnings: [...state.warnings, ev.message],
      };
    case "done":
      return {
        ...state,
        startedAt,
        banner: { kind: "done", detail: ev.detail },
      };
    case "output":
      return {
        ...state,
        startedAt,
        terminal: [...state.terminal, { stream: ev.stream, text: ev.text }],
      };
    case "cancelled":
      // Cancelled is the user-initiated marker; it wins over the
      // subsequent step.failed the interrupted run emits.
      return {
        ...state,
        startedAt,
        banner: { kind: "cancelled", reason: ev.reason },
      };
    default:
      // Forward-compat: an unknown kind from a newer server doesn't
      // crash the reducer. Logged for diagnosis.
      // eslint-disable-next-line no-console
      console.warn("useCreateProgress: unknown event kind", ev);
      return state;
  }
}

// parseEventId turns the SSE frame's `id:` value (MessageEvent.
// lastEventId) into the monotonic per-stream sequence number the
// server assigns. Returns null for a missing or non-numeric id, in
// which case the event is always applied (nothing to compare against).
export function parseEventId(lastEventId: string | undefined): number | null {
  if (!lastEventId) return null;
  const n = Number(lastEventId);
  return Number.isInteger(n) ? n : null;
}

// useCreateProgress opens an EventSource on the given URL, parses
// each message as a typed CreateProgressEvent, and feeds the reducer.
// Returns INITIAL_STATE while eventsUrl is null — the modal uses that
// to render the "submitting…" footer between POST start and the 202
// response. EventSource.close() runs on unmount AND on eventsUrl
// change so a successive create cleanly tears down the old stream.
// State is reset on every eventsUrl change so a second create does
// not inherit the prior instance's terminal lines / step progress.
export function useCreateProgress(eventsUrl: string | null): ProgressState {
  const [state, dispatch] = useReducer(reducer, INITIAL_STATE);

  // Highest server sequence id applied so far on the current stream.
  // EventSource auto-reconnects after a transient drop, and the
  // per-instance handler replays the WHOLE ring buffer on every
  // (re)connection — it does not honor Last-Event-ID server-side.
  // Without this guard, append-only events (`output`, `warn`) would
  // duplicate on every reconnect. Server ids are monotonic per
  // stream, so dropping any frame whose id is <= the highest already
  // applied makes the replay idempotent.
  const lastEventIdRef = useRef<number | null>(null);

  useEffect(() => {
    // Always clear prior create state first — including when eventsUrl
    // becomes null (modal back on the form) or switches instance.
    dispatch({ kind: "reset" });
    lastEventIdRef.current = null;
    if (!eventsUrl) return;
    const es = new EventSource(eventsUrl);
    es.onmessage = (msg) => {
      const id = parseEventId(msg.lastEventId);
      if (id !== null) {
        if (lastEventIdRef.current !== null && id <= lastEventIdRef.current) {
          // Replayed frame after a reconnect — already applied.
          return;
        }
        lastEventIdRef.current = id;
      }
      try {
        const ev = JSON.parse(msg.data) as CreateProgressEvent;
        dispatch(ev);
      } catch {
        // Malformed events shouldn't crash the modal — warn and drop.
        // eslint-disable-next-line no-console
        console.warn("useCreateProgress: bad event payload", msg.data);
      }
    };
    es.onerror = () => {
      // EventSource auto-reconnects on transient errors. On permanent
      // failure the browser stops retrying and the user keeps the
      // last good state in the modal.
    };
    return () => es.close();
  }, [eventsUrl]);

  return state;
}
