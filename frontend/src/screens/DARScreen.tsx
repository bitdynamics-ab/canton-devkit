import { useEffect, useState } from "react";
import {
  ApiError,
  fetchDARList,
  type DARListResponse,
  type Role,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// DARScreen — BIT-187.
//
// Reads the participant's uploaded DAR list via the Admin API
// (proxied through GET /api/instances/:name/dar?role=). MVP is
// read-only: list table + role switcher. Upload, detail drawer,
// and diff are tracked as follow-ups in the ticket.
const ROLES: Role[] = ["app-user", "app-provider", "sv"];

export function DARScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: DARListResponse }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    fetchDARList(name, role)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        // PARTICIPANT_PORT_NOT_RECORDED is the "instance pre-dates
        // BIT-190" code — surface the remediation as a discoverable
        // empty state instead of as a hard error.
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
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load DARs",
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
          DAR Manager —{" "}
          <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>
        </h2>
        <span style={{ marginLeft: "auto" }} />
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {state.kind === "loading" && (
        <Card>
          <p style={{ color: W.dim, fontSize: 13 }}>Loading DARs…</p>
        </Card>
      )}

      {state.kind === "err" && (
        <Card>
          <p style={{ color: W.err, fontSize: 13 }}>{state.error}</p>
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
            port mappings. The DAR Manager can't reach the participant Admin
            API without them.
          </p>
          <p style={{ color: W.dim, fontSize: 12, marginTop: 12 }}>
            {state.remediation}
          </p>
        </Card>
      )}

      {state.kind === "ok" && <DARTable data={state.data} />}
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

function DARTable({ data }: { data: DARListResponse }) {
  if (data.dars.length === 0) {
    return (
      <Card>
        <p style={{ color: W.dim, fontSize: 13 }}>
          No DARs uploaded on role{" "}
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
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          fontSize: 12.5,
        }}
      >
        <thead>
          <tr style={{ background: W.border }}>
            <Th>Name</Th>
            <Th>Version</Th>
            <Th>Package ID</Th>
            <Th>Description</Th>
          </tr>
        </thead>
        <tbody>
          {data.dars.map((d, i) => (
            <tr
              key={d.main}
              style={{
                borderBottom:
                  i < data.dars.length - 1 ? `1px solid ${W.border}` : "none",
              }}
            >
              <Td>
                <code style={{ fontFamily: wMono, color: W.text }}>
                  {d.name}
                </code>
              </Td>
              <Td>
                <code style={{ fontFamily: wMono, color: W.brand }}>
                  {d.version}
                </code>
              </Td>
              <Td>
                <code
                  style={{
                    fontFamily: wMono,
                    color: W.dim,
                    fontSize: 11,
                  }}
                  title={d.main}
                >
                  {d.main.slice(0, 12)}…{d.main.slice(-6)}
                </code>
              </Td>
              <Td>
                <span style={{ color: W.dim, fontSize: 11.5 }}>
                  {d.description || "—"}
                </span>
              </Td>
            </tr>
          ))}
        </tbody>
      </table>
      <div
        style={{
          padding: "8px 14px",
          color: W.dim,
          fontSize: 11,
          borderTop: `1px solid ${W.border}`,
          background: W.surface,
        }}
      >
        {data.dars.length} package{data.dars.length === 1 ? "" : "s"} uploaded
        on{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>{data.role}</code>
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
