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

function unreachableUIs(inst: Instance): Endpoint[] {
  return (inst.endpoints ?? []).filter(
    (e) => e.reachability === "unreachable",
  );
}

interface Props {
  name: string;
  // From the dashboard's fresh list; gates which action button renders.
  // Falls back to this card's own fetched status when omitted.
  statusHint?: string;
  onChanged?: () => void;
}

export function InstanceDetail({ name, statusHint, onChanged }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Bumped after an action so the cached instance.status is refetched.
  const [refetchTick, setRefetchTick] = useState(0);
  const [stopping, setStopping] = useState<
    | { kind: "idle" }
    | { kind: "running" }
    | { kind: "err"; message: string }
  >({ kind: "idle" });
  const showSkeleton = useLoadingDelay(state.kind === "loading");

  async function onStop() {
    // docker compose stop keeps containers for a fast Start; no confirm needed.
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
      // 202 async (down → up); refresh eagerly to show the transitional status.
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
      // 204 → fast start done; 202 → full bring-up (containers had been removed).
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
      // No setRefetchTick — the entry is gone; the parent's refresh drops this card.
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to remove";
      setStopping({ kind: "err", message: msg });
      onChanged?.();
    }
  }

  useEffect(() => {
    let cancelled = false;
    // Only blank to loading on a name-change mount, not a refetchTick bump.
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
      {/* Rendered even on loading/error so a broken instance can still be snapshotted. */}
      <BackupRestore instanceName={name} />
    </section>
  );
}

function DetailGrid({ instance }: { instance: Instance }) {
  // `mono` marks machine-string rows so prose values (status/uptime) stay proportional.
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

// Dispatches verbs per status; on failed/partial containers MAY still be
// up (compose down no-ops cleanly if not), so Down is offered there too.
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
