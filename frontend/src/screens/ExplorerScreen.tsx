import { useEffect, useState } from "react";
import {
  ApiError,
  fetchContracts,
  type ContractRow,
  type ContractsListResponse,
  type Role,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// ExplorerScreen — BIT-186.
//
// ACS snapshot via GET /api/instances/:name/contracts?role=. MVP:
// read-only table + role switcher + click-to-expand payload row.
// Live SSE, filters (template/party), and a typed contract detail
// drawer are tracked as follow-ups in the ticket.
const ROLES: Role[] = ["app-user", "app-provider", "sv"];

export function ExplorerScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: ContractsListResponse }
    | { kind: "port-missing"; remediation: string }
    | { kind: "needs-party-jwt"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    setExpanded(null);
    fetchContracts(name, role)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", data });
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
              `Restart the instance to capture all Canton API ports.`,
          });
          return;
        }
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({
            kind: "needs-party-jwt",
            remediation:
              e.remediation?.[0] ??
              `Wrap UserManagementService to resolve user-id → party set.`,
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
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 16,
          marginBottom: 16,
        }}
      >
        <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>
          Explorer —{" "}
          <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>
        </h2>
        {state.kind === "ok" && (
          <span style={{ color: W.dim, fontSize: 11.5, fontFamily: wMono }}>
            ledger end · {state.data.ledger_end}
          </span>
        )}
        <span style={{ marginLeft: "auto" }} />
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {state.kind === "loading" && (
        <Card>
          <p style={{ color: W.dim, fontSize: 13 }}>Snapshotting ACS…</p>
        </Card>
      )}

      {state.kind === "err" && (
        <Card>
          <p style={{ color: W.err, fontSize: 13 }}>{state.error}</p>
        </Card>
      )}

      {state.kind === "needs-party-jwt" && (
        <Card>
          <h3
            style={{
              color: W.warn,
              fontSize: 14,
              marginTop: 0,
              marginBottom: 8,
            }}
          >
            Explorer needs party-rights resolution
          </h3>
          <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>
            Splice LocalNet signs <code style={{ fontFamily: wMono }}>user-id</code>{" "}
            JWTs by default. The Canton participant's ACS query requires
            either an admin claim or a per-party filter — and resolving a
            user-id to its party rights needs the
            UserManagementService, which the DevKit's ledger client
            doesn't wrap yet.
          </p>
          <p style={{ color: W.dim, fontSize: 12, marginTop: 12 }}>
            {state.remediation}
          </p>
          <p style={{ color: W.dim, fontSize: 11, marginTop: 12 }}>
            Tracked as a follow-up to BIT-186. The CLI{" "}
            <code style={{ fontFamily: wMono, color: W.text2 }}>
              localnet contracts watch
            </code>{" "}
            works against an explicit party-id flag in the meantime.
          </p>
        </Card>
      )}

      {state.kind === "port-missing" && (
        <Card>
          <h3
            style={{
              color: W.warn,
              fontSize: 14,
              marginTop: 0,
              marginBottom: 8,
            }}
          >
            Participant ports not recorded
          </h3>
          <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>
            This instance was started before the registry captured Canton's gRPC
            port mappings. The Explorer can't reach the participant Ledger API
            without them.
          </p>
          <p style={{ color: W.dim, fontSize: 12, marginTop: 12 }}>
            {state.remediation}
          </p>
        </Card>
      )}

      {state.kind === "ok" && (
        <ContractsTable
          data={state.data}
          expanded={expanded}
          onToggle={(id) => setExpanded((cur) => (cur === id ? null : id))}
        />
      )}
    </section>
  );
}

function RoleSwitcher({
  role,
  onChange,
}: {
  role: Role;
  onChange: (r: Role) => void;
}) {
  return (
    <div style={{ display: "flex", gap: 4 }}>
      {ROLES.map((r) => {
        const active = r === role;
        return (
          <button
            key={r}
            onClick={() => onChange(r)}
            style={{
              background: active ? W.brand : "transparent",
              color: active ? W.surface : W.dim,
              border: `1px solid ${active ? W.brand : W.border}`,
              borderRadius: 6,
              padding: "3px 10px",
              fontSize: 11.5,
              fontFamily: wMono,
              cursor: active ? "default" : "pointer",
            }}
          >
            {r}
          </button>
        );
      })}
    </div>
  );
}

