import { type InstanceSummary } from "../api";
import { W } from "../tokens";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { DeveloperSetup } from "./DeveloperSetup";
import { InstanceDetail } from "./InstanceDetail";

// Dashboard — Overview screen. Mirrors the LocalNet table at the
// top of docs/design/mockups/webui-dashboard.jsx. Renders the
// list of registered instances pulled from GET /api/instances.
//
// Selection state lives in the URL (?instance=<name>) via
// useInstanceSelection so the topbar switcher and Dashboard
// agree on a single source of truth — and so shared links
// preserve the user's pick. Pre-lift this lived in local
// useState; the topbar couldn't see it.
//
// SSE wiring for live updates is deferred to a follow-on slice
// (BIT-130 publishes "instances" topic events when an instance's
// status flips — needs a producer in internal/localnet first).
export function Dashboard() {
  const sel = useInstanceSelection();

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, marginTop: 0 }}>
        LocalNet instances
      </h1>

      {sel.loading && <p style={{ color: W.dim }}>Loading…</p>}

      {sel.error && <ErrorPanel error={sel.error} />}

      {!sel.loading && !sel.error && (
        <>
          {sel.warning && (
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
              {sel.warning}
            </div>
          )}
          {sel.instances.length === 0 ? (
            <EmptyState />
          ) : (
            <InstanceTable
              instances={sel.instances}
              selected={sel.selected}
              onSelect={sel.select}
            />
          )}
        </>
      )}

      {sel.selected && (
        <>
          <InstanceDetail name={sel.selected} />
          <DeveloperSetup name={sel.selected} />
        </>
      )}
    </div>
  );
}

interface InstanceTableProps {
  instances: InstanceSummary[];
  selected: string | null;
  onSelect: (name: string) => void;
}

function InstanceTable({ instances, selected, onSelect }: InstanceTableProps) {
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
              onClick={() => onSelect(i.name)}
              style={{
                borderTop: `1px solid ${W.border}`,
                background: i.name === selected ? W.surface2 : undefined,
                cursor: "pointer",
              }}
            >
              <td style={td}>
                <strong
                  style={{
                    color: i.name === selected ? W.brand : W.text,
                  }}
                >
                  {i.name}
                </strong>
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

function ErrorPanel({ error }: { error: string }) {
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
    </div>
  );
}
