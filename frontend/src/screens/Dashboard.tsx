import { useCallback, useState } from "react";
import { type InstanceSummary } from "../api";
import { W } from "../tokens";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { ContainerHealth } from "./ContainerHealth";
import { CreateLocalNetModal } from "./CreateLocalNetModal";
import { CreatingPanel } from "./CreatingPanel";
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
// (publishes "instances" topic events when an instance's
// status flips — needs a producer in internal/localnet first).
export function Dashboard() {
  const sel = useInstanceSelection();
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div>
      <header
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          marginBottom: 12,
        }}
      >
        <h1 style={{ fontSize: 20, fontWeight: 600, marginTop: 0, marginBottom: 0 }}>
          LocalNet instances
        </h1>
        <button
          onClick={() => setCreateOpen(true)}
          style={{
            background: W.brand,
            color: "#082018",
            border: `1px solid ${W.brand}`,
            borderRadius: 6,
            padding: "6px 14px",
            fontSize: 12,
            fontWeight: 600,
            cursor: "pointer",
          }}
        >
          + New instance
        </button>
      </header>

      <CreateLocalNetModal
        open={createOpen}
        onClose={useCallback(() => setCreateOpen(false), [])}
        onCreated={useCallback(
          (name: string) => {
            // After a successful create, refresh the list and
            // promote the new instance to the URL-driven selection
            // so the detail card pops the moment the modal closes.
            //
            // useCallback'd so the modal's done-effect doesn't
            // see this as a new identity each render and refire.
            // Deps cover both sel.refresh + sel.select since
            // they come from the context value.
            sel.refresh();
            sel.select(name);
          },
          [sel.refresh, sel.select],
        )}
      />

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
            <EmptyState onCreate={() => setCreateOpen(true)} />
          ) : (
            <InstanceTable
              instances={sel.instances}
              selected={sel.selected}
              onSelect={sel.select}
            />
          )}
        </>
      )}

      {sel.selected && (() => {
        // When the selected instance is mid-bring-up, surface
        // the live progress panel above the static detail. The
        // JWT generator is hidden while creating — no point
        // signing tokens for an instance that's not running yet.
        const selectedRow = sel.instances.find((i) => i.name === sel.selected);
        const isCreating = selectedRow?.status === "creating";
        return (
          <>
            {isCreating && (
              <CreatingPanel name={sel.selected} onRefresh={sel.refresh} />
            )}
            <InstanceDetail
              name={sel.selected}
              statusHint={selectedRow?.status}
              onChanged={sel.refresh}
            />
            <ContainerHealth name={sel.selected} />
            {!isCreating && <DeveloperSetup name={sel.selected} />}
          </>
        );
      })()}
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

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 8,
        padding: 32,
        color: W.dim,
        textAlign: "center",
      }}
    >
      <p style={{ marginTop: 0, fontSize: 14, color: W.text }}>
        No LocalNet instances yet.
      </p>
      <button
        onClick={onCreate}
        style={{
          background: W.brand,
          color: "#082018",
          border: `1px solid ${W.brand}`,
          borderRadius: 6,
          padding: "8px 18px",
          fontSize: 13,
          fontWeight: 600,
          cursor: "pointer",
          margin: "8px 0 12px",
        }}
      >
        + Create your first instance
      </button>
      <p style={{ marginBottom: 0, fontSize: 11.5 }}>
        Or run{" "}
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
