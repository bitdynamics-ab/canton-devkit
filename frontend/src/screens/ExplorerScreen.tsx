import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  fetchContracts,
  fetchTransactions,
  type ContractRow,
  type ContractsListResponse,
  type Role,
  type TransactionEvent,
  type TransactionRow,
  type TransactionsListResponse,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// ExplorerScreen — BIT-186 production layout.
//
// Matches docs/design/mockups/webui-explorer.jsx pixel-by-pixel:
//   - ProjectionBar at top: participant + party pills, view toggle
//     (Contracts/Transactions/Timeline), live status + count strip
//   - 3-column body grid:
//       LEFT (232px)  filter sidebar — Templates + Parties chips
//                     with counts, Time range buttons
//       CENTER (1fr)  ACS table with custom AcsRow layout
//                     (template · cid · party · amount · age · sig·obs)
//                     + search box with "/" hotkey + active row +
//                     archived dimming
//       RIGHT (380px) detail drawer — pills, template+version,
//                     CID, payload, witnesses
//
// The "Transactions" and "Timeline" views are scoped as
// follow-ups (need UpdateService streaming); the toggle's UI is
// in place so the visual contract is complete.

const ROLES: Role[] = ["app-user", "app-provider", "sv"];
const PALETTE = [
  "#5BD7C5", "#7CB5F7", "#C4A8F5", "#F5BF55",
  "#E8A14E", "#F08FB5", "#62E2A0", "#E37C7C",
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
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    setSelectedCid(null);
    fetchContracts(name, role, 500)
      .then((data) => {
        if (cancelled) return;
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
          error: e instanceof ApiError ? e.message : "failed to load ACS",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, role]);

  // Keyboard: / focuses search; Esc clears selection.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "/" && document.activeElement?.tagName !== "INPUT") {
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
            gridTemplateColumns: "232px 1fr 380px",
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
            <Card title="Time range">
              <div style={{ display: "flex", gap: 6 }}>
                {(["Live", "5m", "1h", "24h"] as const).map((w, i) => (
                  <button
                    key={w}
                    style={timeBtn(i === 2)}
                    onClick={() => {
                      /* future: query window — wired in follow-up */
                    }}
                  >
                    {w}
                  </button>
                ))}
              </div>
              <div
                style={{
                  marginTop: 8,
                  color: W.dim,
                  fontSize: 11,
                  fontFamily: wMono,
                }}
              >
                ledger end · {state.data.ledger_end ?? "—"}
              </div>
            </Card>
          </div>

          {/* CENTER — ACS table */}
          <div
            style={{
              background: W.surface,
              border: `1px solid ${W.border}`,
              borderRadius: 10,
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
                  {filtered.length} of {state.data.contracts.length} contracts ·
                  streaming creates and archives
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
                    borderRadius: 6,
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
                    borderRadius: 3,
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
                letterSpacing: 1.4,
                textTransform: "uppercase",
                fontWeight: 600,
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
                live snapshot
              </span>
              <span>↑↓ navigate · ↵ open · / focus search · esc close</span>
            </div>
          </div>

          {/* RIGHT — detail drawer */}
          <DetailDrawer row={selected} />
        </div>
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
}: {
  instance: string;
  role: Role;
  onRoleChange: (r: Role) => void;
  view: View;
  onViewChange: (v: View) => void;
  acsCount: number | null;
  ledgerEnd: number | null;
}) {
  return (
    <section
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
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
          fontSize: 10.5,
          letterSpacing: 1.4,
          textTransform: "uppercase",
          fontWeight: 600,
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
            borderRadius: 6,
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
            borderRadius: 6,
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
        {ledgerEnd !== null && (
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
          borderRadius: 8,
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
              borderRadius: 5,
              border: "none",
              background: v === view ? W.brand : "transparent",
              color: v === view ? "#082018" : W.dim,
              fontWeight: v === view ? 600 : 500,
              cursor: "pointer",
              textTransform: "capitalize",
            }}
          >
            {v}
          </button>
        ))}
      </div>
      <Pill color="#62E2A0">live</Pill>
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
        borderRadius: 6,
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
        background: active ? `${W.brand}10` : "transparent",
        borderLeft: active ? `2px solid ${W.brand}` : "2px solid transparent",
        paddingLeft: active ? 12 : 14,
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "#62E2A0",
            flexShrink: 0,
          }}
        />
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
          color: "#C4A8F5",
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

