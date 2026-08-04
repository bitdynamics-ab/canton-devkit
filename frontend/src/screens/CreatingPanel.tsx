import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  scrubInstance,
  type StepName,
} from "../api";
import { W, wMono, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcCheck, IcCopy, IcRefresh, IcX } from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// Shown when the selected instance is status="creating". Renders live SSE
// bring-up progress in the mockup's two-column layout — step checklist ⟷
// docker-compose terminal — or, if no event arrives within ZOMBIE_GRACE_MS
// (e.g. a server restart orphaned the entry), a stalled hint + cleanup.
// Every value shown is real (step detail, terminal, elapsed from startedAt);
// nothing is estimated.

const ZOMBIE_GRACE_MS = 3000;

interface Props {
  name: string;
  splice?: string;
  ports?: string;
  onRefresh: () => void;
}

export function CreatingPanel({ name, splice, ports, onRefresh }: Props) {
  const eventsUrl = `/api/instances/${encodeURIComponent(name)}/events`;
  const progress = useCreateProgress(eventsUrl);

  // Derived per render, not via setTimeout: a timeout closure would capture
  // startedAt once and never re-check, wedging late events as "stalled".
  const mountedAtRef = useRef<number>(Date.now());
  const settledRef = useRef(false);
  useEffect(() => {
    mountedAtRef.current = Date.now();
    settledRef.current = false;
  }, [name]);
  const [, forceTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  // When bring-up reaches a terminal banner (done/failed), the server flips
  // the registry off "creating"; re-fetch once so the Dashboard swaps this
  // panel for the running Overview promptly instead of waiting on the 15s
  // list poll — otherwise a finished bring-up reads as "stuck on Ready".
  useEffect(() => {
    const done =
      progress.banner.kind === "done" || progress.banner.kind === "failed";
    if (done && !settledRef.current) {
      settledRef.current = true;
      const t = setTimeout(onRefresh, 500);
      return () => clearTimeout(t);
    }
  }, [progress.banner.kind, onRefresh]);
  const zombieSuspected =
    progress.startedAt === null &&
    Date.now() - mountedAtRef.current > ZOMBIE_GRACE_MS;

  async function onCancelLive() {
    try {
      await cancelInstanceUp(name);
      setTimeout(onRefresh, 300);
    } catch {
      onRefresh();
    }
  }
  async function onScrub() {
    try {
      await scrubInstance(name);
      onRefresh();
    } catch {
      onRefresh();
    }
  }

  const total = STEP_ORDER.length;
  const doneCount = STEP_ORDER.filter(
    (s) => progress.perStep[s as StepName].status === "done",
  ).length;
  const hasActive = STEP_ORDER.some(
    (s) => progress.perStep[s as StepName].status === "active",
  );
  const stepNum = Math.min(total, doneCount + (hasActive ? 1 : 0));
  const elapsed =
    progress.startedAt !== null ? fmtElapsed(Date.now() - progress.startedAt) : null;
  const canCancel =
    progress.banner.kind === "running" && progress.startedAt !== null;

  const meta = [
    splice ? `Splice ${splice}` : null,
    ports ? `ports ${ports}` : null,
    elapsed ? `elapsed ${elapsed}` : null,
  ].filter(Boolean);

  return (
    <section style={{ display: "flex", flexDirection: "column", gap: 14 }}>
      <header style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <h1 style={{ fontSize: fs.title, fontWeight: 600, margin: 0, color: W.text }}>
          {name}
        </h1>
        <BannerPill banner={progress.banner} zombie={zombieSuspected} />
        <span style={{ flex: 1 }} />
        {canCancel && (
          <Button variant="secondary" size="sm" icon={<IcX size={13} />} onClick={onCancelLive}>
            Cancel bring-up
          </Button>
        )}
      </header>
      {meta.length > 0 && (
        <div style={{ color: W.dim, fontSize: fs.meta, fontFamily: wMono, marginTop: -6 }}>
          {meta.join("  ·  ")}
        </div>
      )}

      {zombieSuspected && progress.startedAt === null ? (
        <ZombieHint name={name} onScrub={onScrub} onRefresh={onRefresh} />
      ) : (
        <>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1fr)",
              gap: 14,
              alignItems: "start",
            }}
          >
            <StepsCard
              progress={progress}
              stepNum={stepNum}
              total={total}
              connecting={progress.startedAt === null}
            />
            <TerminalCard progress={progress} />
          </div>
          <SafeToLeaveNote />
        </>
      )}
    </section>
  );
}

