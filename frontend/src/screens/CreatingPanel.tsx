import { useEffect, useRef, useState } from "react";
import {
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  scrubInstance,
  type StepName,
} from "../api";
import { W, wMono } from "../tokens";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// CreatingPanel — shown above the InstanceDetail/DeveloperSetup
// cards when the selected instance is in status="creating".
// Subscribes to /api/instances/{name}/events and renders the
// same step rows the create modal uses, so a user who clicks
// an in-flight instance sees what step the bring-up is on.
//
// Two scenarios:
//
//   1. Live up in progress (goroutine still publishing): the
//      SSE stream replays buffered events + live ones; the
//      panel shows real-time progress just like the modal.
//
//   2. Zombie creating (registry says creating but no goroutine
//      is publishing — e.g. server restart killed the goroutine
//      mid-flight, leaving the registry entry orphaned): no
//      events arrive. After a grace period the panel surfaces
//      a "looks stalled" hint with a cleanup CTA.
//
// We deliberately re-use useCreateProgress so the step-render
// logic stays in one place; CreateLocalNetModal and this panel
// both consume the same state shape.

const ZOMBIE_GRACE_MS = 3000; // wait this long before showing "stalled" hint

interface Props {
  name: string;
  // Called when the user clicks "Refresh list" after a cancel or
  // a stalled-state detection — the Dashboard re-fetches so the
  // row's status updates.
  onRefresh: () => void;
}

export function CreatingPanel({ name, onRefresh }: Props) {
  // EventSource URL is per-instance; the hook handles subscribe
  // + unsubscribe on name change.
  const eventsUrl = `/api/instances/${encodeURIComponent(name)}/events`;
  const progress = useCreateProgress(eventsUrl);

  // Zombie detection: if no event has arrived by ZOMBIE_GRACE_MS
  // we surface the "stalled" affordance. Derived freshly on every
  // render rather than via setTimeout — a setTimeout closure
  // captures progress.startedAt at effect-setup time and never
  // re-checks it, so events arriving 4 seconds later (slow
  // network, slow first publish) would leave the panel
  // permanently "stalled" even though the stream is flowing.
  //
  // The mountedAt ref pegs the start time once per (name); we
  // re-render every second via the elapsed-time ticker, so the
  // derived check stays current without any timer of its own.
  const mountedAtRef = useRef<number>(Date.now());
  useEffect(() => {
    mountedAtRef.current = Date.now();
  }, [name]);
  // Tick once per second so the zombie-grace check + the elapsed
  // counter both re-evaluate. Cheap; clearInterval on unmount.
  const [, forceTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  const zombieSuspected =
    progress.startedAt === null &&
    Date.now() - mountedAtRef.current > ZOMBIE_GRACE_MS;

  // Cancel in the LIVE path: ask the goroutine to stop.
  // Backend publishes kind=cancelled, then the goroutine sees
  // ctx.Done() and writes status=failed via its existing path.
  async function onCancelLive() {
    try {
      await cancelInstanceUp(name);
      setTimeout(onRefresh, 300);
    } catch {
      onRefresh();
    }
  }

  // Cancel in the ZOMBIE path: there is no live goroutine, so
  // /up cancel would 404. Scrub the registry entry instead so the
  // row disappears from the list. The user explicitly asked for
  // cleanup; we honor it.
  async function onScrub() {
    try {
      await scrubInstance(name);
      onRefresh();
    } catch {
      // If scrub fails (e.g. 409 because the backend decided the
      // entry is now running), refresh anyway so the user sees
      // current state.
      onRefresh();
    }
  }

  return (
    <section
      style={{
        marginTop: 24,
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 16,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: 10,
          marginBottom: 12,
        }}
      >
        <div style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
          Bring-up in progress
        </div>
        <code style={{ color: W.brand, fontFamily: wMono, fontSize: 12 }}>
          {name}
        </code>
        <span style={{ flex: 1 }} />
        <BannerPill banner={progress.banner} zombie={zombieSuspected} />
      </header>

      {zombieSuspected && progress.startedAt === null ? (
        <ZombieHint name={name} onScrub={onScrub} onRefresh={onRefresh} />
      ) : (
        <>
          <StepList progress={progress} />
          {progress.warnings.length > 0 && (
            <div style={{ marginTop: 10 }}>
              {progress.warnings.map((m, i) => (
                <div
                  key={i}
                  style={{
                    background: `${W.warn}1A`,
                    border: `1px solid ${W.warn}44`,
                    color: W.warn,
                    borderRadius: 6,
                    padding: "6px 10px",
                    fontSize: 11.5,
                    marginBottom: 4,
                  }}
                >
                  ⚠ {m}
                </div>
              ))}
            </div>
          )}
          {progress.banner.kind === "running" && progress.startedAt !== null && (
            <div
              style={{
                marginTop: 12,
                display: "flex",
                justifyContent: "flex-end",
              }}
            >
              <button onClick={onCancelLive} style={cancelBtnStyle}>
                Cancel bring-up
              </button>
            </div>
          )}
          {progress.terminal.length > 0 && (
            <details open style={{ marginTop: 14 }}>
              <summary style={{ cursor: "pointer", color: W.dim, fontSize: 11.5 }}>
                Terminal output · {progress.terminal.length} line(s)
              </summary>
              <pre
                style={{
                  margin: "8px 0 0",
                  background: W.bg,
                  border: `1px solid ${W.border}`,
                  borderRadius: 7,
                  padding: "10px 12px",
                  fontFamily: wMono,
                  fontSize: 10.5,
                  color: W.text2,
                  maxHeight: 160,
                  overflow: "auto",
                  lineHeight: 1.55,
                }}
              >
                {progress.terminal.map((l, i) => (
                  <div
                    key={i}
                    style={{
                      color: l.stream === "stderr" ? W.warn : W.text2,
                    }}
                  >
                    {l.text}
                  </div>
                ))}
              </pre>
            </details>
          )}
        </>
      )}
    </section>
  );
}

function StepList({ progress }: { progress: ProgressState }) {
  return (
    <div>
      {STEP_ORDER.map((step) => (
        <StepRow
          key={step}
          label={STEP_LABELS[step as StepName]}
          state={progress.perStep[step as StepName]}
        />
      ))}
    </div>
  );
}

function StepRow({ label, state }: { label: string; state: StepState }) {
  const icon = (() => {
    switch (state.status) {
      case "done":
        return <span style={{ color: W.ok }}>✓</span>;
      case "active":
        return <span style={{ color: W.brand }}>●</span>;
      case "fail":
        return <span style={{ color: W.err }}>✕</span>;
      default:
        return <span style={{ color: W.faint }}>○</span>;
    }
  })();
  const color =
    state.status === "fail"
      ? W.err
      : state.status === "active"
      ? W.text
      : state.status === "done"
      ? W.text
      : W.text2;
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        padding: "6px 4px",
        borderBottom: `1px dashed ${W.border}`,
        fontSize: 12.5,
      }}
    >
      <span style={{ width: 14, textAlign: "center" }}>{icon}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color }}>{label}</div>
        {(state.detail || state.summary) && (
          <div
            style={{
              color: state.status === "fail" ? W.err : W.dim,
              fontSize: 11,
              fontFamily: wMono,
              marginTop: 2,
            }}
          >
            {state.summary ?? state.detail}
          </div>
        )}
        {state.percent !== undefined && state.status === "active" && (
          <div
            style={{
              marginTop: 4,
              height: 4,
              background: W.surface2,
              borderRadius: 2,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                width: `${state.percent}%`,
                height: "100%",
                background: W.brand,
                transition: "width 0.2s",
              }}
            />
          </div>
        )}
      </div>
    </div>
  );
}