function DetailDrawer({ row }: { row: ContractRow | null }) {
  if (!row) {
    return (
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
          padding: 32,
          textAlign: "center",
          color: W.dim,
          fontSize: 13,
        }}
      >
        Select a contract to inspect.
      </div>
    );
  }
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        overflow: "hidden",
      }}
    >
      <header style={{ padding: "14px 16px", borderBottom: `1px solid ${W.border}` }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Pill color={W.brand}>active</Pill>
          <Pill color="#7CB5F7">
            visible to {row.signatories.length + row.observers.length}
          </Pill>
        </div>
        <div
          style={{
            marginTop: 8,
            display: "flex",
            alignItems: "baseline",
            gap: 8,
            flexWrap: "wrap",
          }}
        >
          <span style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
            {row.template_id.split(":").slice(1).join(":")}
          </span>
          {row.package_name && (
            <span
              style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}
            >
              {row.package_name}
            </span>
          )}
        </div>
        <div style={{ marginTop: 6, color: "#C4A8F5", fontFamily: wMono, fontSize: 11.5, wordBreak: "break-all" }}>
          {row.contract_id}
        </div>
      </header>
      <Section label="Payload">
        <pre
          style={{
            margin: 0,
            color: W.text2,
            fontFamily: wMono,
            fontSize: 11.5,
            lineHeight: 1.55,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            background: W.border,
            padding: 10,
            borderRadius: 6,
            maxHeight: 240,
            overflow: "auto",
          }}
        >
          {JSON.stringify(row.payload ?? {}, null, 2)}
        </pre>
      </Section>
      <Section label="Signatories">
        {row.signatories.length === 0 ? (
          <Hint>None</Hint>
        ) : (
          row.signatories.map((p) => (
            <div
              key={p}
              style={{ fontFamily: wMono, fontSize: 11, color: W.text2, marginBottom: 3, wordBreak: "break-all" }}
            >
              {p}
            </div>
          ))
        )}
      </Section>
      {row.observers.length > 0 && (
        <Section label="Observers">
          {row.observers.map((p) => (
            <div
              key={p}
              style={{ fontFamily: wMono, fontSize: 11, color: W.text2, marginBottom: 3, wordBreak: "break-all" }}
            >
              {p}
            </div>
          ))}
        </Section>
      )}
      {row.created_at && (
        <Section label="Created">
          <div style={{ fontFamily: wMono, fontSize: 11.5, color: W.text }}>
            {row.created_at}
          </div>
          <div style={{ color: W.dim, fontSize: 11, marginTop: 3 }}>
            {ago(row.created_at)}
          </div>
        </Section>
      )}
    </div>
  );
}

// TransactionsView — table of recent ledger updates (transactions,
// reassignments, topology events) projected from
// UpdateService.GetUpdates. Each transaction row expands inline
// to show its event tree.
function TransactionsView({ name, role }: { name: string; role: Role }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TransactionsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [openId, setOpenId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchTransactions(name, role, 200)
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
  }, [name, role]);

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
        borderRadius: 10,
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
            {state.data.transactions.length} updates · newest first · ledger
            end {state.data.ledger_end.toLocaleString()}
          </div>
        </div>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "70px 110px 1.4fr 1.2fr 0.8fr 60px",
          gap: 14,
          padding: "9px 14px",
          color: W.dim,
          fontSize: 10.5,
          letterSpacing: 1.4,
          textTransform: "uppercase",
          fontWeight: 600,
          borderBottom: `1px solid ${W.border}`,
        }}
      >
        <span>Kind</span>
        <span>Offset</span>
        <span>Command / Update id</span>
        <span>Workflow</span>
        <span>Time</span>
        <span style={{ textAlign: "right" }}>Events</span>
      </div>

      {state.data.transactions.length === 0 && (
        <div style={{ padding: 18, color: W.dim, fontSize: 12.5 }}>
          No updates in the current ledger window.
        </div>
      )}

      <div style={{ maxHeight: "65vh", overflowY: "auto" }}>
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
        Click a row to expand its event tree.
      </div>
    </div>
  );
}