function StepsCard({
  progress,
  stepNum,
  total,
  connecting,
}: {
  progress: ProgressState;
  stepNum: number;
  total: number;
  connecting: boolean;
}) {
  return (
    <div style={cardStyle}>
      <div style={cardHeader}>
        <span style={{ display: "inline-flex", alignItems: "baseline", gap: 8 }}>
          <strong style={{ color: W.text, fontSize: fs.data }}>Bring-up</strong>
          <span style={{ color: W.dim, fontSize: fs.label }}>
            {connecting ? "connecting…" : `step ${stepNum} of ${total}`}
          </span>
        </span>
        <span style={{ color: W.faint, fontSize: fs.label, fontFamily: wMono }}>
          live · SSE
        </span>
      </div>
      <div style={{ padding: "4px 14px 8px" }}>
        {STEP_ORDER.map((step) => (
          <StepRow
            key={step}
            label={STEP_LABELS[step as StepName]}
            state={progress.perStep[step as StepName]}
          />
        ))}
      </div>
      {progress.warnings.length > 0 && (
        <div style={{ padding: "0 14px 12px" }}>
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
                marginTop: 6,
              }}
            >
              <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <IcAlert size={12} /> {m}
              </span>
            </div>
          ))}
        </div>
      )}
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
  const color = state.status === "pending" ? W.text2 : W.text;
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        padding: "7px 0",
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
        <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
          <span style={{ color }}>{label}</span>
          {state.status === "done" && (
            <span style={{ color: W.dim, fontSize: fs.label }}>done</span>
          )}
        </div>
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
              marginTop: 5,
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

function TerminalCard({ progress }: { progress: ProgressState }) {
  const preRef = useRef<HTMLPreElement>(null);
  // Autoscroll to the newest line as output streams in.
  useLayoutEffect(() => {
    const el = preRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [progress.terminal.length]);

  const copyLog = () => {
    void navigator.clipboard?.writeText(
      progress.terminal.map((l) => l.text).join("\n"),
    );
  };

  return (
    <div style={cardStyle}>
      <div style={cardHeader}>
        <span style={{ display: "inline-flex", alignItems: "baseline", gap: 8 }}>
          <strong style={{ color: W.text, fontSize: fs.data }}>Terminal</strong>
          <span style={{ color: W.dim, fontSize: fs.label }}>docker compose output</span>
        </span>
        <button
          type="button"
          onClick={copyLog}
          disabled={progress.terminal.length === 0}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 5,
            background: "transparent",
            border: "none",
            color: progress.terminal.length === 0 ? W.faint : W.brandText,
            fontSize: fs.label,
            cursor: progress.terminal.length === 0 ? "default" : "pointer",
            padding: 0,
          }}
        >
          <IcCopy size={12} /> Copy log
        </button>
      </div>
      <pre
        ref={preRef}
        style={{
          margin: 0,
          background: W.inset,
          padding: "10px 14px",
          fontFamily: wMono,
          fontSize: fs.micro,
          color: W.text2,
          height: 300,
          overflow: "auto",
          lineHeight: 1.6,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
        }}
      >
        {progress.terminal.length === 0 ? (
          <span style={{ color: W.faint }}>waiting for docker compose output…</span>
        ) : (
          progress.terminal.map((l, i) => (
            <div key={i} style={{ color: l.stream === "stderr" ? W.warn : W.text2 }}>
              {l.text}
            </div>
          ))
        )}
      </pre>
      <div
        style={{
          padding: "8px 14px",
          borderTop: `1px solid ${W.border}`,
          background: W.sunken,
          color: W.faint,
          fontSize: fs.label,
          fontFamily: wMono,
        }}
      >
        stderr in amber · autoscrolls
      </div>
    </div>
  );
}

function SafeToLeaveNote() {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        color: W.dim,
        fontSize: fs.label,
      }}
    >
      <Dot color={W.dim} size={5} />
      Safe to leave. Bring-up runs on the server — the switcher keeps the{" "}
      <code style={{ fontFamily: wMono, color: W.text2 }}>creating</code> badge
      and this page picks up where it left off.
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
      return <StatusBadge status="creating" variant="pill" pulse />;
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

function fmtElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

const cardStyle: React.CSSProperties = {
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: R.card,
  overflow: "hidden",
};

const cardHeader: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 8,
  padding: "10px 14px",
  borderBottom: `1px solid ${W.border}`,
  background: W.surface2,
};
