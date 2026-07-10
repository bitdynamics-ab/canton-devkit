import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  fetchContracts,
  fetchTransactions,
  openContractsStream,
  type ContractRow,
  type ContractStreamEvent,
  type ContractsListResponse,
  type Role,
  type TransactionEvent,
  type TransactionFilters,
  type TransactionRow,
  type TransactionsListResponse,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { Button } from "../components/Button";
import { Dot, IcRefresh } from "../components/icons";
import { TX_KIND_COLOR, W, wMono, tableCaps, wideCaps, tint } from "../tokens";
import { ContractDetailDrawer } from "./ContractDetailDrawer";
import { TxReplayDrawer } from "./TxReplayDrawer";

// ExplorerScreen — live Active Contract Set, transaction history, and
// per-party visibility for the selected instance.
//
// The ACS table is a live snapshot + SSE delta stream: an initial
// snapshot fills it, an EventSource applies create/archive deltas, and
// a 30s timer reconciles drift. The Transactions view supports the
// same party/template/offset filters the CLI `tx ls` has, and each
// transaction row can be replayed as a per-party visibility projection
// (the Web UI counterpart of `tx replay`).

const ROLES: Role[] = ["app-user", "app-provider", "sv"];
// Hash palette for template/party dots — the dataviz ramp ordered so
// neighbouring indices never share a hue family, and no danger red
// (red stays reserved for errors).
const PALETTE = [
  "#6480E6", "#7BD2C6", "#DDB25E", "#7CC89A",
  "#93A7F0", "#C8971F", "#189E8C", "#9BA3B5",
];

type View = "contracts" | "transactions" | "timeline";

