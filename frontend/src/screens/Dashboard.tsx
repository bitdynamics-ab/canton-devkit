import { useEffect, useState } from "react";
import { ApiError, type InstanceSummary, fetchInstances } from "../api";
import { W } from "../tokens";

// Dashboard — Overview screen. Mirrors the LocalNet table at the
// top of docs/design/mockups/webui-dashboard.jsx. Renders the
// list of registered instances pulled from GET /api/instances.
//
// First slice of BIT-133: list only. The full mockup also shows
// the Developer setup card (JWT generator + app config exporter)
// driven by /api/instances/{name}/jwt and /api/instances/{name}/
// app-config — added in the follow-on slice once this lands.
//
// SSE wiring for live updates also deferred to follow-on (BIT-130
// publishes "instances" topic events when an instance's status
// flips; the dashboard subscribes and refetches).
export function Dashboard() {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instances: InstanceSummary[]; warning?: string }
    | { kind: "err"; error: string; detail?: string }
  >({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    fetchInstances()
      .then((r) => {
        if (!cancelled) {
          setState({
            kind: "ok",
            instances: r.instances,
            warning: r.warning,
          });
        }
      })
      .catch((e) => {
        if (cancelled) return;
        const err = e instanceof ApiError ? e : null;
        setState({
          kind: "err",
          error: err?.message ?? "failed to load instances",
          detail: err?.detail,
        });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, marginTop: 0 }}>
        LocalNet instances
      </h1>

      {state.kind === "loading" && (
        <p style={{ color: W.dim }}>Loading…</p>
      )}

      {state.kind === "err" && (
        <ErrorPanel error={state.error} detail={state.detail} />
      )}

      {state.kind === "ok" && (
        <>
          {state.warning && (
            <div
              style={{
                background: `${W.warn}1A`,
                border: `1px solid ${W.warn}`,
                color: W.warn,
                borderRadius: 8,
                padding: "8px 12px",
                marginBottom: 16,
                fontSize: 13,
              }}
            >
              {state.warning}
            </div>
          )}
          {state.instances.length === 0 ? (
            <EmptyState />
          ) : (
            <InstanceTable instances={state.instances} />
          )}
        </>
      )}
    </div>
  );
}

function InstanceTable({ instances }: { instances: InstanceSummary[] }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 8,
        overflow: "hidden",
      }}
    >
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          fontSize: 13,
        }}
      >
        <thead>
          <tr style={{ background: W.surface2, color: W.dim, textAlign: "left" }}>
            <th style={th}>NAME</th>
            <th style={th}>STATE</th>
            <th style={th}>SPLICE</th>
            <th style={th}>PORTS</th>
          </tr>
        </thead>
        <tbody>
          {instances.map((i) => (
            <tr
              key={i.name}
              style={{
                borderTop: `1px solid ${W.border}`,
              }}
            >
              <td style={td}>
                <strong style={{ color: W.text }}>{i.name}</strong>
              </td>
              <td style={td}>
                <StatusBadge status={i.status} />
              </td>
              <td style={{ ...td, color: W.text2 }}>{i.splice_version}</td>
              <td style={{ ...td, color: W.text2, fontFamily: "monospace" }}>
                {i.ports}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const th: React.CSSProperties = {
  padding: "8px 12px",
  fontWeight: 500,
  fontSize: 11,
  letterSpacing: 0.6,
};

const td: React.CSSProperties = {
  padding: "10px 12px",
  verticalAlign: "middle",
};

function StatusBadge({ status }: { status: string }) {
  const tone = (() => {
    switch (status) {
      case "running":
        return { color: W.ok, glyph: "●" };
      case "creating":
      case "stopping":
      case "partial":
        return { color: W.warn, glyph: "◐" };
      case "failed":
        return { color: W.err, glyph: "⊗" };
      case "stopped":
        return { color: W.dim, glyph: "○" };
      default:
        return { color: W.dim, glyph: "·" };
    }
  })();
  return (
    <span style={{ color: tone.color, display: "inline-flex", gap: 6 }}>
      <span>{tone.glyph}</span>
      <span>{status}</span>
    </span>
  );
}

function EmptyState() {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 8,
        padding: 24,
        color: W.dim,
        textAlign: "center",
      }}
    >
      <p style={{ marginTop: 0 }}>No LocalNet instances yet.</p>
      <p style={{ marginBottom: 0 }}>
        Run{" "}
        <code style={{ color: W.text2 }}>dpm localnet up --name demo</code>{" "}
        in your terminal.
      </p>
    </div>
  );
}

function ErrorPanel({ error, detail }: { error: string; detail?: string }) {
  return (
    <div
      style={{
        background: `${W.err}1A`,
        border: `1px solid ${W.err}`,
        borderRadius: 8,
        padding: 16,
        color: W.err,
      }}
    >
      <strong>Failed to load instances</strong>
      <div style={{ color: W.text2, marginTop: 6 }}>{error}</div>
      {detail && (
        <pre
          style={{
            color: W.dim,
            marginTop: 8,
            marginBottom: 0,
            fontSize: 12,
            whiteSpace: "pre-wrap",
          }}
        >
          {detail}
        </pre>
      )}
    </div>
  );
}
