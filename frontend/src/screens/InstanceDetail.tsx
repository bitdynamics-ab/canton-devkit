import { useEffect, useState } from "react";
import {
  ApiError,
  type Endpoint,
  type Instance,
  downInstance,
  fetchInstance,
  pauseInstance,
  recreateInstance,
  scrubInstance,
  startInstance,
  stopInstance,
  unpauseInstance,
} from "../api";
import { W, wMono, tint, R } from "../tokens";
import { Button } from "../components/Button";
import {
  IcEject,
  IcPause,
  IcPlay,
  IcRefresh,
  IcStop,
  IcX,
} from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import { SkeletonBar, useLoadingDelay } from "../components/Skeleton";
import { confirmDialog } from "../components/ConfirmDialog";
import { BackupRestore } from "./BackupRestore";

// UI endpoints the backend probed and found not serving HTTP.
function unreachableUIs(inst: Instance): Endpoint[] {
  return (inst.endpoints ?? []).filter(
    (e) => e.reachability === "unreachable",
  );
}

// InstanceDetail — the per-instance detail card the dashboard shows
// when a row is selected. Surfaces the fields GET /api/instances/:name
// returns beyond the summary row (compose project, docker network,
// data dir, container prefix, uptime, live-probe state).
interface Props {
  name: string;
  // statusHint comes from the dashboard's always-fresh instance list
  // and gates which action button renders. Falls back to this card's
  // own fetched status if omitted — but the dashboard should pass it
  // so the button reflects the latest list state immediately after
  // onChanged, not the stale copy from this card's mount-time fetch.
  statusHint?: string;
  // Refresh the dashboard's instance list after an action succeeds so
  // the row's status updates.
  onChanged?: () => void;
}