export function ExplorerScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [view, setView] = useState<View>("contracts");
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: ContractsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [activeTemplates, setActiveTemplates] = useState<Set<string>>(new Set());
  const [activeParties, setActiveParties] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState("");
  const [selectedCid, setSelectedCid] = useState<string | null>(null);
  // Live-stream status: "live" after the first frame, "reconnecting"
  // while the browser retries a dropped connection, "truncated" when
  // the backend hit its event cap.
  const [streamStatus, setStreamStatus] = useState<
    "idle" | "live" | "reconnecting" | "truncated"
  >("idle");
  const searchRef = useRef<HTMLInputElement>(null);

  // refreshSnapshot fills the table from the snapshot endpoint.
  // Background callers (the 30s reconciliation timer, SSE recovery)
  // pass quiet=true so the table repopulates in place without
  // flashing the loading panel; the initial mount uses quiet=false
  // so users see "Snapshotting ACS…" before the first paint.
  const refreshSnapshot = useCallback(
    async (instance: string, asRole: Role, quiet: boolean) => {
      if (!quiet) {
        setState({ kind: "loading" });
        setSelectedCid(null);
      }
      try {
        const data = await fetchContracts(instance, asRole, 500);
        const safe = {
          ...data,
          contracts: (data.contracts ?? []).map((c) => ({
            ...c,
            signatories: c.signatories ?? [],
            observers: c.observers ?? [],
            payload: c.payload ?? {},
          })),
        };
        setState({ kind: "ok", data: safe });
      } catch (e: unknown) {
        if (
          e instanceof ApiError &&
          e.code === "PARTICIPANT_PORT_NOT_RECORDED"
        ) {
          setState({
            kind: "port-missing",
            remediation:
              e.remediation?.[0] ??
              `Restart the instance to capture Canton API ports.`,
          });
          return;
        }
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({
            kind: "needs-jwt",
            remediation: e.remediation?.[0] ?? "Wrap UserManagementService.",
          });
          return;
        }
        if (!quiet) {
          setState({
            kind: "err",
            error: e instanceof ApiError ? e.message : "failed to load ACS",
          });
        }
        // Quiet background failures are swallowed — the user keeps
        // the last-known good state and the next tick retries.
      }
    },
    [],
  );

  useEffect(() => {
    if (!name) return;
    setSelectedCid(null);
    void refreshSnapshot(name, role, false);
  }, [name, role, refreshSnapshot]);

  // Live SSE subscription, mounted once the snapshot has loaded;
  // tears down when the instance/role changes or the screen unmounts.
  // EventSource auto-reconnects on transient failures; the `error`
  // listener triggers a snapshot refetch to recover missed events.
  //
  // Deltas are applied via a Map<contract_id, row>, which dedupes
  // create-then-archive races: an archive arriving before its create
  // removes nothing, so either ordering converges to the same state.
  useEffect(() => {
    if (!name) return;
    if (state.kind !== "ok") return;
    // Resume from the snapshot's `ledger_end` so no events are
    // skipped between the snapshot fetch and the stream open.
    const es = openContractsStream(name, role, state.data.ledger_end);
    let opened = false;
    const onMessage = (raw: MessageEvent) => {
      opened = true;
      setStreamStatus((s) => (s === "truncated" ? s : "live"));
      let payload: ContractStreamEvent;
      try {
        payload = JSON.parse(raw.data) as ContractStreamEvent;
      } catch {
        return;
      }
      if (payload.event === "truncated") {
        setStreamStatus("truncated");
        // Backend stopped sending — reconcile and we'll re-open
        // when the user picks a different instance.
        void refreshSnapshot(name, role, true);
        return;
      }
      if (!payload.contract_id) return;
      setState((prev) => {
        if (prev.kind !== "ok") return prev;
        const map = new Map(
          prev.data.contracts.map((c) => [c.contract_id, c]),
        );
        if (payload.event === "created") {
          map.set(payload.contract_id!, {
            contract_id: payload.contract_id!,
            template_id: payload.template ?? "",
            payload: {},
            signatories: payload.signatories ?? [],
            observers: payload.observers ?? [],
            created_at: payload.at
              ? new Date(payload.at * 1000).toISOString()
              : undefined,
          });
        } else if (payload.event === "archived") {
          map.delete(payload.contract_id!);
        }
        return {
          ...prev,
          data: {
            ...prev.data,
            contracts: [...map.values()],
            ledger_end: payload.offset ?? prev.data.ledger_end,
          },
        };
      });
    };
    es.addEventListener("contracts", onMessage as EventListener);
    es.onerror = () => {
      // EventSource auto-reconnects unless closed. Show the
      // reconnecting state and reconcile via snapshot — the browser
      // may have been suspended (lid-close) for minutes.
      setStreamStatus("reconnecting");
      if (opened) {
        void refreshSnapshot(name, role, true);
      }
    };
    return () => {
      es.removeEventListener("contracts", onMessage as EventListener);
      es.close();
      setStreamStatus("idle");
    };
    // Depend on state.kind (not state) so the subscription is set up
    // once per snapshot transition, not on every contract-list change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, role, state.kind, refreshSnapshot]);

  // Every 30s, quietly re-pull the snapshot to correct any drift the
  // SSE deltas missed (network hiccups, browser suspend, restarts).
  useEffect(() => {
    if (!name) return;
    if (state.kind !== "ok") return;
    const id = window.setInterval(() => {
      void refreshSnapshot(name, role, true);
    }, 30_000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, role, state.kind, refreshSnapshot]);

  // Keyboard: "/" focuses search (unless typing in an editable
  // element); Esc clears the selection.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const active = document.activeElement as HTMLElement | null;
      const inEditable =
        active &&
        (active.tagName === "INPUT" ||
          active.tagName === "TEXTAREA" ||
          active.isContentEditable);
      if (e.key === "/" && !inEditable) {
        e.preventDefault();
        searchRef.current?.focus();
      } else if (e.key === "Escape" && selectedCid) {
        setSelectedCid(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedCid]);

  // Derive template + party facets from the (unfiltered) ACS.
  const facets = useMemo(() => {
    if (state.kind !== "ok") return { templates: [], parties: [] };
    const tpl = new Map<string, number>();
    const pty = new Map<string, number>();
    for (const c of state.data.contracts) {
      tpl.set(c.template_id, (tpl.get(c.template_id) ?? 0) + 1);
      for (const p of c.signatories) pty.set(p, (pty.get(p) ?? 0) + 1);
    }
    const colored = (m: Map<string, number>) =>
      [...m.entries()]
        .sort((a, b) => b[1] - a[1])
        .map(([k, v], i) => ({ k, v, color: PALETTE[i % PALETTE.length] }));
    return { templates: colored(tpl), parties: colored(pty) };
  }, [state]);

  // Filter the ACS in render. Search matches template, cid, payload JSON, party.
  const filtered = useMemo(() => {
    if (state.kind !== "ok") return [];
    const needle = search.trim().toLowerCase();
    return state.data.contracts.filter((c) => {
      if (activeTemplates.size > 0 && !activeTemplates.has(c.template_id)) return false;
      if (
        activeParties.size > 0 &&
        !c.signatories.some((p) => activeParties.has(p)) &&
        !c.observers.some((p) => activeParties.has(p))
      )
        return false;
      if (!needle) return true;
      const hay = (
        c.template_id +
        " " +
        c.contract_id +
        " " +
        JSON.stringify(c.payload ?? {}) +
        " " +
        c.signatories.join(" ") +
        " " +
        c.observers.join(" ")
      ).toLowerCase();
      return hay.includes(needle);
    });
  }, [state, activeTemplates, activeParties, search]);

  const selected = useMemo(
    () =>
      state.kind === "ok"
        ? state.data.contracts.find((c) => c.contract_id === selectedCid) ?? null
        : null,
    [state, selectedCid],
  );

  // j/k navigation over the *filtered* view so the user follows what
  // they see, not the underlying ACS order. The drawer registers its
  // own keydown listener (Esc + j/k) and invokes these callbacks.
  const goPrev = useCallback(() => {
    if (!selectedCid) return;
    const i = filtered.findIndex((c) => c.contract_id === selectedCid);
    if (i > 0) setSelectedCid(filtered[i - 1].contract_id);
  }, [filtered, selectedCid]);
  const goNext = useCallback(() => {
    if (!selectedCid) return;
    const i = filtered.findIndex((c) => c.contract_id === selectedCid);
    if (i >= 0 && i < filtered.length - 1) {
      setSelectedCid(filtered[i + 1].contract_id);
    }
  }, [filtered, selectedCid]);

  if (!name) {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.dim }}>
          No instance selected. Create or pick one from the dashboard first.
        </p>
      </section>
    );
  }

  return (
    <section style={{ padding: 24 }}>
      <header style={{ marginBottom: 10 }}>
        <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>Explorer</h2>
        <p style={{ color: W.dim, fontSize: 12.5, margin: "3px 0 0" }}>
          Live Active Contract Set, transaction history, and per-party visibility.
        </p>
      </header>

      <ProjectionBar
        instance={name}
        role={role}
        onRoleChange={setRole}
        view={view}
        onViewChange={setView}
        acsCount={state.kind === "ok" ? state.data.contracts.length : null}
        ledgerEnd={state.kind === "ok" ? state.data.ledger_end : null}
        streamStatus={streamStatus}
      />

      {state.kind === "loading" && <Status>Snapshotting ACS…</Status>}
      {state.kind === "err" && <ErrorPanel msg={state.error} />}
      {state.kind === "port-missing" && (
        <EmptyPanel
          title="Participant ports not recorded"
          body={`This instance pre-dates the Canton-port persistence fix.`}
          remediation={state.remediation}
        />
      )}
      {state.kind === "needs-jwt" && (
        <EmptyPanel
          title="JWT lacks party-rights for ACS"
          body="Splice LocalNet signs user-id tokens by default; resolving user → party rights needs UserManagementService wiring."
          remediation={state.remediation}
        />
      )}

      {state.kind === "ok" && view === "contracts" && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "232px minmax(0,1fr)",
            gap: 14,
            alignItems: "start",
          }}
        >
          {/* LEFT — filter sidebar */}
          <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
            <Card
              title="Templates"
              subtitle={`${facets.templates.length} in view`}
            >
              {facets.templates.length === 0 && <Hint>No templates yet</Hint>}
              {facets.templates.map(({ k, v, color }) => (
                <FilterChip
                  key={k}
                  label={shortTemplate(k)}
                  color={color}
                  count={v}
                  active={activeTemplates.has(k)}
                  onClick={() =>
                    setActiveTemplates((prev) => toggle(prev, k))
                  }
                />
              ))}
            </Card>
            <Card title="Parties" subtitle="filter visibility">
              {facets.parties.length === 0 && <Hint>No parties yet</Hint>}
              {facets.parties.map(({ k, v, color }) => (
                <FilterChip
                  key={k}
                  label={shortParty(k)}
                  color={color}
                  count={v}
                  active={activeParties.has(k)}
                  onClick={() => setActiveParties((prev) => toggle(prev, k))}
                />
              ))}
            </Card>
            <Card title="Snapshot">
              <Button
                variant="secondary"
                fullWidth
                icon={<IcRefresh />}
                onClick={() => void refreshSnapshot(name, role, false)}
              >
                Refresh snapshot
              </Button>
              <div
                style={{
                  marginTop: 8,
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "baseline",
                  gap: 12,
                  padding: "4px 0",
                }}
              >
                <span style={{ color: W.dim, fontSize: 12, fontWeight: 500 }}>
                  Stream
                </span>
                <span
                  style={{ fontFamily: wMono, fontSize: 11, color: W.text2 }}
                >
                  {streamStatus}
                </span>
              </div>
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "baseline",
                  gap: 12,
                  padding: "4px 0",
                }}
              >
                <span style={{ color: W.dim, fontSize: 12, fontWeight: 500 }}>
                  Ledger end
                </span>
                <span
                  style={{ fontFamily: wMono, fontSize: 11, color: W.text2 }}
                >
                  {state.data.ledger_end ?? "—"}
                </span>
              </div>
            </Card>
          </div>

          {/* CENTER — ACS table */}
          <div
            style={{
              background: W.surface,
              border: `1px solid ${W.border}`,
              borderRadius: 4,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                padding: "11px 14px",
                borderBottom: `1px solid ${W.border}`,
                display: "flex",
                alignItems: "center",
                gap: 12,
              }}
            >
              <div>
                <div style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}>
                  Active Contract Set
                </div>
                <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2 }}>
                  {filtered.length} of {state.data.contracts.length} contracts ·{" "}
                  {streamStatus === "live"
                    ? "streaming creates and archives"
                    : streamStatus === "reconnecting"
                      ? "reconnecting to live stream…"
                      : streamStatus === "truncated"
                        ? "stream capped — reconciling via snapshot"
                        : "snapshot (stream idle)"}
                </div>
              </div>
              <span style={{ marginLeft: "auto" }} />
              <div style={{ position: "relative" }}>
                <input
                  ref={searchRef}
                  type="text"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="filter cid, payload, party…"
                  style={{
                    background: W.border,
                    border: `1px solid ${W.border}`,
                    color: W.text,
                    fontSize: 12,
                    padding: "5px 32px 5px 10px",
                    borderRadius: 2,
                    width: 240,
                  }}
                  aria-label="Filter contracts"
                />
                <span
                  style={{
                    position: "absolute",
                    right: 8,
                    top: 5,
                    color: W.dim,
                    fontSize: 10,
                    fontFamily: wMono,
                    background: W.surface,
                    border: `1px solid ${W.border}`,
                    padding: "0 4px",
                    borderRadius: 2,
                  }}
                >
                  /
                </span>
              </div>
            </div>

            {/* Column header row */}
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1.8fr 1.2fr 1fr 0.8fr 0.8fr",
                gap: 14,
                padding: "9px 14px",
                color: W.dim,
                fontSize: 10.5,
                ...tableCaps,
                borderBottom: `1px solid ${W.border}`,
              }}
            >
              <span>Template</span>
              <span>Cid</span>
              <span>Owner / signatory</span>
              <span>Payload</span>
              <span style={{ display: "flex", justifyContent: "space-between" }}>
                <span>Age</span>
                <span>Sig · Obs</span>
              </span>
            </div>

            {filtered.length === 0 && (
              <div style={{ padding: 18, color: W.dim, fontSize: 12.5 }}>
                No contracts match the current filters.
              </div>
            )}
            <div style={{ maxHeight: "60vh", overflowY: "auto" }}>
              {filtered.map((c) => (
                <AcsRow
                  key={c.contract_id}
                  row={c}
                  active={c.contract_id === selectedCid}
                  onClick={() => setSelectedCid(c.contract_id)}
                />
              ))}
            </div>
            <div
              style={{
                padding: "10px 14px",
                color: W.dim,
                fontSize: 11.5,
                display: "flex",
                justifyContent: "space-between",
                borderTop: `1px solid ${W.border}`,
              }}
            >
              <span>
                Showing {filtered.length} of {state.data.contracts.length} ·{" "}
                {streamStatus === "live" ? "live" : "snapshot"} @ offset{" "}
                {state.data.ledger_end ?? "—"}
              </span>
              <span>↑↓ navigate · ↵ open · / focus search · esc close</span>
            </div>
          </div>
        </div>
      )}

      {/* Detail drawer — fixed right-side overlay, outside the grid */}
      {state.kind === "ok" && view === "contracts" && selected && (
        <ContractDetailDrawer
          instance={name}
          role={role}
          row={selected}
          onClose={() => setSelectedCid(null)}
          onPrev={goPrev}
          onNext={goNext}
        />
      )}

      {state.kind === "ok" && view === "transactions" && (
        <TransactionsView name={name} role={role} />
      )}
      {state.kind === "ok" && view === "timeline" && (
        <TimelineView name={name} role={role} />
      )}
    </section>
  );
}

