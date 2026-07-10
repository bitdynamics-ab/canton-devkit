import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ApiError,
  fetchTransactions,
  type InstanceSummary,
  type TransactionEvent,
  type TransactionRow,
} from "../api";
import { W, wMono, tableCaps, tint, R } from "../tokens";
import { Button } from "../components/Button";
import { IcPlus, IcRefresh } from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import { MonoId } from "../components/MonoId";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { ContainerHealth } from "./ContainerHealth";
import { CreateLocalNetModal } from "./CreateLocalNetModal";
import { CreatingPanel } from "./CreatingPanel";
import { DeveloperSetup } from "./DeveloperSetup";
import { InstanceDetail } from "./InstanceDetail";

// Dashboard — the Overview screen. Renders the registered-instance
// table from GET /api/instances.
//
// Selection state lives in the URL (?instance=<name>) via
// useInstanceSelection so the topbar switcher and Dashboard agree on a
// single source of truth — and so shared links preserve the user's
// pick.
export function Dashboard() {
  const sel = useInstanceSelection();
  const [createOpen, setCreateOpen] = useState(false);
  // Gate the skeleton so a fast local fetch never flashes it.
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
        <h1 style={{ fontSize: 20, fontWeight: 600, marginTop: 0, marginBottom: 0 }}>
          LocalNet instances
        </h1>
        <Button
          // The empty-state hero CTA is the primary while the list is
          // empty — never two cobalt fills for the same action at once.
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
            // Refresh the list and promote the new instance to the
            // URL-driven selection so the detail card pops when the
            // modal closes. useCallback'd so the modal's done-effect
            // doesn't see a new identity each render and refire.
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
                fontSize: 12,
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
        // Mid-bring-up: show the live progress panel above the static
        // detail and hide the JWT generator — no point signing tokens
        // for an instance that isn't running yet.
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
            {!isCreating && selectedRow?.status === "running" && (
              <RecentActivity name={sel.selected} />
            )}
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
        borderRadius: R.card,
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
                  // Flat active fill — no accent side-bar, no padding
                  // swap, so the row never shifts on selection.
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

// Table loading placeholder — mirrors the four-column instance table so
// rows arrive in place instead of jumping in after a spinner.
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
  fontSize: 11,
};

const td: React.CSSProperties = {
  padding: "10px 12px",
  verticalAlign: "middle",
};

// Numeric / mono columns (ports, versions): right-aligned, tabular so
// digits line up column-to-column.
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
      <p style={{ marginTop: 0, fontSize: 14, color: W.text }}>
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
      <p style={{ marginBottom: 0, fontSize: 11.5 }}>
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

