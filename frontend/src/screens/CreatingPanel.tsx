import { useEffect, useRef, useState } from "react";
import {
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  scrubInstance,
  type StepName,
} from "../api";
import { W, wMono } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcCheck, IcRefresh, IcX } from "../components/icons";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// CreatingPanel — shown above the InstanceDetail/DeveloperSetup cards
// when the selected instance is status="creating". Subscribes to
// /api/instances/{name}/events and renders the same step rows as the
// create modal (both consume the shared useCreateProgress state).
//
// Two scenarios:
//   1. Live bring-up: the SSE stream replays buffered events + live
//      ones — real-time progress just like the modal.
//   2. Zombie creating: the registry says creating but no goroutine is
//      publishing (e.g. a server restart killed it mid-flight). No
//      events arrive; after a grace period the panel surfaces a "looks
//      stalled" hint with a cleanup CTA.

const ZOMBIE_GRACE_MS = 3000; // wait this long before showing "stalled" hint

interface Props {
  name: string;
  // Called after a cancel or stalled-state cleanup so the Dashboard
  // re-fetches and the row's status updates.
  onRefresh: () => void;
}

export function CreatingPanel({ name, onRefresh }: Props) {
  const eventsUrl = `/api/instances/${encodeURIComponent(name)}/events`;
  const progress = useCreateProgress(eventsUrl);

  // Zombie detection: no event by ZOMBIE_GRACE_MS surfaces the
  // "stalled" affordance. Derived freshly on every render rather than
  // via setTimeout — a timeout closure would capture progress.startedAt
  // at setup time and never re-check it, so events arriving late (slow
  // network, slow first publish) would leave the panel permanently
  // "stalled". mountedAtRef pegs the start time per name; the 1s ticker
  // below keeps the derived check current.
  const mountedAtRef = useRef<number>(Date.now());
  useEffect(() => {
    mountedAtRef.current = Date.now();
  }, [name]);
  const [, forceTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  const zombieSuspected =
    progress.startedAt === null &&
    Date.now() - mountedAtRef.current > ZOMBIE_GRACE_MS;

  // Live path: ask the goroutine to stop. The backend publishes
  // kind=cancelled, then the goroutine sees ctx.Done() and writes
  // status=failed via its existing path.
  async function onCancelLive() {
    try {
      await cancelInstanceUp(name);
      setTimeout(onRefresh, 300);
    } catch {
      onRefresh();
    }
  }

  // Zombie path: no live goroutine, so /up cancel would 404 — scrub the
  // registry entry instead so the row disappears from the list.
  async function onScrub() {
    try {
      await scrubInstance(name);
      onRefresh();
    } catch {
      // Even if scrub fails (e.g. 409 because the entry is now
      // running), refresh so the user sees current state.
      onRefresh();
    }
  }

  return (
    <section
      style={{
        marginTop: 24,
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
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
                    borderRadius: 2,
                    padding: "6px 10px",
                    fontSize: 11.5,
                    marginBottom: 4,
                  }}
                >
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      gap: 6,
                    }}
                  >
                    <IcAlert size={12} /> {m}
                  </span>
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
              <Button variant="secondary" onClick={onCancelLive}>
                Cancel bring-up
              </Button>
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
                  borderRadius: 2,
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
        return <IcCheck size={12} style={{ color: W.ok }} />;
      case "active":
        return <Dot color={W.brand} pulse />;
      case "fail":
        return <IcX size={12} style={{ color: W.err }} />;
      default:
        return <Dot color={W.faint} />;
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
      <span
        style={{
          width: 14,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          alignSelf: "flex-start",
          height: 19,
          flex: "none",
        }}
      >
        {icon}
      </span>
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
        borderRadius: 2,
        border: `1px solid ${color}`,
        background: `${color}1A`,
        color,
        fontFamily: wMono,
        fontSize: 11,
      }}
    >
      <Dot color={color} /> {children}
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
        borderRadius: 4,
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
        <Button variant="secondary" icon={<IcRefresh />} onClick={onRefresh}>
          Refresh list
        </Button>
        <Button variant="danger" icon={<IcX />} onClick={onScrub}>
          Remove entry
        </Button>
      </div>
    </div>
  );
}