// ─────── Sub-components ────────────────────────────────────────

function ProjectionBar({
  instance,
  role,
  onRoleChange,
  view,
  onViewChange,
  acsCount,
  ledgerEnd,
  streamStatus,
}: {
  instance: string;
  role: Role;
  onRoleChange: (r: Role) => void;
  view: View;
  onViewChange: (v: View) => void;
  acsCount: number | null;
  ledgerEnd: number | null;
  streamStatus: "idle" | "live" | "reconnecting" | "truncated";
}) {
  const pillColor =
    streamStatus === "live"
      ? "#7CC89A"
      : streamStatus === "reconnecting"
        ? "#DDB25E"
        : streamStatus === "truncated"
          ? "#7BD2C6"
          : W.dim;
  const pillLabel =
    streamStatus === "live"
      ? "live"
      : streamStatus === "reconnecting"
        ? "reconnecting"
        : streamStatus === "truncated"
          ? "truncated"
          : "idle";
  return (
    <section
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: "12px 16px",
        marginBottom: 14,
        display: "grid",
        gridTemplateColumns: "auto auto 1fr auto auto",
        gap: 16,
        alignItems: "center",
      }}
    >
      <span
        style={{
          color: W.dim,
          fontSize: 12,
          fontWeight: 500,
        }}
      >
        Projecting through
      </span>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span
          style={{
            background: W.border,
            border: `1px solid ${W.border}`,
            color: W.text,
            fontFamily: wMono,
            fontSize: 11.5,
            padding: "5px 10px",
            borderRadius: 2,
          }}
        >
          <span style={{ color: W.dim }}>participant</span>{" "}
          <span style={{ color: W.brand }}>{instance}</span>
        </span>
        <span style={{ color: W.dim }}>as</span>
        <select
          value={role}
          onChange={(e) => onRoleChange(e.target.value as Role)}
          aria-label="Project as role"
          style={{
            background: W.border,
            border: `1px solid ${W.border}`,
            color: W.text,
            fontFamily: wMono,
            fontSize: 11.5,
            padding: "5px 10px",
            borderRadius: 2,
            cursor: "pointer",
          }}
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </div>
      <div
        style={{
          borderLeft: `1px solid ${W.border}`,
          paddingLeft: 16,
          color: W.dim,
          fontSize: 11.5,
          lineHeight: 1.4,
        }}
      >
        <span style={{ color: W.text, fontWeight: 600 }}>
          {acsCount === null ? "…" : acsCount.toLocaleString()}
        </span>{" "}
        active contracts visible
        {ledgerEnd != null && (
          <>
            {" · ledger offset "}
            <span style={{ color: W.text }}>{ledgerEnd.toLocaleString()}</span>
          </>
        )}
      </div>
      <div
        style={{
          display: "flex",
          background: W.border,
          borderRadius: 4,
          padding: 3,
          border: `1px solid ${W.border}`,
        }}
      >
        {(["contracts", "transactions", "timeline"] as const).map((v) => (
          <button
            key={v}
            onClick={() => onViewChange(v)}
            style={{
              padding: "5px 12px",
              fontSize: 12,
              borderRadius: 2,
              border: "none",
              background: v === view ? W.brand : "transparent",
              color: v === view ? W.onAccent : W.dim,
              fontWeight: v === view ? 600 : 500,
              cursor: "pointer",
              textTransform: "capitalize",
            }}
          >
            {v}
          </button>
        ))}
      </div>
      <Pill color={pillColor}>{pillLabel}</Pill>
    </section>
  );
}