export function InstanceDetail({ name, statusHint, onChanged }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Bumped after an action so the cached instance.status doesn't lie
  // about the post-action state.
  const [refetchTick, setRefetchTick] = useState(0);
  const [stopping, setStopping] = useState<
    | { kind: "idle" }
    | { kind: "running" }
    | { kind: "err"; message: string }
  >({ kind: "idle" });
  // Gate the loading skeleton so a fast local fetch never flashes it.
  const showSkeleton = useLoadingDelay(state.kind === "loading");

  async function onStop() {
    // Gentle stop: `docker compose stop` keeps containers around for a
    // fast Start. No destructive confirm needed — nothing is removed.
    setStopping({ kind: "running" });
    try {
      await stopInstance(name);
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to stop";
      setStopping({ kind: "err", message: msg });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onDown() {
    if (
      !(await confirmDialog({
        title: "Tear down instance?",
        body: `Removes ${name}'s containers and networks. Data volumes are preserved, so Start recreates it.`,
        detail: `dpm localnet down ${name}`,
        confirmLabel: "Down",
        danger: true,
      }))
    ) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await downInstance(name);
      setStopping({ kind: "idle" });
      // Refetch our own status, then notify the parent so the
      // dashboard's row + ActionButton catch up too.
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to tear down";
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

  async function onRecreate() {
    if (
      !(await confirmDialog({
        title: "Recreate instance?",
        body: `Brings ${name} down then back up. The recorded Splice version and profiles are preserved. Data volumes are not touched.`,
        detail: `dpm localnet down ${name} && dpm localnet up ${name}`,
        confirmLabel: "Recreate",
      }))
    ) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await recreateInstance(name);
      // 202 — recreate is async (down → up). Refresh both surfaces
      // eagerly so the user sees the transitional status before the
      // dashboard's next poll.
      setStopping({ kind: "idle" });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to recreate";
      setStopping({ kind: "err", message: msg });
      setRefetchTick((n) => n + 1);
      onChanged?.();
    }
  }

  async function onStart() {
    setStopping({ kind: "running" });
    try {
      // 204 → fast `docker compose start` done; 202 → full bring-up in
      // progress (containers had been removed). Either way, refresh
      // both surfaces so the user sees the transitional status before
      // the dashboard's next poll.
      await startInstance(name);
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
      !(await confirmDialog({
        title: "Remove from registry?",
        body: `Deletes the ${name} entry and its state.json. Docker volumes (if any) are not touched. To drop those, run dpm localnet remove from a terminal.`,
        detail: `dpm localnet remove --name ${name}`,
        confirmLabel: "Remove",
        danger: true,
      }))
    ) {
      return;
    }
    setStopping({ kind: "running" });
    try {
      await scrubInstance(name);
      setStopping({ kind: "idle" });
      onChanged?.();
      // No setRefetchTick — the entry is gone; the parent's refresh
      // drops this whole card.
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to remove";
      setStopping({ kind: "err", message: msg });
      onChanged?.();
    }
  }

  useEffect(() => {
    let cancelled = false;
    // Show the loading placeholder only on a true name-change mount,
    // not on a refetchTick bump — without this guard, every action
    // would briefly blank the detail card.
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
        borderRadius: R.card,
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
              border: `1px solid ${tint(W.warn, 34)}`,
              background: tint(W.warn, 13),
              borderRadius: R.control,
              padding: "2px 8px",
            }}
          >
            Live probe failed
          </span>
        )}
        <span style={{ marginLeft: "auto" }} />
        {/* Prefer statusHint (parent's fresh list) over this card's
           own fetch so the action button updates the instant the
           dashboard refreshes. */}
        {(statusHint || state.kind === "ok") && (
          <ActionButton
            status={statusHint ?? (state.kind === "ok" ? state.instance.status : "")}
            busy={stopping.kind === "running"}
            onStart={onStart}
            onStop={onStop}
            onDown={onDown}
            onPause={onPause}
            onResume={onResume}
            onRemove={onRemove}
            onRecreate={onRecreate}
          />
        )}
      </header>

      {stopping.kind === "err" && (
        <div
          role="alert"
          style={{
            background: `${tint(W.err, 6)}`,
            color: W.err,
            border: `1px solid ${W.err}`,
            borderRadius: R.control,
            padding: "6px 10px",
            fontSize: 12,
            marginBottom: 10,
          }}
        >
          Action failed: {stopping.message}
        </div>
      )}

      {state.kind === "ok" && unreachableUIs(state.instance).length > 0 && (
        <div
          role="alert"
          style={{
            background: `${tint(W.warn, 6)}`,
            color: W.warn,
            border: `1px solid ${W.warn}`,
            borderRadius: R.control,
            padding: "6px 10px",
            fontSize: 12,
            marginBottom: 10,
          }}
        >
          {unreachableUIs(state.instance)
            .map((e) => e.label)
            .join(", ")}{" "}
          not serving HTTP. Usually a stale port overlay from an instance
          created by an older DevKit. Use <strong>Recreate</strong> (or re-run{" "}
          <code style={{ fontFamily: wMono }}>
            dpm localnet up --name {name}
          </code>
          ) to regenerate its overlays.
        </div>
      )}

      {state.kind === "loading" && showSkeleton && <DetailGridLoading />}
      {state.kind === "err" && (
        <div role="alert" style={{ color: W.err, fontSize: 13 }}>{state.error}</div>
      )}
      {state.kind === "ok" && <DetailGrid instance={state.instance} />}
      {/* Rendered even on loading/error so the user can still take a
          snapshot of a mostly-broken instance for support tickets. */}
      <BackupRestore instanceName={name} />
    </section>
  );
}

function DetailGrid({ instance }: { instance: Instance }) {
  // Identity first, then runtime, then on-disk locations. `mono` marks
  // the machine-string rows (ids, paths, network names) so plain-prose
  // values like status/uptime aren't forced into the monospace column.
  const rows: Array<[string, React.ReactNode, boolean]> = [
    ["splice", instance.splice_version, true],
    ["status", <StatusBadge status={instance.status} />, false],
    ["created", instance.created_at, true],
    ["uptime", instance.uptime ?? "—", false],
    ["compose project", instance.compose_project, true],
    ["docker network", instance.docker_network, true],
    ["container prefix", instance.container_prefix, true],
    ["project dir", instance.project_dir, true],
    ["data dir", instance.data_dir, true],
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
      {rows.map(([k, v, mono]) => (
        <div key={String(k)} style={{ display: "contents" }}>
          <div style={{ color: W.dim }}>{k}</div>
          <div
            style={{
              color: W.text2,
              fontFamily: mono ? wMono : undefined,
              fontVariantNumeric: mono ? "tabular-nums" : undefined,
              wordBreak: mono ? "break-all" : undefined,
            }}
          >
            {v}
          </div>
        </div>
      ))}
    </div>
  );
}

