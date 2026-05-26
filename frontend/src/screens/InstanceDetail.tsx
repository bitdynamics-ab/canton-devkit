import { useEffect, useState } from "react";
import {
  ApiError,
  type Instance,
  fetchInstance,
  scrubInstance,
  stopInstance,
} from "../api";
import { W, wMono } from "../tokens";

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
  // Optional: refresh the dashboard's instance list after a Stop
  // succeeds so the row's status updates (running → stopped) and
  // the DeveloperSetup panel hides.
  onChanged?: () => void;
}

export function InstanceDetail({ name, onChanged }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
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
      await stopInstance(name, /*keepData=*/ true);
      setStopping({ kind: "idle" });
      onChanged?.();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to stop";
      setStopping({ kind: "err", message: msg });
      onChanged?.();
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
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "failed to remove";
      setStopping({ kind: "err", message: msg });
      onChanged?.();
    }
  }

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
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
  }, [name]);

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
        {state.kind === "ok" && <ActionButton
          status={state.instance.status}
          busy={stopping.kind === "running"}
          onStop={onStop}
          onRemove={onRemove}
        />}
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

// ActionButton dispatches the right verb per instance status:
//   - running        → Stop  (POST /down via stopInstance)
//   - stopped/failed → Remove entry (DELETE / via scrubInstance)
//   - creating       → no button (CreatingPanel owns that surface)
//   - other states   → no button (defensive)
//
// The pair-vs-single choice keeps the surface honest: Stop on a
// failed instance would be meaningless (there's nothing live to
// stop), and Remove on a running instance would orphan containers.
// Backend already refuses both cross-cases with 409, but hiding
// the button is the kinder UX than letting the user click and
// get an error toast.
function ActionButton({
  status,
  busy,
  onStop,
  onRemove,
}: {
  status: string;
  busy: boolean;
  onStop: () => void;
  onRemove: () => void;
}) {
  if (status === "running") {
    return (
      <button
        onClick={onStop}
        disabled={busy}
        title="Bring containers down via docker compose. Data volumes preserved."
        style={btnStyle(W.err, busy)}
      >
        {busy ? "Stopping…" : "⏹ Stop"}
      </button>
    );
  }
  if (status === "stopped" || status === "failed" || status === "partial") {
    return (
      <button
        onClick={onRemove}
        disabled={busy}
        title="Remove the registry entry + state.json. Docker volumes (if any) untouched."
        style={btnStyle(W.dim, busy)}
      >
        {busy ? "Removing…" : "✕ Remove entry"}
      </button>
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