function FilterChip({
  label,
  color,
  count,
  active,
  onClick,
}: {
  label: string;
  color: string;
  count: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "6px 9px",
        borderRadius: 2,
        cursor: "pointer",
        background: active ? W.border : "transparent",
        borderLeft: active ? `2px solid ${color}` : "2px solid transparent",
        paddingLeft: 9,
        width: "100%",
        border: "none",
        textAlign: "left",
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: 2,
          background: color,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          flex: 1,
          fontSize: 12.5,
          color: active ? W.text : W.text2,
          fontWeight: active ? 600 : 500,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </span>
      <span style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}>
        {count}
      </span>
    </button>
  );
}

function AcsRow({
  row,
  active,
  onClick,
}: {
  row: ContractRow;
  active: boolean;
  onClick: () => void;
}) {
  const payloadPreview = useMemo(() => {
    const p = row.payload ?? {};
    const amt = p.amount ?? p.balance ?? p.value ?? null;
    return amt !== null && amt !== undefined ? String(amt) : "—";
  }, [row.payload]);
  const tplParts = row.template_id.split(":");
  const shortTpl =
    tplParts.length >= 3 ? `${tplParts[1]}:${tplParts[2]}` : row.template_id;
  return (
    <div
      onClick={onClick}
      style={{
        display: "grid",
        gridTemplateColumns: "1.8fr 1.2fr 1fr 0.8fr 0.8fr",
        gap: 14,
        padding: "9px 14px",
        alignItems: "center",
        background: active ? `${tint(W.brand, 6)}` : "transparent",
        borderLeft: active ? `2px solid ${W.brand}` : "2px solid transparent",
        paddingLeft: active ? 12 : 14,
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <Dot color={W.ok} size={6} />
        <span
          style={{
            fontSize: 12.5,
            fontWeight: active ? 600 : 500,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            color: W.text,
          }}
          title={row.template_id}
        >
          {shortTpl}
        </span>
      </div>
      <code
        style={{
          color: "#93A7F0",
          fontFamily: wMono,
          fontSize: 11,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.contract_id}
      >
        {row.contract_id.slice(0, 14)}…
      </code>
      <span
        style={{
          color: W.text2,
          fontSize: 11.5,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.signatories[0]}
      >
        {row.signatories[0]?.split("::")[0] ?? "—"}
      </span>
      <span style={{ fontFamily: wMono, fontSize: 12, color: W.text }}>
        {payloadPreview}
      </span>
      <span
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          justifyContent: "space-between",
          color: W.dim,
          fontSize: 11.5,
        }}
      >
        <span>{row.created_at ? ago(row.created_at) : "—"}</span>
        <span>
          {row.signatories.length}·{row.observers.length}
        </span>
      </span>
    </div>
  );
}

// TransactionsView — table of recent ledger updates (transactions,
// reassignments, topology events). Each row expands inline to show
// its event tree and can be replayed as a per-party visibility
// projection. The filter bar mirrors the CLI `tx ls` flags; filters
// are applied server-side over the offset window, so narrowing the
// query can find contracts beyond the row cap.
function TransactionsView({ name, role }: { name: string; role: Role }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TransactionsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [openId, setOpenId] = useState<string | null>(null);
  const [replayId, setReplayId] = useState<string | null>(null);
  // Draft filter inputs (raw strings) vs the applied filters used in
  // the fetch effect; applying on submit avoids a round-trip per
  // keystroke.
  const [draft, setDraft] = useState<TxFilterDraft>(emptyDraft);
  const [applied, setApplied] = useState<TransactionFilters>({});

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchTransactions(name, role, 200, applied)
      .then((data) => {
        if (cancelled) return;
        setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (
          e instanceof ApiError &&
          e.code === "PARTICIPANT_PORT_NOT_RECORDED"
        ) {
          setState({
            kind: "port-missing",
            remediation:
              e.remediation?.[0] ??
              `Restart the instance to capture Canton API ports.`,
          });
          return;
        }
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({
            kind: "needs-jwt",
            remediation: e.remediation?.[0] ?? "Wrap UserManagementService.",
          });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load updates",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, role, applied]);

  // Party options for the replay drawer's "visible to" selector,
  // derived from the witnesses present in the loaded rows.
  const partyOptions = useMemo(() => {
    if (state.kind !== "ok") return [];
    const set = new Set<string>();
    for (const tx of state.data.transactions) {
      for (const ev of tx.events ?? []) {
        for (const wparty of ev.witnesses ?? []) set.add(wparty);
      }
    }
    return [...set].sort();
  }, [state]);

  const applyFilters = useCallback(() => setApplied(parseDraft(draft)), [draft]);
  const clearFilters = useCallback(() => {
    setDraft(emptyDraft);
    setApplied({});
  }, []);
  const hasFilters =
    applied.parties?.length ||
    applied.templates?.length ||
    applied.from !== undefined ||
    applied.to !== undefined;

  const body = (() => {
    if (state.kind === "loading") {
      return <Status>Loading updates stream…</Status>;
    }
    if (state.kind === "err") {
      return <ErrorPanel msg={state.error} />;
    }
    if (state.kind === "port-missing") {
      return (
        <EmptyPanel
          title="Participant ports not recorded"
          body="This instance pre-dates the Canton-port persistence fix."
          remediation={state.remediation}
        />
      );
    }
    if (state.kind === "needs-jwt") {
      return (
        <EmptyPanel
          title="JWT lacks party-rights"
          body="Resolving user → party rights needs UserManagementService."
          remediation={state.remediation}
        />
      );
    }

    return (
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            padding: "11px 14px",
            borderBottom: `1px solid ${W.border}`,
            display: "flex",
            alignItems: "baseline",
            gap: 12,
          }}
        >
          <div>
            <div style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}>
              Transactions
            </div>
            <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2 }}>
              {state.data.transactions.length} updates · newest first ·{" "}
              {hasFilters ? "filtered · " : ""}
              scanned from {state.data.scanned_from?.toLocaleString() ?? "—"} to
              ledger end {state.data.ledger_end.toLocaleString()}
              {state.data.window_truncated ? " · partial window" : ""}
            </div>
          </div>
        </div>

        <div
          style={{
            display: "grid",
            gridTemplateColumns: "70px 110px 1.4fr 1.1fr 0.8fr 56px 70px",
            gap: 14,
            padding: "9px 14px",
            color: W.dim,
            fontSize: 10.5,
            ...tableCaps,
            borderBottom: `1px solid ${W.border}`,
          }}
        >
          <span>Kind</span>
          <span>Offset</span>
          <span>Command / Update id</span>
          <span>Workflow</span>
          <span>Time</span>
          <span style={{ textAlign: "right" }}>Events</span>
          <span style={{ textAlign: "right" }}>Replay</span>
        </div>

        {state.data.transactions.length === 0 && (
          <div style={{ padding: 18, color: W.dim, fontSize: 12.5 }}>
            {hasFilters
              ? "No updates matched the filters in the scanned window."
              : "No updates in the current ledger window."}
          </div>
        )}

        <div style={{ maxHeight: "60vh", overflowY: "auto" }}>
          {state.data.transactions.map((tx) => (
            <TxRowComponent
              key={`${tx.offset}-${tx.update_id ?? ""}`}
              tx={tx}
              open={openId === (tx.update_id ?? `o-${tx.offset}`)}
              onToggle={() =>
                setOpenId((cur) =>
                  cur === (tx.update_id ?? `o-${tx.offset}`)
                    ? null
                    : tx.update_id ?? `o-${tx.offset}`,
                )
              }
              onReplay={
                tx.kind === "transaction" && tx.update_id
                  ? () => setReplayId(tx.update_id!)
                  : undefined
              }
            />
          ))}
        </div>
        <div
          style={{
            padding: "10px 14px",
            color: W.dim,
            fontSize: 11.5,
            borderTop: `1px solid ${W.border}`,
          }}
        >
          Click a row to expand its event tree · Replay shows the per-party
          visibility projection.
        </div>
      </div>
    );
  })();

  return (
    <div>
      <TxFilterBar
        draft={draft}
        onChange={setDraft}
        onApply={applyFilters}
        onClear={clearFilters}
        active={!!hasFilters}
      />
      {body}
      {/* Replay drawer — fixed right-side overlay; the table keeps
          its full width underneath. */}
      {replayId && (
        <TxReplayDrawer
          instance={name}
          role={role}
          updateId={replayId}
          partyOptions={partyOptions}
          onClose={() => setReplayId(null)}
        />
      )}
    </div>
  );
}