// DetailGridLoading — same 160px / 1fr rhythm as the real grid so the
// values slot in without a jump.
function DetailGridLoading() {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "160px 1fr",
        rowGap: 10,
        columnGap: 16,
      }}
    >
      {Array.from({ length: 6 }).map((_, r) => (
        <div key={r} style={{ display: "contents" }}>
          <SkeletonBar width="60%" height={11} />
          <SkeletonBar width={r % 2 === 0 ? "42%" : "70%"} height={11} />
        </div>
      ))}
    </div>
  );
}

// ActionButton dispatches the right verb(s) per instance status.
// Registry status alone isn't enough — docker truth may diverge:
//
//   - running/paused → Pause/Resume + Recreate + Stop + Down
//   - failed/partial → Recreate + Down + Remove (containers MAY still
//                      be up even though the orchestrator gave up;
//                      compose down no-ops cleanly if not)
//   - stopped        → Start + Down + Remove
//   - creating/other → no button (CreatingPanel owns that surface)
//
// Stop (docker compose stop) is the gentle halt — containers are kept
// so Start is fast. Down (docker compose down) removes containers; a
// following Start recreates them via up. On failed/partial, Down is
// labeled "Down containers" to signal a force-cleanup.
function ActionButton({
  status,
  busy,
  onStart,
  onStop,
  onDown,
  onPause,
  onResume,
  onRemove,
  onRecreate,
}: {
  status: string;
  busy: boolean;
  onStart: () => void;
  onStop: () => void;
  onDown: () => void;
  onPause: () => void;
  onResume: () => void;
  onRemove: () => void;
  onRecreate: () => void;
}) {
  if (status === "running" || status === "paused") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        {status === "running" ? (
          <Button
            variant="secondary"
            icon={<IcPause />}
            onClick={onPause}
            disabled={busy}
            title="Freeze containers (docker compose pause) — hold state + ports, free CPU. Resume is instant."
          >
            {busy ? "…" : "Pause"}
          </Button>
        ) : (
          <Button
            variant="primary"
            icon={<IcPlay />}
            onClick={onResume}
            disabled={busy}
            title="Resume frozen containers (docker compose unpause) — no boot cost."
          >
            {busy ? "…" : "Resume"}
          </Button>
        )}
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={onRecreate}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
        >
          {busy ? "…" : "Recreate"}
        </Button>
        <Button
          variant="secondary"
          icon={<IcStop />}
          onClick={onStop}
          disabled={busy}
          title="Gracefully stop (docker compose stop) — processes exit and free CPU/memory, but containers are kept for a fast Start. Data volumes preserved."
        >
          {busy ? "Stopping…" : "Stop"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Tear down (docker compose down) — remove containers and networks. Data volumes preserved; Start will recreate them."
        >
          {busy ? "…" : "Down"}
        </Button>
      </div>
    );
  }
  if (status === "failed" || status === "partial") {
    // Recreate is offered because failed/partial often comes from a
    // transient compose hiccup that a clean down + up resolves
    // without losing the instance metadata.
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={onRecreate}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
        >
          {busy ? "…" : "Recreate"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Force docker compose down — use if containers are still running. Data volumes preserved."
        >
          {busy ? "…" : "Down containers"}
        </Button>
        <Button
          variant="ghost"
          icon={<IcX />}
          onClick={onRemove}
          disabled={busy}
          title="Remove the registry entry only. Won't touch docker — run Down first if containers are live."
        >
          {busy ? "Removing…" : "Remove entry"}
        </Button>
      </div>
    );
  }
  if (status === "stopped") {
    // Start is intelligent: a fast `docker compose start` when the
    // containers are still present, or a full up (reusing the recorded
    // version + profiles) when they were removed by a Down.
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <Button
          variant="primary"
          icon={<IcPlay />}
          onClick={onStart}
          disabled={busy}
          title="Start the instance. Fast when containers are present; otherwise recreates them with the recorded Splice version and ports."
        >
          {busy ? "Starting…" : "Start"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Tear down stopped containers (docker compose down) — remove preserved containers and networks. Data volumes preserved; Start will recreate them."
        >
          {busy ? "…" : "Down"}
        </Button>
        <Button
          variant="ghost"
          icon={<IcX />}
          onClick={onRemove}
          disabled={busy}
          title="Remove the registry entry + state.json. Docker volumes (if any) untouched."
        >
          {busy ? "Removing…" : "Remove entry"}
        </Button>
      </div>
    );
  }
  return null;
}