function TxRowComponent({
  tx,
  open,
  onToggle,
}: {
  tx: TransactionRow;
  open: boolean;
  onToggle: () => void;
}) {
  const kindColor: Record<TransactionRow["kind"], string> = {
    transaction: "#62E2A0",
    reassignment: "#7CB5F7",
    topology: "#C4A8F5",
    checkpoint: W.dim,
  };
  return (
    <>
      <div
        onClick={onToggle}
        style={{
          display: "grid",
          gridTemplateColumns: "70px 110px 1.4fr 1.2fr 0.8fr 60px",
          gap: 14,
          padding: "9px 14px",
          alignItems: "center",
          background: open ? `${W.brand}10` : "transparent",
          borderBottom: `1px solid ${W.border}`,
          cursor: "pointer",
        }}
      >
        <span
          style={{
            color: kindColor[tx.kind],
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
            color: "#C4A8F5",
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
      </div>
      {open && tx.events && tx.events.length > 0 && (
        <div
          style={{
            background: `${W.brand}05`,
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
    create: "#62E2A0",
    archive: "#F08FB5",
    exercise: "#7CB5F7",
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
          color: "#C4A8F5",
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
// glyph along the offset/time axis. Clicking a glyph highlights it +
// shows quick metadata in a side card. Useful for "what happened in
// the last minute" debugging.
function TimelineView({ name, role }: { name: string; role: Role }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TransactionsListResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
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
  const hovered = hoverIdx !== null ? txs[hoverIdx] ?? null : null;

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "1fr 320px",
        gap: 14,
        alignItems: "start",
      }}
    >
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
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
            background: `linear-gradient(180deg, transparent 0%, ${W.border}20 100%)`,
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
                      : `linear-gradient(180deg, ${W.brand}66 0%, ${W.brand} 100%)`,
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
                ? "#62E2A0"
                : tx.kind === "reassignment"
                  ? "#7CB5F7"
                  : "#C4A8F5";
            return (
              <span
                key={`${tx.offset}-${tx.update_id ?? i}`}
                onMouseEnter={() => setHoverIdx(i)}
                onMouseLeave={() => setHoverIdx(null)}
                title={`${tx.kind} · offset ${tx.offset} · ${tx.event_count ?? 0} events`}
                style={{
                  width: 10,
                  height: 18,
                  background: color,
                  opacity: hoverIdx === i ? 1 : 0.7,
                  borderRadius: 1.5,
                  cursor: "pointer",
                  transition: "opacity 80ms",
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
            <LegendDot color="#62E2A0" label="transaction" />
            <LegendDot color="#7CB5F7" label="reassignment" />
            <LegendDot color="#C4A8F5" label="topology" />
          </span>
          <span>Hover a glyph for the right-panel detail.</span>
        </div>
      </div>

      {/* Side panel — hovered detail */}
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
          overflow: "hidden",
        }}
      >
        {hovered ? (
          <>
            <header style={{ padding: "14px 16px", borderBottom: `1px solid ${W.border}` }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <Pill
                  color={
                    hovered.kind === "transaction"
                      ? "#62E2A0"
                      : hovered.kind === "reassignment"
                        ? "#7CB5F7"
                        : "#C4A8F5"
                  }
                >
                  {hovered.kind}
                </Pill>
                <span style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}>
                  offset {hovered.offset.toLocaleString()}
                </span>
              </div>
              {hovered.record_time && (
                <div
                  style={{
                    color: W.text2,
                    fontSize: 11,
                    fontFamily: wMono,
                    marginTop: 6,
                  }}
                >
                  {hovered.record_time}
                </div>
              )}
            </header>
            {hovered.command_id && (
              <Section label="Command ID">
                <Mono>{hovered.command_id}</Mono>
              </Section>
            )}
            {hovered.workflow_id && (
              <Section label="Workflow">
                <Mono>{hovered.workflow_id}</Mono>
              </Section>
            )}
            {hovered.events && hovered.events.length > 0 && (
              <Section label={`Events (${hovered.events.length})`}>
                {hovered.events.map((ev, i) => (
                  <EventTreeNode
                    key={i}
                    ev={ev}
                    last={i === hovered.events!.length - 1}
                  />
                ))}
              </Section>
            )}
          </>
        ) : (
          <div
            style={{
              padding: 32,
              textAlign: "center",
              color: W.dim,
              fontSize: 13,
            }}
          >
            Hover any glyph on the left to inspect it.
          </div>
        )}
      </div>
    </div>
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
        borderRadius: 10,
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
          letterSpacing: 1.4,
          textTransform: "uppercase",
          fontWeight: 600,
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
        borderRadius: 4,
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
        borderRadius: 10,
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
        borderRadius: 10,
        padding: 16,
        color: "#F08FB5",
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
        background: `${W.warn}10`,
        border: `1px solid ${W.warn}`,
        borderRadius: 10,
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

function timeBtn(active: boolean): React.CSSProperties {
  return {
    flex: 1,
    padding: "5px",
    fontSize: 11.5,
    borderRadius: 5,
    border: `1px solid ${active ? W.brand : W.border}`,
    background: active ? `${W.brand}1A` : "transparent",
    color: active ? W.brand : W.dim,
    cursor: "pointer",
    fontWeight: active ? 600 : 500,
    fontFamily: wMono,
  };
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