// TxFilterDraft holds the raw filter-bar inputs. party/template are
// comma-separated free text; from/to are offset strings.
interface TxFilterDraft {
  party: string;
  template: string;
  from: string;
  to: string;
}

const emptyDraft: TxFilterDraft = { party: "", template: "", from: "", to: "" };

// parseDraft converts the raw inputs into the typed TransactionFilters
// the API client forwards. Blank fields drop out; non-numeric from/to
// are ignored.
function parseDraft(d: TxFilterDraft): TransactionFilters {
  const split = (s: string) =>
    s
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  const f: TransactionFilters = {};
  const parties = split(d.party);
  if (parties.length > 0) f.parties = parties;
  const templates = split(d.template);
  if (templates.length > 0) f.templates = templates;
  const from = Number(d.from);
  if (d.from.trim() !== "" && Number.isFinite(from) && from >= 0) f.from = from;
  const to = Number(d.to);
  if (d.to.trim() !== "" && Number.isFinite(to) && to >= 0) f.to = to;
  return f;
}

function TxFilterBar({
  draft,
  onChange,
  onApply,
  onClear,
  active,
}: {
  draft: TxFilterDraft;
  onChange: (d: TxFilterDraft) => void;
  onApply: () => void;
  onClear: () => void;
  active: boolean;
}) {
  const input = (
    key: keyof TxFilterDraft,
    placeholder: string,
    width: number,
    numeric = false,
  ) => (
    <input
      type={numeric ? "number" : "text"}
      value={draft[key]}
      placeholder={placeholder}
      onChange={(e) => onChange({ ...draft, [key]: e.target.value })}
      onKeyDown={(e) => {
        if (e.key === "Enter") onApply();
      }}
      aria-label={placeholder}
      style={{
        background: W.border,
        border: `1px solid ${W.border}`,
        color: W.text,
        fontSize: 12,
        fontFamily: wMono,
        padding: "5px 8px",
        borderRadius: 2,
        width,
      }}
    />
  );
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: "10px 14px",
        marginBottom: 12,
        display: "flex",
        alignItems: "center",
        gap: 8,
        flexWrap: "wrap",
      }}
    >
      <span
        style={{
          color: W.dim,
          fontSize: 12,
          fontWeight: 500,
        }}
      >
        Filters
      </span>
      {input("party", "party id (comma-sep)", 200)}
      {input("template", "Module:Entity (comma-sep)", 200)}
      {input("from", "from offset", 110, true)}
      {input("to", "to offset", 110, true)}
      <Button variant="secondary" onClick={onApply}>
        Apply
      </Button>
      {active && (
        <Button variant="ghost" onClick={onClear}>
          Clear
        </Button>
      )}
    </div>
  );
}