function BannerPill({
  banner,
  zombie,
}: {
  banner: ProgressState["banner"];
  zombie: boolean;
}) {
  if (zombie) {
    return <Pill color={W.warn}>looks stalled</Pill>;
  }
  switch (banner.kind) {
    case "done":
      return <Pill color={W.ok}>ready</Pill>;
    case "failed":
      return <Pill color={W.err}>failed</Pill>;
    case "cancelled":
      return <Pill color={W.warn}>cancelled</Pill>;
    default:
      return <Pill color={W.brand}>streaming</Pill>;
  }
}

function Pill({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 5,
        padding: "2px 9px",
        borderRadius: 999,
        border: `1px solid ${color}`,
        background: `${color}1A`,
        color,
        fontFamily: wMono,
        fontSize: 11,
      }}
    >
      ● {children}
    </span>
  );
}

function ZombieHint({
  name,
  onScrub,
  onRefresh,
}: {
  name: string;
  onScrub: () => void;
  onRefresh: () => void;
}) {
  return (
    <div
      style={{
        background: `${W.warn}10`,
        border: `1px solid ${W.warn}44`,
        borderRadius: 8,
        padding: "12px 14px",
        color: W.text,
        fontSize: 12.5,
        lineHeight: 1.6,
      }}
    >
      <strong style={{ color: W.warn }}>No live progress events.</strong>
      <div style={{ color: W.text2, marginTop: 6 }}>
        The registry has <code style={{ fontFamily: wMono }}>{name}</code>{" "}
        marked as <code style={{ fontFamily: wMono }}>creating</code>, but
        the SSE stream is silent. The most likely causes:
      </div>
      <ul style={{ color: W.text2, marginTop: 6, paddingLeft: 18 }}>
        <li>The bring-up finished after the page loaded — refresh to pick up the new state.</li>
        <li>
          The server was restarted mid-bring-up, orphaning the entry.
          Click <strong>Remove entry</strong> to scrub it from the
          registry, or refresh if you think it's recovered.
        </li>
      </ul>
      <div style={{ marginTop: 12, display: "flex", gap: 8, justifyContent: "flex-end" }}>
        <button onClick={onRefresh} style={{ ...cancelBtnStyle, color: W.brand, borderColor: W.brand }}>
          Refresh list
        </button>
        <button onClick={onScrub} style={{ ...cancelBtnStyle, color: W.err, borderColor: W.err }}>
          Remove entry
        </button>
      </div>
    </div>
  );
}

const cancelBtnStyle: React.CSSProperties = {
  background: "transparent",
  color: W.warn,
  border: `1px solid ${W.warn}`,
  borderRadius: 6,
  padding: "5px 12px",
  fontSize: 12,
  fontWeight: 600,
  cursor: "pointer",
};
