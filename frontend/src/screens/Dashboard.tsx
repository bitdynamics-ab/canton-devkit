import { useCallback, useState } from "react";
import { type InstanceSummary } from "../api";
import { W, wMono, tableCaps, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { IcPlus } from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { CreateLocalNetModal } from "./CreateLocalNetModal";
import { CreatingPanel } from "./CreatingPanel";
import { InstanceOverview } from "./InstanceOverview";

// Selection state lives in the URL (?instance=<name>) so the topbar
// switcher and Dashboard share one source of truth and links survive.
export function Dashboard() {
  const sel = useInstanceSelection();
  const [createOpen, setCreateOpen] = useState(false);
  const showSkeleton = useLoadingDelay(sel.loading);

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
        <h1 style={{ fontSize: fs.title, fontWeight: 600, marginTop: 0, marginBottom: 0 }}>
          LocalNet instances
        </h1>
        <Button
          // Steps down to secondary when the empty-state hero CTA owns primary.
          variant={sel.instances.length === 0 ? "secondary" : "primary"}
          icon={<IcPlus />}
          onClick={() => setCreateOpen(true)}
        >
          New instance
        </Button>
      </header>

      <CreateLocalNetModal
        open={createOpen}
        onClose={useCallback(() => setCreateOpen(false), [])}
        onCreated={useCallback(
          (name: string) => {
            // useCallback'd so the modal's done-effect keeps a stable
            // identity and doesn't refire each render.
            sel.refresh();
            sel.select(name);
          },
          [sel.refresh, sel.select],
        )}
      />

      {sel.loading && showSkeleton && <InstanceTableLoading />}

      {sel.error && <ErrorPanel error={sel.error} />}

      {!sel.loading && !sel.error && (
        <>
          {sel.stale && (
            <div
              role="status"
              style={{
                background: `${tint(W.dim, 10)}`,
                border: `1px solid ${W.dim}`,
                color: W.dim,
                borderRadius: R.control,
                padding: "6px 12px",
                marginBottom: 12,
                fontSize: fs.meta,
              }}
            >
              Couldn’t refresh. Showing last known state.
            </div>
          )}
          {sel.warning && (
            <div
              style={{
                background: `${tint(W.warn, 10)}`,
                border: `1px solid ${W.warn}`,
                color: W.warn,
                borderRadius: R.control,
                padding: "8px 12px",
                marginBottom: 16,
                fontSize: fs.data,
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
        // While creating, show the live progress panel above the overview —
        // the overview's own tabs handle the running vs. not-running gate.
        const selectedRow = sel.instances.find((i) => i.name === sel.selected);
        const isCreating = selectedRow?.status === "creating";
        return (
          <>
            {isCreating && (
              <CreatingPanel name={sel.selected} onRefresh={sel.refresh} />
            )}
            <InstanceOverview
              name={sel.selected}
              statusHint={selectedRow?.status}
              ports={selectedRow?.ports}
              onChanged={sel.refresh}
            />
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
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          fontSize: fs.data,
        }}
      >
        <thead>
          <tr style={{ background: W.surface2, color: W.dim, textAlign: "left" }}>
            <th style={th}>Name</th>
            <th style={th}>State</th>
            <th style={{ ...th, textAlign: "right" }}>Splice</th>
            <th style={{ ...th, textAlign: "right" }}>Ports</th>
          </tr>
        </thead>
        <tbody>
          {instances.map((i) => {
            const isSel = i.name === selected;
            return (
              <tr
                key={i.name}
                onClick={() => onSelect(i.name)}
                style={{
                  borderTop: `1px solid ${W.border}`,
                  // Flat fill, no padding swap, so the row never shifts on select.
                  background: isSel ? W.selRow : undefined,
                  cursor: "pointer",
                }}
              >
                <td style={td}>
                  <strong style={{ color: isSel ? W.brand : W.text }}>
                    {i.name}
                  </strong>
                </td>
                <td style={td}>
                  <StatusBadge status={i.status} />
                </td>
                <td style={{ ...td, ...numCell, color: W.text2 }}>
                  {i.splice_version}
                </td>
                <td style={{ ...td, ...numCell, color: W.text2 }}>{i.ports}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function InstanceTableLoading() {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <SkeletonTable columns={[2, 1.4, 1, 1.4]} rows={3} rowHeight={40} />
    </div>
  );
}

const th: React.CSSProperties = {
  ...tableCaps,
  padding: "8px 12px",
  fontSize: fs.label,
};

const td: React.CSSProperties = {
  padding: "10px 12px",
  verticalAlign: "middle",
};

const numCell: React.CSSProperties = {
  textAlign: "right",
  fontFamily: wMono,
  fontVariantNumeric: "tabular-nums",
};

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        color: W.dim,
      }}
    >
      <p style={{ marginTop: 0, fontSize: fs.body, color: W.text }}>
        No LocalNet instances yet.
      </p>
      <Button
        variant="primary"
        size="md"
        icon={<IcPlus />}
        onClick={onCreate}
        style={{ margin: "8px 0 12px" }}
      >
        Create your first instance
      </Button>
      <p style={{ marginBottom: 0, fontSize: fs.label }}>
        Or run{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>
          dpm localnet up --name demo
        </code>{" "}
        in your terminal.
      </p>
    </div>
  );
}

function ErrorPanel({ error }: { error: string }) {
  return (
    <div
      role="alert"
      style={{
        background: `${tint(W.err, 10)}`,
        border: `1px solid ${W.err}`,
        borderRadius: R.card,
        padding: 16,
        color: W.err,
      }}
    >
      <strong>Failed to load instances</strong>
      <div style={{ color: W.text2, marginTop: 6 }}>{error}</div>
    </div>
  );
}