function TxRowComponent({
  tx,
  open,
  onToggle,
  onReplay,
}: {
  tx: TransactionRow;
  open: boolean;
  onToggle: () => void;
  onReplay?: () => void;
}) {
  return (
    <>
      <div
        onClick={onToggle}
        style={{
          display: "grid",
          gridTemplateColumns: "70px 110px 1.4fr 1.1fr 0.8fr 56px 70px",
          gap: 14,
          padding: "9px 14px",
          alignItems: "center",
          background: open ? `${tint(W.brand, 6)}` : "transparent",
          borderBottom: `1px solid ${W.border}`,
          cursor: "pointer",
        }}
      >
        <span
          style={{
            color: TX_KIND_COLOR[tx.kind],
            fontFamily: wMono,
            fontSize: 11,
            fontWeight: 600,
          }}
        >
          {tx.kind}
        </span>
        <code style={{ fontFamily: wMono, color: W.text2, fontSize: 11 }}>
          {tx.offset.toLocaleString()}
        </code>
        <code
          style={{
            fontFamily: wMono,
            color: "#93A7F0",
            fontSize: 11,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          title={tx.command_id ?? tx.update_id ?? ""}
        >
          {tx.command_id ?? tx.update_id?.slice(0, 16) ?? "—"}
        </code>
        <span
          style={{
            color: W.text2,
            fontSize: 11.5,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {tx.workflow_id || (tx.synchronizer ? `→ ${tx.synchronizer}` : "—")}
        </span>
        <span style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}>
          {tx.record_time ? hhmmss(tx.record_time) : "—"}
        </span>
        <span
          style={{
            color: W.text,
            fontFamily: wMono,
            fontSize: 11,
            textAlign: "right",
          }}
        >
          {tx.event_count ?? "—"}
        </span>
        <span
          style={{
            display: "flex",
            justifyContent: "flex-end",
            alignItems: "center",
          }}
        >
          {onReplay ? (
            <Button
              variant="secondary"
              onClick={(e) => {
                e.stopPropagation();
                onReplay();
              }}
              title="Replay this transaction's per-party visibility projection"
            >
              replay
            </Button>
          ) : (
            <span style={{ color: W.dim, fontSize: 11 }}>—</span>
          )}
        </span>
      </div>
      {open && tx.events && tx.events.length > 0 && (
        <div
          style={{
            background: `${tint(W.brand, 2)}`,
            padding: "10px 14px 12px 84px",
            borderBottom: `1px solid ${W.border}`,
          }}
        >
          {tx.events.map((ev, i) => (
            <EventTreeNode key={i} ev={ev} last={i === tx.events!.length - 1} />
          ))}
        </div>
      )}
    </>
  );
}

function EventTreeNode({
  ev,
  last,
}: {
  ev: TransactionEvent;
  last: boolean;
}) {
  const c: Record<TransactionEvent["kind"], string> = {
    create: "#7CC89A",
    archive: "#7BD2C6",
    exercise: "#8FA3EE",
  };
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        alignItems: "baseline",
        marginBottom: 3,
        fontSize: 11.5,
      }}
    >
      <span
        style={{
          color: W.dim,
          fontFamily: wMono,
          fontSize: 11,
          width: 14,
        }}
      >
        {last ? "└─" : "├─"}
      </span>
      <span style={{ color: c[ev.kind], fontWeight: 600, fontFamily: wMono }}>
        {ev.kind}
      </span>
      <span
        style={{ color: W.text, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
        title={ev.template}
      >
        {ev.template
          ? ev.template.split(":").slice(1).join(":")
          : "—"}
      </span>
      <code
        style={{
          fontFamily: wMono,
          color: "#93A7F0",
          fontSize: 10.5,
          marginLeft: "auto",
        }}
        title={ev.contract_id}
      >
        {ev.contract_id.slice(0, 16)}…
      </code>
    </div>
  );
}

// TimelineView — time-axis strip showing every update as a coloured
// glyph. Clicking a glyph highlights it and shows quick metadata in
// a side card — useful for "what happened in the last minute".
function TimelineView({ name, role }: { name: string; role: Role }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TransactionsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Click = persistent selection; hover = preview when nothing is
  // selected. Click again or Esc clears.
  const [selectedIdx, setSelectedIdx] = useState<number | null>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && selectedIdx !== null) {
        setSelectedIdx(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedIdx]);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    setSelectedIdx(null);
    setHoverIdx(null);
    fetchTransactions(name, role, 500)
      .then((data) => {
        if (cancelled) return;
        setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (
          e instanceof ApiError &&
          e.code === "PARTICIPANT_PORT_NOT_RECORDED"
        ) {
          setState({
            kind: "port-missing",
            remediation: e.remediation?.[0] ?? "Restart the instance.",
          });
          return;
        }
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({
            kind: "needs-jwt",
            remediation: e.remediation?.[0] ?? "Resolve user rights.",
          });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, role]);

  if (state.kind === "loading") return <Status>Loading timeline…</Status>;
  if (state.kind === "err") return <ErrorPanel msg={state.error} />;
  if (state.kind === "port-missing")
    return (
      <EmptyPanel
        title="Participant ports not recorded"
        body="This instance pre-dates the Canton-port persistence fix."
        remediation={state.remediation}
      />
    );
  if (state.kind === "needs-jwt")
    return (
      <EmptyPanel
        title="JWT lacks party-rights"
        body="Resolving user → party rights needs UserManagementService."
        remediation={state.remediation}
      />
    );

  const txs = state.data.transactions;
  // Bucket updates into time slots for the strip — newest on the right.
  const buckets = bucketByTime(txs, 60);
  // Selection wins over hover: once a glyph is clicked, the side
  // panel sticks to that update so the user can read the events
  // without keeping the cursor over the strip.
  const focusedIdx = selectedIdx ?? hoverIdx;
  const focused = focusedIdx !== null ? txs[focusedIdx] ?? null : null;

  return (
    <>
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            padding: "11px 14px",
            borderBottom: `1px solid ${W.border}`,
          }}
        >
          <div style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}>
            Timeline
          </div>
          <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2 }}>
            {txs.length} updates · {buckets.length}-bucket density strip ·
            hover any glyph for details
          </div>
        </div>

        {/* Activity strip */}
        <div
          style={{
            padding: "16px 14px",
            display: "flex",
            gap: 2,
            alignItems: "flex-end",
            height: 100,
            background: `linear-gradient(180deg, transparent 0%, ${tint(W.border, 13)} 100%)`,
          }}
        >
          {buckets.map((b, i) => {
            const h = Math.max(4, Math.min(80, b.count * 8));
            return (
              <div
                key={i}
                title={`${b.count} updates`}
                style={{
                  flex: 1,
                  height: h,
                  background:
                    b.count === 0
                      ? W.border
                      : `linear-gradient(180deg, ${tint(W.brand, 40)} 0%, ${W.brand} 100%)`,
                  borderRadius: 2,
                }}
              />
            );
          })}
        </div>

        <div
          style={{
            padding: "4px 14px 0",
            color: W.dim,
            fontSize: 10.5,
            fontFamily: wMono,
            display: "flex",
            justifyContent: "space-between",
          }}
        >
          {txs.length > 0 ? (
            <>
              <span>oldest · {hhmmss(txs[txs.length - 1].record_time ?? "")}</span>
              <span>newest · {hhmmss(txs[0].record_time ?? "")}</span>
            </>
          ) : (
            <span>—</span>
          )}
        </div>

        {/* Event glyph row */}
        <div
          style={{
            padding: "12px 14px",
            display: "flex",
            gap: 3,
            flexWrap: "wrap",
            borderTop: `1px solid ${W.border}`,
            maxHeight: 280,
            overflowY: "auto",
          }}
        >
          {txs.map((tx, i) => {
            const color =
              tx.kind === "transaction"
                ? TX_KIND_COLOR.transaction
                : tx.kind === "reassignment"
                  ? TX_KIND_COLOR.reassignment
                  : TX_KIND_COLOR.topology;
            return (
              <span
                key={`${tx.offset}-${tx.update_id ?? i}`}
                role="button"
                tabIndex={0}
                aria-label={`${tx.kind} at offset ${tx.offset}, ${tx.event_count ?? 0} events`}
                aria-pressed={selectedIdx === i}
                onMouseEnter={() => setHoverIdx(i)}
                onMouseLeave={() => setHoverIdx(null)}
                onClick={() =>
                  setSelectedIdx((cur) => (cur === i ? null : i))
                }
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    setSelectedIdx((cur) => (cur === i ? null : i));
                  }
                }}
                title={`${tx.kind} · offset ${tx.offset} · ${tx.event_count ?? 0} events`}
                style={{
                  width: 10,
                  height: 18,
                  background: color,
                  opacity:
                    selectedIdx === i || hoverIdx === i ? 1 : 0.65,
                  borderRadius: 1.5,
                  cursor: "pointer",
                  transition: "opacity 80ms, transform 80ms",
                  outline:
                    selectedIdx === i ? `2px solid ${W.brand}` : "none",
                  outlineOffset: selectedIdx === i ? 1 : 0,
                  transform:
                    selectedIdx === i
                      ? "translateY(-2px)"
                      : hoverIdx === i
                        ? "translateY(-1px)"
                        : "none",
                }}
              />
            );
          })}
        </div>
        <div
          style={{
            padding: "10px 14px",
            color: W.dim,
            fontSize: 11.5,
            display: "flex",
            justifyContent: "space-between",
            borderTop: `1px solid ${W.border}`,
          }}
        >
          <span>
            <LegendDot color={TX_KIND_COLOR.transaction} label="transaction" />
            <LegendDot color={TX_KIND_COLOR.reassignment} label="reassignment" />
            <LegendDot color={TX_KIND_COLOR.topology} label="topology" />
          </span>
          <span>
            {selectedIdx !== null
              ? "Selected — click again or press Esc to clear."
              : "Hover for preview · click to pin."}
          </span>
        </div>
      </div>

      {/* Detail overlay — hovered/pinned update, fixed to the right
          edge so the timeline strip keeps its full width. */}
      {focused && (
        <div
          style={{
            position: "fixed",
            top: 52,
            right: 0,
            bottom: 0,
            width: "min(480px, 92vw)",
            // Raised surface — matches ContractDetailDrawer/TxReplayDrawer.
            background: W.surface2,
            borderLeft: `1px solid ${W.borderHi}`,
            boxShadow:
              "0 0 0 1px rgba(0,0,0,0.2), -16px 0 40px -12px rgba(0,0,0,0.5)",
            // A hover preview must not steal hit-testing from the strip
            // underneath it (hover-in would unhover the glyph and
            // unmount the panel in a loop); only a pinned selection is
            // interactive.
            pointerEvents: selectedIdx !== null ? "auto" : "none",
            overscrollBehavior: "contain",
            // Below the CommandPalette (zIndex 100) but above content.
            zIndex: 40,
            overflowY: "auto",
          }}
        >
          <header style={{ padding: "14px 16px", borderBottom: `1px solid ${W.border}` }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <Pill
                color={
                  focused.kind === "transaction"
                    ? TX_KIND_COLOR.transaction
                    : focused.kind === "reassignment"
                      ? TX_KIND_COLOR.reassignment
                      : TX_KIND_COLOR.topology
                }
              >
                {focused.kind}
              </Pill>
              <span style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}>
                offset {focused.offset.toLocaleString()}
              </span>
            </div>
            {focused.record_time && (
              <div
                style={{
                  color: W.text2,
                  fontSize: 11,
                  fontFamily: wMono,
                  marginTop: 6,
                }}
              >
                {focused.record_time}
              </div>
            )}
          </header>
          {focused.command_id && (
            <Section label="Command ID">
              <Mono>{focused.command_id}</Mono>
            </Section>
          )}
          {focused.workflow_id && (
            <Section label="Workflow">
              <Mono>{focused.workflow_id}</Mono>
            </Section>
          )}
          {focused.events && focused.events.length > 0 && (
            <Section label={`Events (${focused.events.length})`}>
              {focused.events.map((ev, i) => (
                <EventTreeNode
                  key={i}
                  ev={ev}
                  last={i === focused.events!.length - 1}
                />
              ))}
            </Section>
          )}
        </div>
      )}
    </>
  );
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 5, marginRight: 12 }}>
      <span style={{ width: 8, height: 8, borderRadius: 2, background: color }} />
      <span style={{ color: W.dim, fontSize: 11 }}>{label}</span>
    </span>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return (
    <code
      style={{
        fontFamily: wMono,
        color: W.text2,
        fontSize: 11,
        wordBreak: "break-all",
      }}
    >
      {children}
    </code>
  );
}

