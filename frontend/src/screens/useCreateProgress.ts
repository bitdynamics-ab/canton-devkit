import { useEffect, useReducer, useRef } from "react";
import {
  STEP_ORDER,
  type CreateProgressEvent,
  type StepName,
} from "../api";

// useCreateProgress is the state hook the CreateLocalNetModal
// uses while an up is in-flight. It subscribes to the per-instance
// SSE topic at `/api/instances/{name}/events` (via EventSource),
// dispatches each typed event into a reducer, and exposes a
// snapshot the UI renders.
//
// State shape mirrors what the mockup's progress modal needs:
//
//   - perStep: status + detail + percent for each of the 8 steps
//   - terminal: appended chunks from kind=output events
//   - banner: top-level state (running / cancelled / done / failed)
//   - elapsedMs: derived from `start` on first event
//
// We use useReducer (not multiple useStates) so the reducer can
// be tested in isolation as a pure function — the SSE wiring is
// kept thin, and assertions about "the right step transitions"
// don't need a full React render.

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

export const INITIAL_STATE: ProgressState = {
  perStep: INITIAL_PER_STEP,
  terminal: [],
  banner: { kind: "running" },
  warnings: [],
  startedAt: null,
};

// reducer is exported so tests can drive event sequences without
// any React or DOM machinery. The action type IS the event union
// (no wrapping) — events ARE actions.
export function reducer(state: ProgressState, ev: CreateProgressEvent): ProgressState {
  // First event also stamps startedAt — useful for the modal's
  // "elapsed" counter without a separate timer firing on mount
  // (which would be off by the SSE connect latency).
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
        // banner stays "failed" unless a later event upgrades it
        // (cancelled wins — see ordering note on the handler).
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
      // Cancelled is the user-initiated marker; it wins over a
      // subsequent step.failed (which would say "interrupted").
      // Matches the handler's ordering: cancelled fires first,
      // then the natural failure stream catches up.
      return {
        ...state,
        startedAt,
        banner: { kind: "cancelled", reason: ev.reason },
      };
    default:
      // Forward-compat: an unknown kind from a server with newer
      // taxonomy doesn't crash the reducer. Logged for diagnosis.
      // eslint-disable-next-line no-console
      console.warn("useCreateProgress: unknown event kind", ev);
      return state;
  }
}

// parseEventId turns the SSE frame's `id:` value (surfaced by the
// browser as MessageEvent.lastEventId) into the monotonic sequence
// number the server assigns per stream (sse_progress.go: id =
// seq.Add(1)). Returns null when the frame carried no id or a
// non-numeric one, in which case the event is always applied (we
// have nothing to compare against).
export function parseEventId(lastEventId: string | undefined): number | null {
  if (!lastEventId) return null;
  const n = Number(lastEventId);
  return Number.isInteger(n) ? n : null;
}

// useCreateProgress opens an EventSource on the given URL, parses
// each message's data as a typed CreateProgressEvent, and feeds
// the reducer. Returns the current snapshot.
//
// Returns INITIAL_STATE while eventsUrl is null — the modal uses
// that null state to render the "submitting…" footer between
// POST start and 202 response. As soon as eventsUrl is set the
// stream opens and the connecting->step.started transition is
// visible to the user within the SSE replay latency.
//
// Cleanup: EventSource.close() runs on unmount AND on eventsUrl
// change so a successive create call cleanly tears down the
// previous stream.
export function useCreateProgress(eventsUrl: string | null): ProgressState {
  const [state, dispatch] = useReducer(reducer, INITIAL_STATE);

  // Highest server sequence id applied so far on the current
  // stream. EventSource auto-reconnects after a transient drop, and
  // the per-instance handler (instances.go: SubscribeWithReplay)
  // replays the WHOLE ring buffer on every (re)connection — it does
  // not honor Last-Event-ID server-side. Without a client-side
  // guard, append-only events (`output`, `warn`) would be applied
  // twice (or N times across N reconnects), so the terminal log and
  // warning list would show duplicates. Server ids are monotonic
  // per stream, so dropping any frame whose id is <= the highest
  // we've already applied makes the replay idempotent.
  const lastEventIdRef = useRef<number | null>(null);

  useEffect(() => {
    if (!eventsUrl) return;
    // New stream → reset the watermark; sequence ids restart at 1.
    lastEventIdRef.current = null;
    const es = new EventSource(eventsUrl);
    es.onmessage = (msg) => {
      const id = parseEventId(msg.lastEventId);
      if (id !== null) {
        if (lastEventIdRef.current !== null && id <= lastEventIdRef.current) {
          // Already applied (this is a replayed frame after a
          // reconnect) — skip so collections don't duplicate.
          return;
        }
        lastEventIdRef.current = id;
      }
      try {
        const ev = JSON.parse(msg.data) as CreateProgressEvent;
        dispatch(ev);
      } catch {
        // Malformed events shouldn't crash the modal. The
        // backend's SSE writer is well-typed; if a JSON.parse
        // fails here it's worth a console warning but not a
        // user-visible error.
        // eslint-disable-next-line no-console
        console.warn("useCreateProgress: bad event payload", msg.data);
      }
    };
    es.onerror = () => {
      // EventSource auto-reconnects on transient errors; we
      // don't have to do anything. A permanent failure (server
      // gone) leaves the stream in the closed state and the
      // browser stops retrying — the user still sees the last
      // good state in the modal.
    };
    return () => es.close();
  }, [eventsUrl]);

  return state;
}
