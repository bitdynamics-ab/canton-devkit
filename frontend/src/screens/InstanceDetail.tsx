import { useEffect, useState } from "react";
import {
  ApiError,
  type Instance,
  fetchInstance,
  pauseInstance,
  restartInstance,
  resumeInstance,
  scrubInstance,
  stopInstance,
  unpauseInstance,
} from "../api";
import { W, wMono } from "../tokens";
import { BackupRestore } from "./BackupRestore";

// InstanceDetail — the per-instance detail card the dashboard
// pops above the Developer setup card when a row is selected.
//
// Surfaces the fields that GET /api/instances/:name returns
// beyond the summary row (compose project, docker network,
// data dir, container prefix, uptime, live-probe state). The
// summary table only carries name/status/version/ports/started
// — the rest is hidden behind this fetch.
//
// Pure-frontend slice: the endpoint shipped in PR #43 but no
// screen consumed it. Wiring it surfaces the data without
// changing the backend.
interface Props {
  name: string;
  // statusHint comes from sel.instances (the always-fresh list)
  // and gates which action button renders. Falls back to the
  // status in the fetched-instance state if omitted — but the
  // dashboard should pass it so the button reflects the latest
  // list state immediately after onChanged, not the stale copy
  // from this component's own mount-time fetch.
  statusHint?: string;
  // Optional: refresh the dashboard's instance list after a Stop
  // succeeds so the row's status updates (running → stopped) and
  // the DeveloperSetup panel hides.
  onChanged?: () => void;
}

