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
import { MonoId } from "../components/MonoId";
import { StatusBadge } from "../components/StatusBadge";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import { TX_KIND_COLOR, W, wMono, tableCaps, wideCaps, tint, R, FAST, fs } from "../tokens";
import { ContractDetailDrawer } from "./ContractDetailDrawer";
import { TxReplayDrawer } from "./TxReplayDrawer";

const ROLES: Role[] = ["app-user", "app-provider", "sv"];
// Template/party dot palette, ordered so neighbouring indices differ in
// hue; no red (reserved for errors).
const PALETTE = [
  "#6480E6", "#7BD2C6", "#DDB25E", "#7CC89A",
  "#93A7F0", "#C8971F", "#189E8C", "#9BA3B5",
];

type View = "contracts" | "transactions" | "timeline";

// Honour the OS reduced-motion setting for the timeline glyph fades.
const prefersReducedMotion =
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

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
  const [streamStatus, setStreamStatus] = useState<
    "idle" | "live" | "reconnecting" | "truncated"
  >("idle");
  const searchRef = useRef<HTMLInputElement>(null);

  // quiet=true (reconciliation timer, SSE recovery) repopulates in place;
  // quiet=false (initial mount) shows the loading panel first.
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
        // Quiet background failures are swallowed; next tick retries.
      }
    },
    [],
  );

  useEffect(() => {
    if (!name) return;
    setSelectedCid(null);
    void refreshSnapshot(name, role, false);
  }, [name, role, refreshSnapshot]);

  // Live SSE subscription, mounted once the snapshot has loaded. Deltas
  // apply via a Map<contract_id, row> so create/archive races converge to
  // the same state regardless of arrival order.
  useEffect(() => {
    if (!name) return;
    if (state.kind !== "ok") return;
    // Resume from ledger_end so no events are skipped between the
    // snapshot fetch and the stream open.
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
      // EventSource auto-reconnects; reconcile via snapshot since the
      // browser may have been suspended for minutes.
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
    // Depend on state.kind (not state) so the subscription resets once
    // per snapshot transition, not on every contract-list change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, role, state.kind, refreshSnapshot]);

  // Every 30s, re-pull the snapshot to correct drift the SSE deltas missed.
  useEffect(() => {
    if (!name) return;
    if (state.kind !== "ok") return;
    const id = window.setInterval(() => {
      void refreshSnapshot(name, role, true);
    }, 30_000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, role, state.kind, refreshSnapshot]);

  // "/" focuses search (unless already in an editable); Esc clears selection.
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

  // Template + party facets from the unfiltered ACS.
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

  // Search matches template, cid, payload JSON, and party.
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

  // Navigate over the filtered view (what the user sees), not the ACS order.
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
        <h2 style={{ color: W.text, fontSize: fs.h3, margin: 0 }}>Explorer</h2>
        <p style={{ color: W.dim, fontSize: fs.small, margin: "3px 0 0" }}>
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

      {state.kind === "loading" && <AcsLoading />}
      {state.kind === "err" && (
        <ErrorPanel
          msg={state.error}
          onRetry={() => void refreshSnapshot(name, role, false)}
        />
      )}
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
                <span style={{ color: W.dim, fontSize: fs.small, fontWeight: 500 }}>
                  Stream
                </span>
                <StatusBadge
                  status={streamStatus}
                  pulse={streamStatus === "reconnecting"}
                  style={{ fontSize: fs.small }}
                />
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
                <span style={{ color: W.dim, fontSize: fs.small, fontWeight: 500 }}>
                  Ledger end
                </span>
                <span
                  style={{
                    fontFamily: wMono,
                    fontSize: fs.small,
                    color: W.text2,
                    fontVariantNumeric: "tabular-nums",
                  }}
                >
                  {state.data.ledger_end ?? "—"}
                </span>
              </div>
            </Card>
          </div>

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
                <div style={{ color: W.text, fontSize: fs.body, fontWeight: 600 }}>
                  Active Contract Set
                </div>
                <div style={{ color: W.dim, fontSize: fs.small, marginTop: 2 }}>
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
                    fontSize: fs.small,
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
                    fontSize: fs.caption,
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

            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1.8fr 1.2fr 1fr 0.8fr 0.8fr",
                gap: 14,
                padding: "9px 14px",
                color: W.dim,
                fontSize: fs.caption,
                ...tableCaps,
                borderBottom: `1px solid ${W.border}`,
              }}
            >
              <span>Template</span>
              <span>Contract Id</span>
              <span>Owner / Signatory</span>
              <span style={{ textAlign: "right" }}>Payload</span>
              <span style={{ display: "flex", justifyContent: "space-between" }}>
                <span>Age</span>
                <span>Sig · Obs</span>
              </span>
            </div>

            {filtered.length === 0 &&
              (() => {
                const hasAcsFilters =
                  activeTemplates.size > 0 ||
                  activeParties.size > 0 ||
                  search.trim() !== "";
                return (
                  <div
                    style={{
                      padding: "14px 16px",
                      color: W.dim,
                      fontSize: fs.small,
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "flex-start",
                      gap: 8,
                    }}
                  >
                    {hasAcsFilters ? (
                      <>
                        <span>
                          No contracts match these filters.{" "}
                          {state.data.contracts.length.toLocaleString()} in the
                          snapshot.
                        </span>
                        <Button
                          variant="secondary"
                          onClick={() => {
                            setActiveTemplates(new Set());
                            setActiveParties(new Set());
                            setSearch("");
                          }}
                        >
                          Clear filters
                        </Button>
                      </>
                    ) : (
                      <>
                        <span>
                          The active contract set is empty. Create a contract to
                          populate it.
                        </span>
                        <code
                          style={{
                            fontFamily: wMono,
                            fontSize: fs.small,
                            color: W.text2,
                          }}
                        >
                          dpm localnet tx submit
                        </code>
                      </>
                    )}
                  </div>
                );
              })()}
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
                fontSize: fs.small,
                display: "flex",
                justifyContent: "space-between",
                borderTop: `1px solid ${W.border}`,
              }}
            >
              <span style={{ fontVariantNumeric: "tabular-nums" }}>
                Showing {filtered.length} of {state.data.contracts.length} ·{" "}
                {streamStatus === "live" ? "live" : "snapshot"} @ offset{" "}
                {state.data.ledger_end ?? "—"}
              </span>
              <span>↑↓ navigate · ↵ open · / focus search · esc close</span>
            </div>
          </div>
        </div>
      )}

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
          fontSize: fs.small,
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
            fontSize: fs.small,
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
            fontSize: fs.small,
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
          fontSize: fs.small,
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
              fontSize: fs.small,
              borderRadius: R.control,
              border: "none",
              background: v === view ? W.brand : "transparent",
              color: v === view ? W.onAccent : W.dim,
              fontWeight: v === view ? 600 : 500,
              cursor: "pointer",
              textTransform: "capitalize",
              transition: `background-color ${FAST}, color ${FAST}`,
            }}
          >
            {v}
          </button>
        ))}
      </div>
      <StatusBadge status={streamStatus} variant="pill" pulse={streamStatus === "reconnecting"} />
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
      aria-pressed={active}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "6px 9px",
        borderRadius: R.control,
        cursor: "pointer",
        background: active ? tint(W.brand, 12) : "transparent",
        width: "100%",
        border: "none",
        textAlign: "left",
        transition: `background-color ${FAST}`,
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: R.control,
          background: color,
          opacity: active ? 1 : 0.7,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          flex: 1,
          fontSize: fs.small,
          color: active ? W.text : W.text2,
          fontWeight: active ? 600 : 500,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </span>
      <span style={{ color: W.dim, fontSize: fs.small, fontFamily: wMono }}>
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
        background: active ? tint(W.brand, 12) : "transparent",
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
        transition: `background-color ${FAST}`,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span title="Active contract" aria-label="Active contract" style={{ display: "inline-flex" }}>
          <Dot color={W.ok} size={6} />
        </span>
        <span
          style={{
            fontSize: fs.small,
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
      <MonoId value={row.contract_id} head={8} tail={6} size={fs.small} color={W.mag} />
      <span
        style={{
          color: W.text2,
          fontSize: fs.small,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.signatories[0]}
      >
        {row.signatories[0]?.split("::")[0] ?? "—"}
      </span>
      <span
        style={{
          fontFamily: wMono,
          fontSize: fs.small,
          color: W.text,
          fontVariantNumeric: "tabular-nums",
          textAlign: "right",
        }}
      >
        {payloadPreview}
      </span>
      <span
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          justifyContent: "space-between",
          color: W.dim,
          fontSize: fs.small,
        }}
      >
        <span>{row.created_at ? ago(row.created_at) : "—"}</span>
        <span style={{ fontFamily: wMono, fontVariantNumeric: "tabular-nums" }}>
          {row.signatories.length}·{row.observers.length}
        </span>
      </span>
    </div>
  );
}

