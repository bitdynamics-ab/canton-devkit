import { useEffect, useRef, useState } from "react";
import {
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  scrubInstance,
  type StepName,
} from "../api";
import { W, wMono, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcCheck, IcRefresh, IcX } from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// Shown when the selected instance is status="creating". Renders live
// SSE bring-up progress, or — if no event arrives within ZOMBIE_GRACE_MS
// (e.g. a server restart orphaned the entry) — a stalled hint + cleanup.

const ZOMBIE_GRACE_MS = 3000;

interface Props {
  name: string;
  onRefresh: () => void;
}

export function CreatingPanel({ name, onRefresh }: Props) {
  const eventsUrl = `/api/instances/${encodeURIComponent(name)}/events`;
  const progress = useCreateProgress(eventsUrl);

  // Derived per render, not via setTimeout: a timeout closure would
  // capture startedAt once and never re-check, wedging late events as
  // "stalled". mountedAtRef pegs the start; the 1s ticker below refreshes.
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

  // Live path: ask the goroutine to stop; it publishes kind=cancelled
  // then writes status=failed.
  async function onCancelLive() {
    try {
      await cancelInstanceUp(name);
      setTimeout(onRefresh, 300);
    } catch {
      onRefresh();
    }
  }

  // Zombie path: no live goroutine, so /up cancel would 404 — scrub the
  // registry entry instead.
  async function onScrub() {
    try {
      await scrubInstance(name);
      onRefresh();
    } catch {
      onRefresh();
    }
  }

  return (
    <section
      style={{
        marginTop: 24,
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
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
        <div style={{ fontWeight: 600, fontSize: fs.lead, color: W.text }}>
          Bring-up in progress
        </div>
        <code style={{ color: W.brand, fontFamily: wMono, fontSize: fs.meta }}>
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
                    background: `${tint(W.warn, 10)}`,
                    border: `1px solid ${tint(W.warn, 27)}`,
                    color: W.warn,
                    borderRadius: R.control,
                    padding: "6px 10px",
                    fontSize: fs.label,
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
                Cancel
              </Button>
            </div>
          )}
          {progress.terminal.length > 0 && (
            <details open style={{ marginTop: 14 }}>
              <summary style={{ cursor: "pointer", color: W.dim, fontSize: fs.label }}>
                Terminal output · {progress.terminal.length} line(s)
              </summary>
              <pre
                style={{
                  margin: "8px 0 0",
                  background: W.bg,
                  border: `1px solid ${W.border}`,
                  borderRadius: R.control,
                  padding: "10px 12px",
                  fontFamily: wMono,
                  fontSize: fs.micro,
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
        borderBottom: `1px solid ${W.border}`,
        fontSize: fs.meta,
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
              fontSize: fs.label,
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
              borderRadius: R.control,
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
    return <StatusBadge status="stalled" variant="pill" />;
  }
  switch (banner.kind) {
    case "done":
      return <StatusBadge status="ready" variant="pill" />;
    case "failed":
      return <StatusBadge status="failed" variant="pill" />;
    case "cancelled":
      return <StatusBadge status="cancelled" variant="pill" />;
    default:
      return <StatusBadge status="starting" variant="pill" pulse />;
  }
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
      role="alert"
      style={{
        background: `${tint(W.warn, 6)}`,
        border: `1px solid ${tint(W.warn, 27)}`,
        borderRadius: R.card,
        padding: "12px 14px",
        color: W.text,
        fontSize: fs.meta,
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
        <li>The bring-up finished after the page loaded. Refresh to pick up the new state.</li>
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
