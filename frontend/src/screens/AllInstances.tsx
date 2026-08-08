import { useNavigate } from "react-router-dom";
import { type InstanceSummary } from "../api";
import { W, wMono, tableCaps, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { IcPlus, IcRefresh, IcArrowRight } from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { useCreateInstance } from "../shell/useCreateInstance";

// All-instances view: the multi-instance management surface, reached
// from the topbar switcher's "All instances" link. The Overview ("/")
// is detail-only for the selected instance; this is where you scan the
// whole fleet, see the census, and jump into one.
export function AllInstances() {
  const sel = useInstanceSelection();
  const create = useCreateInstance();
  const navigate = useNavigate();
  const showSkeleton = useLoadingDelay(sel.loading);

  // Selecting from here promotes the instance and drops the user on the
  // detail-only Overview. Navigate WITH ?instance= so the selection rides
  // the URL (the source of truth) — calling select() then navigate("/")
  // would strip the param and let auto-pick choose a different instance.
  const open = (name: string) => {
    navigate(`/?instance=${encodeURIComponent(name)}`);
  };

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
          variant={sel.instances.length === 0 ? "secondary" : "primary"}
          icon={<IcPlus />}
          onClick={create.open}
        >
          New instance
        </Button>
      </header>

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
            <EmptyState onCreate={create.open} />
          ) : (
            <>
              <CensusBar
                instances={sel.instances}
                loading={sel.loading}
                onRefresh={sel.refresh}
              />
              <InstanceTable
                instances={sel.instances}
                selected={sel.selected}
                onOpen={open}
              />
            </>
          )}
        </>
      )}
    </div>
  );
}

// Bucket statuses into the three census categories. Anything not clearly
// up or down (creating/starting/…) counts toward neither running nor stopped
// but still toward the registered total.
function census(instances: InstanceSummary[]) {
  let running = 0;
  let stopped = 0;
  let failed = 0;
  for (const i of instances) {
    const s = i.status.toLowerCase();
    if (s === "running" || s === "healthy" || s === "ready" || s === "live") running++;
    else if (s === "stopped" || s === "exited" || s === "idle") stopped++;
    else if (s === "failed" || s === "error" || s === "dead") failed++;
  }
  return { registered: instances.length, running, stopped, failed };
}

// Status census + Refresh, mirroring `dpm localnet list`'s summary line.
function CensusBar({
  instances,
  loading,
  onRefresh,
}: {
  instances: InstanceSummary[];
  loading: boolean;
  onRefresh: () => void;
}) {
  const c = census(instances);
  const parts = [
    `${c.registered} registered`,
    `${c.running} running`,
    `${c.stopped} stopped`,
  ];
  if (c.failed > 0) parts.push(`${c.failed} failed`);
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        marginBottom: 10,
      }}
    >
      <span style={{ color: W.dim, fontSize: fs.meta, fontVariantNumeric: "tabular-nums" }}>
        {parts.join(" · ")}
      </span>
      {c.failed > 0 && (
        <span style={{ color: W.err, fontSize: fs.meta }}>
          — run <code style={{ fontFamily: wMono }}>dpm localnet doctor &lt;name&gt;</code>
        </span>
      )}
      <Button
        variant="ghost"
        size="sm"
        icon={<IcRefresh />}
        onClick={onRefresh}
        disabled={loading}
        style={{ marginLeft: "auto" }}
      >
        Refresh
      </Button>
    </div>
  );
}

interface InstanceTableProps {
  instances: InstanceSummary[];
  selected: string | null;
  onOpen: (name: string) => void;
}

function InstanceTable({ instances, selected, onOpen }: InstanceTableProps) {
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
            <th style={th}>Uptime</th>
            <th style={{ ...th, textAlign: "right" }}>Splice</th>
            <th style={{ ...th, textAlign: "right" }}>Ports</th>
            <th style={{ ...th, textAlign: "right" }} aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {instances.map((i) => {
            const isSel = i.name === selected;
            return (
              <tr
                key={i.name}
                onClick={() => onOpen(i.name)}
                style={{
                  borderTop: `1px solid ${W.border}`,
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
                <td style={{ ...td, color: W.dim, fontVariantNumeric: "tabular-nums" }}>
                  {i.started_ago || "—"}
                </td>
                <td style={{ ...td, ...numCell, color: W.text2 }}>
                  {i.splice_version}
                </td>
                <td style={{ ...td, ...numCell, color: W.text2 }}>{i.ports}</td>
                <td style={{ ...td, textAlign: "right" }}>
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<IcArrowRight />}
                    onClick={(e) => {
                      e.stopPropagation();
                      onOpen(i.name);
                    }}
                    aria-label={`Open ${i.name}`}
                  >
                    Open
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "8px 12px",
          borderTop: `1px solid ${W.border}`,
          background: W.surface2,
        }}
      >
        <code style={{ fontFamily: wMono, fontSize: fs.label, color: W.faint }}>
          dpm localnet list
        </code>
        <span style={{ color: W.faint, fontSize: fs.label, fontVariantNumeric: "tabular-nums" }}>
          {instances.length} {instances.length === 1 ? "instance" : "instances"}
        </span>
      </div>
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
      <SkeletonTable columns={[2, 1.4, 1, 1, 1.4, 0.6]} rows={3} rowHeight={40} />
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
