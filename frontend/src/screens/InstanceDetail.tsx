import { useEffect, useState } from "react";
import { ApiError, type Instance, fetchInstance } from "../api";
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
export function InstanceDetail({ name }: { name: string }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

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
              marginLeft: "auto",
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
      </header>

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