function bucketByTime(
  txs: TransactionRow[],
  bucketCount: number,
): Array<{ count: number }> {
  if (txs.length === 0) return Array.from({ length: bucketCount }, () => ({ count: 0 }));
  const times = txs.map((t) =>
    t.record_time ? new Date(t.record_time).getTime() : NaN,
  );
  const valid = times.filter((t) => Number.isFinite(t));
  if (valid.length === 0)
    return Array.from({ length: bucketCount }, () => ({ count: 0 }));
  const min = Math.min(...valid);
  const max = Math.max(...valid);
  const span = Math.max(1, max - min);
  const buckets = Array.from({ length: bucketCount }, () => ({ count: 0 }));
  for (const t of times) {
    if (!Number.isFinite(t)) continue;
    const i = Math.min(
      bucketCount - 1,
      Math.floor(((t - min) / span) * bucketCount),
    );
    buckets[i].count++;
  }
  return buckets;
}

function hhmmss(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return iso;
  return d
    .toISOString()
    .slice(11, 19); // "HH:MM:SS"
}

// ─────── Tiny shared primitives ───────────────────────────────

function Card({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 10,
      }}
    >
      <div style={{ padding: "0 4px 8px", borderBottom: `1px solid ${W.border}` }}>
        <div style={{ color: W.text, fontSize: 12.5, fontWeight: 600 }}>
          {title}
        </div>
        {subtitle && (
          <div style={{ color: W.dim, fontSize: 11, marginTop: 1 }}>
            {subtitle}
          </div>
        )}
      </div>
      <div style={{ marginTop: 6 }}>{children}</div>
    </div>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{ padding: "12px 16px", borderBottom: `1px solid ${W.border}` }}>
      <div
        style={{
          color: W.dim,
          fontSize: 10.5,
          ...wideCaps,
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}

function Pill({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span
      style={{
        background: `${color}1A`,
        border: `1px solid ${color}44`,
        color,
        padding: "1px 8px",
        borderRadius: 2,
        fontSize: 10.5,
        fontWeight: 600,
        fontFamily: wMono,
      }}
    >
      {children}
    </span>
  );
}

function Status({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 16,
        color: W.dim,
        fontSize: 13,
      }}
    >
      {children}
    </div>
  );
}