// RecentActivity — instance-scoped ledger activity on the Overview.
// Reuses GET /api/instances/{name}/transactions (the same offset-window
// scan the Explorer and CLI `tx ls` use), flattened to one row per
// ledger event and projected through the app-provider participant. A
// snapshot with manual refresh — the transactions endpoint is a window
// scan, not an SSE stream.
function RecentActivity({ name }: { name: string }) {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; rows: TransactionRow[]; truncated: boolean }
    | { kind: "needs-jwt" }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchTransactions(name, "app-provider", 40)
      .then((r) => {
        if (cancelled) return;
        setState({ kind: "ok", rows: r.transactions, truncated: !!r.window_truncated });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({ kind: "needs-jwt" });
          return;
        }
        setState({ kind: "err", error: e instanceof ApiError ? e.message : "failed to load activity" });
      });
    return () => {
      cancelled = true;
    };
  }, [name, tick]);

  const events = useMemo(() => {
    if (state.kind !== "ok") return [];
    const flat: { offset: number; key: string; time: string; kind: TransactionEvent["kind"]; event: string; cid: string }[] = [];
    for (const tx of state.rows) {
      if (tx.kind !== "transaction" || !tx.events) continue;
      const time = tx.record_time ? tx.record_time.replace("T", " ").slice(11, 19) : `@${tx.offset}`;
      tx.events.forEach((ev, i) => {
        flat.push({
          offset: tx.offset,
          key: `${tx.offset}:${i}`,
          time,
          kind: ev.kind,
          event: shortTemplate(ev.template),
          cid: ev.contract_id,
        });
      });
    }
    flat.sort((a, b) => b.offset - a.offset);
    return flat.slice(0, 14);
  }, [state]);

  const kindColor: Record<TransactionEvent["kind"], string> = {
    create: W.ok,
    exercise: W.info,
    archive: W.err,
  };

  return (
    <section
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        marginBottom: 16,
      }}
    >
      <header style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
        <h3 style={{ margin: 0, fontSize: 14, fontWeight: 600, color: W.text }}>Recent activity</h3>
        <span style={{ color: W.dim, fontSize: 12 }}>
          ledger events · as seen by the app-provider participant
        </span>
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={() => setTick((t) => t + 1)}
          title="Refresh"
          style={{ marginLeft: "auto" }}
        >
          Refresh
        </Button>
      </header>

      {state.kind === "loading" && (
        <div style={{ color: W.dim, fontSize: 13, padding: "8px 0" }}>Scanning recent ledger updates…</div>
      )}
      {state.kind === "needs-jwt" && (
        <div style={{ color: W.dim, fontSize: 12.5, padding: "8px 0" }}>
          Ledger activity needs a party-rights JWT. Splice LocalNet signs user-id tokens by
          default. Open the Explorer to project through a specific party.
        </div>
      )}
      {state.kind === "err" && (
        <div style={{ color: W.dim, fontSize: 12.5, padding: "8px 0" }}>
          {/no jwt recorded/i.test(state.error)
            ? "Ledger activity needs recorded role JWTs. Restart the instance to capture them (older instances predate JWT capture)."
            : `Ledger activity unavailable. ${state.error}.`}{" "}
          Open the Explorer for the full ledger view.
        </div>
      )}
      {state.kind === "ok" && events.length === 0 && (
        <div style={{ color: W.dim, fontSize: 13, padding: "8px 0" }}>
          No recent ledger activity. Run a token or DAR operation to see updates.
        </div>
      )}
      {state.kind === "ok" && events.length > 0 && (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13, marginTop: 8 }}>
          <thead>
            <tr style={{ color: W.dim, textAlign: "left" }}>
              <th style={actTh}>TIME</th>
              <th style={actTh}>KIND</th>
              <th style={actTh}>EVENT</th>
              <th style={actTh}>CID</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.key} style={{ borderTop: `1px solid ${W.border}` }}>
                <td style={{ ...actTd, color: W.dim, fontFamily: wMono, fontVariantNumeric: "tabular-nums", fontSize: 11 }}>{e.time}</td>
                <td style={actTd}>
                  <span
                    style={{
                      color: kindColor[e.kind],
                      border: `1px solid ${tint(kindColor[e.kind], 34)}`,
                      background: tint(kindColor[e.kind], 13),
                      borderRadius: R.control,
                      padding: "1px 6px",
                      fontSize: 11,
                    }}
                  >
                    {e.kind}
                  </span>
                </td>
                <td style={{ ...actTd, fontFamily: wMono, color: W.text2 }}>{e.event}</td>
                <td style={actTd}>
                  <MonoId value={e.cid} head={6} tail={6} size={11} color={W.info} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {state.kind === "ok" && state.truncated && (
        <div style={{ color: W.dim, fontSize: 11, marginTop: 8 }}>
          Showing the newest events of a clipped scan.
        </div>
      )}
    </section>
  );
}

// shortTemplate drops the package-id prefix from a fully-qualified
// template id (`<pkg>:Module:Entity` → `Module:Entity`) for a compact,
// readable EVENT column.
function shortTemplate(t?: string): string {
  if (!t) return "—";
  const parts = t.split(":");
  return parts.length >= 3 ? `${parts[parts.length - 2]}:${parts[parts.length - 1]}` : t;
}

const actTh: React.CSSProperties = { ...tableCaps, padding: "6px 10px 6px 0", fontSize: 11 };
const actTd: React.CSSProperties = { padding: "8px 10px 8px 0", verticalAlign: "middle" };