function ContractsTable({
  data,
  expanded,
  onToggle,
}: {
  data: ContractsListResponse;
  expanded: string | null;
  onToggle: (id: string) => void;
}) {
  if (data.contracts.length === 0) {
    return (
      <Card>
        <p style={{ color: W.dim, fontSize: 13 }}>
          No active contracts visible to{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>{data.role}</code>
          .
        </p>
      </Card>
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
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12.5 }}>
        <thead>
          <tr style={{ background: W.border }}>
            <Th>Template</Th>
            <Th>Contract ID</Th>
            <Th>Signatories</Th>
            <Th>Created</Th>
          </tr>
        </thead>
        <tbody>
          {data.contracts.map((c, i) => (
            <ContractRowView
              key={c.contract_id}
              row={c}
              expanded={expanded === c.contract_id}
              isLast={i === data.contracts.length - 1}
              onClick={() => onToggle(c.contract_id)}
            />
          ))}
        </tbody>
      </table>
      <div
        style={{
          padding: "8px 14px",
          color: W.dim,
          fontSize: 11,
          borderTop: `1px solid ${W.border}`,
        }}
      >
        {data.contracts.length} contract{data.contracts.length === 1 ? "" : "s"}{" "}
        on{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>{data.role}</code> ·
        click a row to expand payload
      </div>
    </div>
  );
}

function ContractRowView({
  row,
  expanded,
  isLast,
  onClick,
}: {
  row: ContractRow;
  expanded: boolean;
  isLast: boolean;
  onClick: () => void;
}) {
  // Show only the "Module:Entity" suffix in the table to keep the
  // template column readable; package ID lives in the expanded view.
  const tplParts = row.template_id.split(":");
  const shortTpl =
    tplParts.length >= 3 ? `${tplParts[1]}:${tplParts[2]}` : row.template_id;
  return (
    <>
      <tr
        onClick={onClick}
        style={{
          cursor: "pointer",
          borderBottom: !isLast || expanded ? `1px solid ${W.border}` : "none",
        }}
      >
        <Td>
          <code style={{ fontFamily: wMono, color: W.text, fontSize: 11.5 }}>
            {shortTpl}
          </code>
        </Td>
        <Td>
          <code
            style={{ fontFamily: wMono, color: W.dim, fontSize: 11 }}
            title={row.contract_id}
          >
            {row.contract_id.slice(0, 18)}…
          </code>
        </Td>
        <Td>
          <span style={{ color: W.text2, fontSize: 11.5 }}>
            {row.signatories.length === 0
              ? "—"
              : row.signatories
                  .map((p) => p.split("::")[0])
                  .slice(0, 2)
                  .join(", ")}
            {row.signatories.length > 2
              ? ` +${row.signatories.length - 2}`
              : ""}
          </span>
        </Td>
        <Td>
          <span style={{ color: W.dim, fontSize: 11 }}>
            {row.created_at ? row.created_at.slice(0, 19).replace("T", " ") : "—"}
          </span>
        </Td>
      </tr>
      {expanded && (
        <tr style={{ background: `${W.brand}08` }}>
          <td colSpan={4} style={{ padding: "12px 14px" }}>
            <PayloadView row={row} />
          </td>
        </tr>
      )}
    </>
  );
}

function PayloadView({ row }: { row: ContractRow }) {
  return (
    <div style={{ display: "grid", gap: 8, fontSize: 11.5 }}>
      <KV label="Contract ID" value={row.contract_id} mono />
      <KV label="Template" value={row.template_id} mono />
      {row.package_name && <KV label="Package" value={row.package_name} mono />}
      <KV
        label="Signatories"
        value={row.signatories.join("\n") || "—"}
        mono
      />
      {row.observers.length > 0 && (
        <KV label="Observers" value={row.observers.join("\n")} mono />
      )}
      <div>
        <div style={{ color: W.dim, marginBottom: 4 }}>Payload</div>
        <pre
          style={{
            background: W.border,
            color: W.text,
            padding: 10,
            borderRadius: 6,
            margin: 0,
            fontSize: 11,
            fontFamily: wMono,
            overflow: "auto",
            maxHeight: 320,
          }}
        >
          {JSON.stringify(row.payload ?? {}, null, 2)}
        </pre>
      </div>
    </div>
  );
}

function KV({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "120px 1fr", gap: 8 }}>
      <div style={{ color: W.dim }}>{label}</div>
      <div
        style={{
          color: W.text2,
          fontFamily: mono ? wMono : undefined,
          fontSize: mono ? 11 : 11.5,
          whiteSpace: "pre-wrap",
          wordBreak: "break-all",
        }}
      >
        {value}
      </div>
    </div>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th
      style={{
        textAlign: "left",
        padding: "8px 14px",
        color: W.dim,
        fontSize: 11,
        fontWeight: 600,
        textTransform: "uppercase",
        letterSpacing: 0.4,
      }}
    >
      {children}
    </th>
  );
}
function Td({ children }: { children: React.ReactNode }) {
  return (
    <td style={{ padding: "8px 14px", verticalAlign: "top" }}>{children}</td>
  );
}
function Card({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 20,
      }}
    >
      {children}
    </div>
  );
}