function ErrorPanel({ msg }: { msg: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 16,
        color: "#7BD2C6",
        fontSize: 13,
      }}
    >
      {msg}
    </div>
  );
}

function EmptyPanel({
  title,
  body,
  remediation,
}: {
  title: string;
  body: string;
  remediation: string;
}) {
  return (
    <div
      style={{
        background: `${tint(W.warn, 6)}`,
        border: `1px solid ${W.warn}`,
        borderRadius: 4,
        padding: 20,
      }}
    >
      <h3 style={{ color: W.warn, fontSize: 14, marginTop: 0, marginBottom: 8 }}>
        {title}
      </h3>
      <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>{body}</p>
      <p style={{ color: W.dim, fontSize: 12, marginTop: 12 }}>{remediation}</p>
    </div>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ color: W.dim, fontSize: 11.5, padding: "6px 4px" }}>
      {children}
    </div>
  );
}

// ─────── Helpers ──────────────────────────────────────────────

function shortTemplate(tpl: string): string {
  const parts = tpl.split(":");
  return parts.length >= 3 ? `${parts[1]}:${parts[2]}` : tpl;
}

function shortParty(p: string): string {
  const [name, hash] = p.split("::");
  if (!hash) return p;
  return `${name}::${hash.slice(0, 6)}…`;
}

function toggle<T>(set: Set<T>, item: T): Set<T> {
  const next = new Set(set);
  if (next.has(item)) next.delete(item);
  else next.add(item);
  return next;
}

function ago(iso: string): string {
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return iso;
  const secs = (Date.now() - t) / 1000;
  if (secs < 60) return `${Math.floor(secs)}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}