export function InstanceDetail({ name, statusHint, onChanged }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Bumped to force a refetch after a successful Stop/Remove so
  // the cached instance.status doesn't lie about the post-action
  // state.
  const [refetchTick, setRefetchTick] = useState(0);
  const [stopping, setStopping] = useState<
    | { kind: "idle" }
    | { kind: "running" }
    | { kind: "err"; message: string }
  >({ kind: "idle" });

  async function onStop() {
    if (!confirm(`Stop instance ${name}? Containers will be brought down via docker compose. Data volumes are preserved.`)) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await stopInstance(name);
      setStopping({ kind: "idle" });
      // Bump our own refetch tick so this card's status field
      // updates from running → stopped, then notify the parent
      // so the dashboard's row + ActionButton catch up too.
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to stop";
      setStopping({ kind: "err", message: msg });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onPause() {
    setStopping({ kind: "running" });
    try {
      await pauseInstance(name);
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      setStopping({ kind: "err", message: e instanceof ApiError ? e.message : "failed to pause" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onResume() {
    setStopping({ kind: "running" });
    try {
      await unpauseInstance(name);
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      setStopping({ kind: "err", message: e instanceof ApiError ? e.message : "failed to resume" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onRestart() {
    if (
      !confirm(
        `Restart ${name}? Containers will be brought down and back up via docker compose. ` +
          `The recorded Splice version and profiles are preserved; data volumes are NOT touched.`,
      )
    ) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await restartInstance(name);
      // 202 — restart is async (down → up). The dashboard's 15s
      // poll will pick up the transitional `creating` status when
      // the goroutine reaches the up phase; refresh both surfaces
      // eagerly so the user sees movement within the next tick.
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to restart";
      setStopping({ kind: "err", message: msg });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onStart() {
    setStopping({ kind: "running" });
    try {
      await resumeInstance(name);
      // 202 — bring-up is in progress. The dashboard's 15s
      // poll will pick up the running status when the
      // reconciler sees it. Refresh both surfaces eagerly so
      // the user sees "creating" within the next tick.
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to start";
      setStopping({ kind: "err", message: msg });
    }
  }

  async function onRemove() {
    if (
      !confirm(
        `Remove ${name} from the registry?\n\nThis deletes the instance entry + state.json. ` +
          `Docker volumes (if any) are NOT touched — for that, use \`dpm localnet clean --name ${name}\` from a terminal.`,
      )
    ) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await scrubInstance(name);
      setStopping({ kind: "idle" });
      onChanged?.();
      // No setRefetchTick — the entry is gone, the parent's
      // refresh will drop this whole card via sel.selected
      // changing or the conditional render hiding it.
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to remove";
      setStopping({ kind: "err", message: msg });
      onChanged?.();
    }
  }

  useEffect(() => {
    let cancelled = false;
    // Only show the loading placeholder on a true name-change
    // mount, not on a refetchTick bump — the latter is a
    // background refresh and the cached data is still valid
    // until the new fetch resolves. Without this guard, every
    // Stop/Remove would briefly blank the detail card.
    if (refetchTick === 0) {
      setState({ kind: "loading" });
    }
    fetchInstance(name)
      .then((r) => {
        if (!cancelled) setState({ kind: "ok", instance: r });
      })
      .catch((e) => {
        if (cancelled) return;
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load instance",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, refetchTick]);

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
      <header style={{ marginBottom: 12, display: "flex", alignItems: "baseline", gap: 12 }}>
        <div style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
          Instance detail
        </div>
        <code style={{ color: W.brand, fontFamily: wMono, fontSize: 12 }}>{name}</code>
        {state.kind === "ok" && state.instance.live_probe_failed && (
          <span
            style={{
              color: W.warn,
              fontSize: 11,
              border: `1px solid ${W.warn}`,
              borderRadius: 6,
              padding: "2px 8px",
            }}
          >
            live probe failed
          </span>
        )}
        <span style={{ marginLeft: "auto" }} />
        {/* Status source priority:
           1. statusHint from parent (always-fresh sel.instances row)
           2. state.instance.status (this card's own fetch)
           This keeps the action button accurate the instant the
           dashboard refreshes after Stop, without waiting for
           this card's own refetch to settle. */}
        {(statusHint || state.kind === "ok") && (
          <ActionButton
            status={statusHint ?? (state.kind === "ok" ? state.instance.status : "")}
            busy={stopping.kind === "running"}
            onStart={onStart}
            onStop={onStop}
            onPause={onPause}
            onResume={onResume}
            onRemove={onRemove}
            onRestart={onRestart}
          />
        )}
      </header>

      {stopping.kind === "err" && (
        <div
          role="alert"
          style={{
            background: `${W.err}10`,
            color: W.err,
            border: `1px solid ${W.err}`,
            borderRadius: 6,
            padding: "6px 10px",
            fontSize: 12,
            marginBottom: 10,
          }}
        >
          Stop failed: {stopping.message}
        </div>
      )}

      {state.kind === "loading" && (
        <div style={{ color: W.dim, fontSize: 13 }}>Loading…</div>
      )}
      {state.kind === "err" && (
        <div style={{ color: W.err, fontSize: 13 }}>{state.error}</div>
      )}
      {state.kind === "ok" && <DetailGrid instance={state.instance} />}
      {/* Backup & restore lives inside the detail card so
          the instance-name context is implicit. Renders even on
          loading/error so the user can still take a snapshot of a
          mostly-broken instance for support tickets. */}
      <BackupRestore instanceName={name} />
    </section>
  );
}

function DetailGrid({ instance }: { instance: Instance }) {
  // Field order mirrors the mockup's "About this instance" card:
  // identity first, then runtime, then on-disk locations.
  const rows: Array<[string, React.ReactNode]> = [
    ["splice", instance.splice_version],
    ["status", instance.status],
    ["created", instance.created_at],
    ["uptime", instance.uptime ?? "—"],
    ["compose project", instance.compose_project],
    ["docker network", instance.docker_network],
    ["container prefix", instance.container_prefix],
    ["project dir", instance.project_dir],
    ["data dir", instance.data_dir],
  ];

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "160px 1fr",
        rowGap: 6,
        columnGap: 16,
        fontSize: 12.5,
      }}
    >
      {rows.map(([k, v]) => (
        <div key={k} style={{ display: "contents" }}>
          <div style={{ color: W.dim }}>{k}</div>
          <div style={{ color: W.text2, fontFamily: wMono, wordBreak: "break-all" }}>
            {v}
          </div>
        </div>
      ))}
    </div>
  );
}

// ActionButton dispatches the right verb(s) per instance status.
// Registry status alone isn't enough — docker truth may diverge
// (the ContainerHealth panel shows this). Specifically:
//
//   - running        → Stop  (containers are live by definition)
//   - failed/partial → Stop + Remove (containers MAY still be up
//                       — the orchestrator gave up but docker
//                       compose down is the right cleanup; if no
//                       project exists docker no-ops cleanly)
//   - stopped        → Remove only (containers definitely gone)
//   - creating       → no button (CreatingPanel owns that surface)
//   - other          → no button (defensive)
//
// The Stop variant on failed/partial is labeled "Stop containers"
// (distinct from "Stop" on running) so the user knows it's a
// force-cleanup rather than a graceful shutdown of a healthy
// instance. The wording difference also matters because docker
// compose down with --volumes is destructive — surface it
// explicitly when the registry's been lying.
function ActionButton({
  status,
  busy,
  onStart,
  onStop,
  onPause,
  onResume,
  onRemove,
  onRestart,
}: {
  status: string;
  busy: boolean;
  onStart: () => void;
  onStop: () => void;
  onPause: () => void;
  onResume: () => void;
  onRemove: () => void;
  onRestart: () => void;
}) {
  // Restart is offered alongside the existing controls on every
  // non-transitional status: running, paused, failed, partial. The
  // `creating` and `stopping` statuses are in-flight transitions
  // where ActionButton renders nothing (the CreatingPanel and the
  // disabled-by-busy guard cover those), so the Restart button is
  // implicitly hidden during those phases.
  if (status === "running") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <button
          onClick={onPause}
          disabled={busy}
          title="Freeze containers (docker compose pause) — hold state + ports, free CPU. Resume is instant."
          style={btnStyle(W.warn, busy)}
        >
          {busy ? "…" : "⏸ Pause"}
        </button>
        <button
          onClick={onRestart}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
          style={btnStyle(W.brand, busy)}
        >
          {busy ? "…" : "↻ Restart"}
        </button>
        <button
          onClick={onStop}
          disabled={busy}
          title="Bring containers down via docker compose. Data volumes preserved."
          style={btnStyle(W.err, busy)}
        >
          {busy ? "Stopping…" : "⏹ Stop"}
        </button>
      </div>
    );
  }
  if (status === "paused") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <button
          onClick={onResume}
          disabled={busy}
          title="Resume frozen containers (docker compose unpause) — no boot cost."
          style={btnStyle(W.brand, busy)}
        >
          {busy ? "…" : "▶ Resume"}
        </button>
        <button
          onClick={onRestart}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
          style={btnStyle(W.brand, busy)}
        >
          {busy ? "…" : "↻ Restart"}
        </button>
        <button
          onClick={onStop}
          disabled={busy}
          title="Bring containers down via docker compose. Data volumes preserved."
          style={btnStyle(W.err, busy)}
        >
          {busy ? "Stopping…" : "⏹ Stop"}
        </button>
      </div>
    );
  }
  if (status === "failed" || status === "partial") {
    // Both Stop and Remove — docker may still have live containers
    // even though the registry gave up. Restart is also offered:
    // failed/partial often comes from a transient compose hiccup
    // that a clean down + up sequence resolves without losing the
    // instance metadata.
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <button
          onClick={onRestart}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
          style={btnStyle(W.brand, busy)}
        >
          {busy ? "…" : "↻ Restart"}
        </button>
        <button
          onClick={onStop}
          disabled={busy}
          title="Force docker compose down — use if containers are still running. Data volumes preserved."
          style={btnStyle(W.warn, busy)}
        >
          {busy ? "Stopping…" : "⏹ Stop containers"}
        </button>
        <button
          onClick={onRemove}
          disabled={busy}
          title="Remove the registry entry only. Won't touch docker — run Stop first if containers are live."
          style={btnStyle(W.dim, busy)}
        >
          {busy ? "Removing…" : "✕ Remove entry"}
        </button>
      </div>
    );
  }
  if (status === "stopped") {
    // Start sits next to Remove so a user who came back to a
    // stopped instance can resume it without bouncing to the
    // terminal. The Start path is the dedicated POST /up
    // endpoint — reuses the recorded version + ports, won't
    // silently upgrade.
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <button
          onClick={onStart}
          disabled={busy}
          title="Bring containers back up with the recorded Splice version and ports."
          style={btnStyle(W.brand, busy)}
        >
          {busy ? "Starting…" : "▶ Start"}
        </button>
        <button
          onClick={onRemove}
          disabled={busy}
          title="Remove the registry entry + state.json. Docker volumes (if any) untouched."
          style={btnStyle(W.dim, busy)}
        >
          {busy ? "Removing…" : "✕ Remove entry"}
        </button>
      </div>
    );
  }
  return null;
}

function btnStyle(accent: string, busy: boolean): React.CSSProperties {
  return {
    background: "transparent",
    color: busy ? W.dim : accent,
    border: `1px solid ${busy ? W.dim : accent}`,
    borderRadius: 6,
    padding: "4px 12px",
    fontSize: 11.5,
    fontWeight: 600,
    cursor: busy ? "wait" : "pointer",
  };
}
