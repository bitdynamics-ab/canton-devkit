import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ApiError,
  fetchTransactions,
  type InstanceSummary,
  type TransactionEvent,
  type TransactionRow,
} from "../api";
import { W, wMono } from "../tokens";
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

      {sel.loading && <p style={{ color: W.dim }}>Loading…</p>}

      {sel.error && <ErrorPanel error={sel.error} />}

      {!sel.loading && !sel.error && (
        <>
          {sel.stale && (
            <div
              role="status"
              style={{
                background: `${W.dim}1A`,
                border: `1px solid ${W.dim}`,
                color: W.dim,
                borderRadius: 8,
                padding: "6px 12px",
                marginBottom: 12,
                fontSize: 12,
              }}
            >
              Couldn’t refresh — showing last known state.
            </div>
          )}
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
        borderRadius: 10,
        padding: 16,
        marginBottom: 16,
      }}
    >
      <header style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
        <h3 style={{ margin: 0, fontSize: 14, color: W.text }}>Recent activity</h3>
        <span style={{ color: W.dim, fontSize: 12 }}>
          ledger events · as seen by the app-provider participant
        </span>
        <button
          onClick={() => setTick((t) => t + 1)}
          title="Refresh"
          style={{
            marginLeft: "auto",
            background: "transparent",
            border: `1px solid ${W.border}`,
            color: W.text2,
            borderRadius: 6,
            padding: "3px 10px",
            fontSize: 12,
            cursor: "pointer",
          }}
        >
          ↻ Refresh
        </button>
      </header>

      {state.kind === "loading" && (
        <div style={{ color: W.dim, fontSize: 13, padding: "8px 0" }}>Scanning recent ledger updates…</div>
      )}
      {state.kind === "needs-jwt" && (
        <div style={{ color: W.dim, fontSize: 12.5, padding: "8px 0" }}>
          Ledger activity needs a party-rights JWT — Splice LocalNet signs user-id tokens by
          default. Open the Explorer to project through a specific party.
        </div>
      )}
      {state.kind === "err" && (
        <div style={{ color: W.dim, fontSize: 12.5, padding: "8px 0" }}>
          {/no jwt recorded/i.test(state.error)
            ? "Ledger activity needs recorded role JWTs — restart the instance to capture them (older instances predate JWT capture)."
            : `Ledger activity unavailable — ${state.error}.`}{" "}
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
                <td style={{ ...actTd, color: W.dim, fontFamily: wMono, fontSize: 11 }}>{e.time}</td>
                <td style={actTd}>
                  <span
                    style={{
                      color: kindColor[e.kind],
                      border: `1px solid ${kindColor[e.kind]}`,
                      borderRadius: 4,
                      padding: "1px 6px",
                      fontSize: 11,
                      textTransform: "uppercase",
                    }}
                  >
                    {e.kind}
                  </span>
                </td>
                <td style={{ ...actTd, fontFamily: wMono, color: W.text2 }}>{e.event}</td>
                <td style={{ ...actTd, fontFamily: wMono, color: W.info, fontSize: 11 }}>
                  {e.cid.slice(0, 10)}…
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

const actTh: React.CSSProperties = { padding: "6px 10px 6px 0", fontWeight: 500, fontSize: 11, letterSpacing: 0.4 };
const actTd: React.CSSProperties = { padding: "8px 10px 8px 0", verticalAlign: "middle" };
