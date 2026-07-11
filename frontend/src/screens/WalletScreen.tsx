import { useEffect, useState } from "react";
import { ApiError, fetchInstance, type Instance, type Role } from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { ROLE_COLOR, W, wMono, tint, R, FAST, fs } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcRefresh } from "../components/icons";

// Embeds Splice's per-role Wallet UI (the `<role>_ui` host port from
// state.json) in an iframe. Splice 0.6.4 sends no X-Frame-Options, so it
// loads directly; the "Open in new tab" fallback covers a future SAMEORIGIN.
const ROLES: Role[] = ["app-user", "app-provider", "sv"];

// Hardcoded AUTH_<ROLE>_WALLET_ADMIN_USER_NAME values from env/<role>-auth-on.env.
// Password is ignored: LocalNet auth is dev-only HS-256 with the secret "unsafe".
const LOGIN_USER_FOR: Record<Role, string> = {
  "app-user": "app-user",
  "app-provider": "app-provider",
  sv: "sv",
};

// Logical port names from state.json; endpoints match by key, labels are display-only.
const WALLET_ENDPOINT_KEY: Record<Role, string> = {
  "app-user": "app_user_ui",
  "app-provider": "app_provider_ui",
  sv: "sv_ui",
};

// Returns the whole endpoint so callers get both URL and reachability verdict.
function walletEndpointFor(role: Role, endpoints: Instance["endpoints"]) {
  if (!endpoints) return null;
  const want = WALLET_ENDPOINT_KEY[role];
  return endpoints.find((e) => e.key === want) ?? null;
}