// Filters are applied server-side over the offset window, so narrowing
// the query can surface rows beyond the row cap.
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
  // Draft inputs vs applied filters; applying on submit avoids a
  // round-trip per keystroke.
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
  // from witnesses in the loaded rows.
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
      return (
        <TableLoading
          columns={[70, 110, 1.4, 1.1, 0.8, 56, 70]}
          rows={6}
          rowHeight={36}
        />
      );
    }
    if (state.kind === "err") {
      return (
        <ErrorPanel
          msg={state.error}
          onRetry={() => setApplied((f) => ({ ...f }))}
        />
      );
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
            <div style={{ color: W.text, fontSize: fs.body, fontWeight: 600 }}>
              Transactions
            </div>
            <div
              style={{
                color: W.dim,
                fontSize: fs.small,
                marginTop: 2,
                fontVariantNumeric: "tabular-nums",
              }}
            >
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
            fontSize: fs.caption,
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
          <div
            style={{
              padding: "14px 16px",
              color: W.dim,
              fontSize: fs.small,
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-start",
              gap: 8,
            }}
          >
            {hasFilters ? (
              <>
                <span>No updates matched these filters in the scanned window.</span>
                <Button variant="secondary" onClick={clearFilters}>
                  Clear filters
                </Button>
              </>
            ) : (
              <>
                <span>No updates in the current ledger window.</span>
                <code
                  style={{ fontFamily: wMono, fontSize: fs.small, color: W.text2 }}
                >
                  dpm localnet tx ls
                </code>
              </>
            )}
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
            fontSize: fs.small,
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

// party/template are comma-separated free text; from/to are offset strings.
interface TxFilterDraft {
  party: string;
  template: string;
  from: string;
  to: string;
}

const emptyDraft: TxFilterDraft = { party: "", template: "", from: "", to: "" };

// Blank fields drop out; non-numeric from/to are ignored.
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
        fontSize: fs.small,
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
          fontSize: fs.small,
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
            fontSize: fs.small,
            fontWeight: 600,
          }}
        >
          {tx.kind}
        </span>
        <code
          style={{
            fontFamily: wMono,
            color: W.text2,
            fontSize: fs.small,
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {tx.offset.toLocaleString()}
        </code>
        {tx.command_id ? (
          <MonoId value={tx.command_id} head={8} tail={6} size={fs.small} color={W.mag} />
        ) : tx.update_id ? (
          <MonoId value={tx.update_id} head={8} tail={6} size={fs.small} color={W.mag} />
        ) : (
          <span style={{ color: W.dim, fontSize: fs.small }}>—</span>
        )}
        <span
          style={{
            color: W.text2,
            fontSize: fs.small,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {tx.workflow_id || (tx.synchronizer ? `→ ${tx.synchronizer}` : "—")}
        </span>
        <span style={{ color: W.dim, fontSize: fs.small, fontFamily: wMono }}>
          {tx.record_time ? hhmmss(tx.record_time) : "—"}
        </span>
        <span
          style={{
            color: W.text,
            fontFamily: wMono,
            fontSize: fs.small,
            textAlign: "right",
            fontVariantNumeric: "tabular-nums",
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
            <span style={{ color: W.dim, fontSize: fs.small }}>—</span>
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
        fontSize: fs.small,
      }}
    >
      <span
        style={{
          color: W.dim,
          fontFamily: wMono,
          fontSize: fs.small,
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
      <MonoId
        value={ev.contract_id}
        head={8}
        tail={6}
        size={fs.small}
        color={W.mag}
        style={{ marginLeft: "auto" }}
      />
    </div>
  );
}

function TimelineView({ name, role }: { name: string; role: Role }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TransactionsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Click pins a selection; hover previews when nothing is pinned.
  const [selectedIdx, setSelectedIdx] = useState<number | null>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  // Bumped by the error-state Retry to re-run the fetch effect.
  const [nonce, setNonce] = useState(0);
  const reload = useCallback(() => setNonce((n) => n + 1), []);

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
  }, [name, role, nonce]);

  if (state.kind === "loading")
    return (
      <TableLoading columns={[1, 1, 1, 1, 1, 1]} rows={4} rowHeight={30} />
    );
  if (state.kind === "err")
    return <ErrorPanel msg={state.error} onRetry={reload} />;
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
  const buckets = bucketByTime(txs, 60);
  // Pinned selection wins over hover.
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
          <div style={{ color: W.text, fontSize: fs.body, fontWeight: 600 }}>
            Timeline
          </div>
          <div style={{ color: W.dim, fontSize: fs.small, marginTop: 2 }}>
            {txs.length} updates · {buckets.length}-bucket density strip ·
            hover any glyph for details
          </div>
        </div>

        <div
          style={{
            padding: "16px 14px",
            display: "flex",
            gap: 2,
            alignItems: "flex-end",
            height: 100,
            background: W.sunken,
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
                  background: b.count === 0 ? W.border : W.brand,
                  borderRadius: R.control,
                }}
              />
            );
          })}
        </div>

        <div
          style={{
            padding: "4px 14px 0",
            color: W.dim,
            fontSize: fs.caption,
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
                  borderRadius: R.control,
                  cursor: "pointer",
                  transition: prefersReducedMotion
                    ? undefined
                    : `opacity ${FAST}`,
                  outline:
                    selectedIdx === i ? `2px solid ${W.brand}` : "none",
                  outlineOffset: selectedIdx === i ? 1 : 0,
                }}
              />
            );
          })}
        </div>
        <div
          style={{
            padding: "10px 14px",
            color: W.dim,
            fontSize: fs.small,
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
              ? "Pinned. Click again or press Esc to clear."
              : "Hover for preview · click to pin."}
          </span>
        </div>
      </div>

      {focused && (
        <div
          style={{
            position: "fixed",
            top: 52,
            right: 0,
            bottom: 0,
            width: "min(480px, 92vw)",
            background: W.surface2,
            borderLeft: `1px solid ${W.borderHi}`,
            // Only a pinned panel is interactive; a hover preview taking
            // pointer events would unhover the glyph and loop-unmount.
            pointerEvents: selectedIdx !== null ? "auto" : "none",
            overscrollBehavior: "contain",
            // Below the CommandPalette (zIndex 100), above content.
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
              <span
                style={{
                  color: W.dim,
                  fontSize: fs.small,
                  fontFamily: wMono,
                  fontVariantNumeric: "tabular-nums",
                }}
              >
                offset {focused.offset.toLocaleString()}
              </span>
            </div>
            {focused.record_time && (
              <div
                style={{
                  color: W.text2,
                  fontSize: fs.small,
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
      <span style={{ color: W.dim, fontSize: fs.small }}>{label}</span>
    </span>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return (
    <code
      style={{
        fontFamily: wMono,
        color: W.text2,
        fontSize: fs.small,
        fontVariantNumeric: "tabular-nums",
        wordBreak: "break-word",
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
    .slice(11, 19);
}

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
        borderRadius: R.card,
        padding: 10,
      }}
    >
      <div style={{ padding: "0 4px 8px", borderBottom: `1px solid ${W.border}` }}>
        <div style={{ color: W.text, fontSize: fs.small, fontWeight: 600 }}>
          {title}
        </div>
        {subtitle && (
          <div style={{ color: W.dim, fontSize: fs.small, marginTop: 1 }}>
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
          fontSize: fs.caption,
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
        background: tint(color, 13),
        border: `1px solid ${tint(color, 34)}`,
        color,
        padding: "1px 8px",
        borderRadius: R.control,
        fontSize: fs.caption,
        fontWeight: 600,
        fontFamily: wMono,
      }}
    >
      {children}
    </span>
  );
}

// Layout-matched skeleton, gated so a fast snapshot never flashes it.
function AcsLoading() {
  const show = useLoadingDelay(true);
  if (!show) return null;
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <SkeletonTable columns={[1.8, 1.2, 1, 0.8, 0.8]} rows={7} rowHeight={38} />
    </div>
  );
}

function TableLoading({
  columns,
  rows,
  rowHeight,
}: {
  columns: (number | string)[];
  rows: number;
  rowHeight: number;
}) {
  const show = useLoadingDelay(true);
  if (!show) return null;
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <SkeletonTable columns={columns} rows={rows} rowHeight={rowHeight} />
    </div>
  );
}

function ErrorPanel({ msg, onRetry }: { msg: string; onRetry?: () => void }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: "14px 16px",
        display: "flex",
        flexDirection: "column",
        alignItems: "flex-start",
        gap: 8,
      }}
    >
      <div style={{ color: W.err, fontSize: fs.body, fontWeight: 600 }}>
        Could not load ledger data.
      </div>
      <div style={{ color: W.text2, fontSize: fs.small }}>
        The participant did not answer. Check the instance is running, then
        retry.
      </div>
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          Retry
        </Button>
      )}
      <details style={{ color: W.dim, fontSize: fs.small }}>
        <summary style={{ cursor: "pointer" }}>Details</summary>
        <code
          style={{
            display: "block",
            marginTop: 6,
            fontFamily: wMono,
            color: W.text2,
            fontSize: fs.small,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {msg}
        </code>
      </details>
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
        background: tint(W.warn, 6),
        border: `1px solid ${W.warn}`,
        borderRadius: R.card,
        padding: "14px 16px",
      }}
    >
      <h3 style={{ color: W.warn, fontSize: fs.body, marginTop: 0, marginBottom: 8 }}>
        {title}
      </h3>
      <p style={{ color: W.text2, fontSize: fs.body, lineHeight: 1.5, margin: 0 }}>
        {body}
      </p>
      <p style={{ color: W.dim, fontSize: fs.small, marginTop: 12, marginBottom: 0 }}>
        {remediation}
      </p>
    </div>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ color: W.dim, fontSize: fs.small, padding: "6px 4px" }}>
      {children}
    </div>
  );
}

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