export function WalletScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  // Bumped by Retry to re-fetch and re-run the backend reachability probe.
  const [refetchNonce, setRefetchNonce] = useState(0);

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    fetchInstance(name)
      .then((inst) => {
        if (!cancelled) setState({ kind: "ok", instance: inst });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load instance",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, refetchNonce]);

  if (!name) {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.dim }}>
          No instance selected. Create or pick one from the dashboard first.
        </p>
      </section>
    );
  }

  if (state.kind === "loading") {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.dim, fontSize: fs.data }}>Loading wallet…</p>
      </section>
    );
  }

  if (state.kind === "err") {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.err, fontSize: fs.data }}>{state.error}</p>
      </section>
    );
  }

  const walletEndpoint = walletEndpointFor(role, state.instance.endpoints);
  const walletURL = walletEndpoint?.url ?? null;
  // Own the failure state rather than let an iframe render the browser's
  // gray error page on a dead port.
  const walletUnreachable = walletEndpoint?.reachability === "unreachable";

  return (
    <section
      style={{
        padding: 24,
        height: "calc(100vh - 56px)",
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 16,
          flexWrap: "wrap",
        }}
      >
        <div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              marginBottom: 4,
            }}
          >
            <h2 style={{ color: W.text, fontSize: fs.title, margin: 0 }}>Wallet</h2>
            <Pill>provided by Splice</Pill>
          </div>
          <div style={{ color: W.dim, fontSize: fs.lead }}>
            Embedded Splice Wallet · DevKit handles auth + party selection so
            you don't juggle browser tabs.
          </div>
        </div>
        <span style={{ marginLeft: "auto" }} />
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {/* Login help — dev-mode credentials inline, no env-file digging. */}
      <div
        style={{
          background: `${tint(W.brand, 6)}`,
          border: `1px solid ${tint(W.brand, 25)}`,
          borderRadius: 4,
          padding: "10px 14px",
          display: "flex",
          alignItems: "center",
          gap: 12,
          fontSize: fs.meta,
          color: W.text2,
        }}
      >
        <div style={{ lineHeight: 1.5 }}>
          <strong style={{ color: W.text }}>Login:</strong> on the wallet
          landing page, click <em>Log in</em> and enter user name{" "}
          <code
            style={{
              fontFamily: wMono,
              color: W.text,
              background: W.border,
              padding: "1px 6px",
              borderRadius: 2,
            }}
          >
            {LOGIN_USER_FOR[role]}
          </code>
          . Password is unused. LocalNet auth is dev-mode HS-256 with the
          shared secret <code style={{ fontFamily: wMono }}>"unsafe"</code>.
          No MetaMask required.
        </div>
      </div>

      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          padding: "12px 18px",
          display: "grid",
          alignItems: "center",
          gap: 18,
          gridTemplateColumns: "auto 1fr auto",
        }}
      >
        <RoleAvatar role={role} />
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span style={{ fontWeight: 600, fontSize: fs.body, color: W.text }}>
              {role}
            </span>
            <span style={{ color: W.dim, fontSize: fs.meta }}>@{name}</span>
            <Dot color={W.brand} />
          </div>
          <div
            style={{
              color: W.dim,
              fontSize: fs.label,
              fontFamily: wMono,
              marginTop: 4,
            }}
          >
            {walletURL ?? "—  (wallet endpoint not recorded)"}
          </div>
        </div>
        <div style={{ display: "flex", gap: 6 }}>
          {walletURL && (
            <a
              href={walletURL}
              target="_blank"
              rel="noopener noreferrer"
              className="bd-btn bd-btn--secondary bd-btn--sm"
              style={{ textDecoration: "none" }}
            >
              Open in new tab
            </a>
          )}
        </div>
      </div>

      <div
        style={{
          flex: 1,
          minHeight: 0,
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          overflow: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {/* Fake browser chrome: signals this is the real Splice UI, not a reimplementation. */}
        <div
          style={{
            background: W.border,
            borderBottom: `1px solid ${W.border}`,
            padding: "7px 12px",
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontSize: fs.label,
            fontFamily: wMono,
            color: W.text2,
          }}
        >
          <Dot color={W.brand} />
          <span style={{ color: W.dim }}>{walletURL ?? "—"}</span>
          <span style={{ marginLeft: "auto", color: W.dim, fontSize: fs.micro }}>
            signed in as {role} via DevKit JWT
          </span>
        </div>
        {walletUnreachable ? (
          <div
            role="alert"
            style={{ flex: 1, padding: 24, color: W.text2, fontSize: fs.body, lineHeight: 1.6 }}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                color: W.warn,
                fontWeight: 600,
                marginBottom: 6,
              }}
            >
              <IcAlert size={14} /> Wallet UI is not serving HTTP
            </div>
            <p style={{ margin: "0 0 6px" }}>
              The wallet UI for{" "}
              <code style={{ fontFamily: wMono, color: W.text2 }}>{role}</code>{" "}
              accepts connections but returns no HTTP response
              {walletEndpoint?.reachability_detail
                ? ` (${walletEndpoint.reachability_detail})`
                : ""}
              . This usually means the instance was created by an older DevKit
              whose generated port overlay is stale.
            </p>
            <p style={{ margin: "0 0 14px", color: W.dim }}>
              Use <strong>Recreate</strong> on the dashboard, or re-run{" "}
              <code style={{ fontFamily: wMono, color: W.text2 }}>
                dpm localnet up --name {name}
              </code>{" "}
              to regenerate the instance's overlays.
            </p>
            <Button
              variant="secondary"
              icon={<IcRefresh />}
              onClick={() => setRefetchNonce((n) => n + 1)}
            >
              Retry
            </Button>
          </div>
        ) : walletURL ? (
          <iframe
            key={walletURL}
            src={walletURL}
            title={`Splice Wallet — ${role}`}
            // The allow-same-origin + allow-scripts foot-gun only bites when
            // iframe and parent share an origin; here they differ in host and
            // port, so allow-same-origin only lets the wallet reach its own
            // cookies/localStorage (needed for its auth), not the parent.
            sandbox="allow-scripts allow-forms allow-same-origin allow-popups"
            referrerPolicy="no-referrer"
            style={{ flex: 1, border: 0, background: "#FCFCFD" }}
          />
        ) : (
          <div style={{ flex: 1, padding: 24, color: W.dim }}>
            No wallet endpoint recorded for{" "}
            <code style={{ fontFamily: wMono, color: W.text2 }}>{role}</code>{" "}
            on this instance. Splice publishes a per-role wallet UI on a host
            port. Re-run{" "}
            <code style={{ fontFamily: wMono, color: W.text2 }}>
              dpm localnet up --name {name}
            </code>{" "}
            to capture it.
          </div>
        )}
      </div>
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
    <div
      style={{
        display: "flex",
        gap: 1,
        padding: 3,
        background: W.border,
        border: `1px solid ${W.border}`,
        borderRadius: 2,
      }}
    >
      {ROLES.map((id) => {
        const active = id === role;
        return (
          <button
            key={id}
            onClick={() => onChange(id)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 11px",
              borderRadius: R.control,
              border: "none",
              background: active ? tint(W.brand, 16) : "transparent",
              cursor: active ? "default" : "pointer",
              fontSize: fs.meta,
              fontFamily: wMono,
              fontWeight: active ? 600 : 500,
              color: active ? W.text : W.dim,
              transition: `background-color ${FAST}`,
            }}
          >
            <RoleAvatarMini role={id} />
            {id}
          </button>
        );
      })}
    </div>
  );
}

function RoleAvatar({ role }: { role: Role }) {
  const color = ROLE_COLOR[role];
  return (
    <div
      style={{
        width: 36,
        height: 36,
        borderRadius: "50%",
        background: color,
        color: W.onAccent,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontWeight: 600,
        fontSize: fs.data,
      }}
    >
      {role[0].toUpperCase()}
    </div>
  );
}

function RoleAvatarMini({ role }: { role: Role }) {
  const color = ROLE_COLOR[role];
  return (
    <span
      style={{
        width: 16,
        height: 16,
        borderRadius: "50%",
        background: color,
        color: W.onAccent,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontWeight: 600,
        fontSize: fs.micro,
      }}
    >
      {role[0].toUpperCase()}
    </span>
  );
}

function Pill({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        color: W.dim,
        border: `1px solid ${W.border}`,
        padding: "1px 7px",
        borderRadius: 2,
        fontSize: fs.micro,
        fontFamily: wMono,
        background: `${tint(W.border, 25)}`,
      }}
    >
      {children}
    </span>
  );
}
