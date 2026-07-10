import { useEffect, useState } from "react";
import { ApiError, fetchInstance, type Instance, type Role } from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { ROLE_COLOR, W, wMono, tint, R, FAST } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcRefresh } from "../components/icons";

// WalletScreen embeds Splice's per-role Wallet UI inside the DevKit
// shell so users don't juggle three browser tabs (one per party).
// The iframe target is the `<role>_ui` host port from state.json —
// Splice already exposes its wallet there; we just frame it with a
// role switcher.
//
// X-Frame-Options is empty on Splice 0.6.4's wallet UI (nginx sends
// no frame-options header), so the iframe loads directly. If a future
// Splice release ships SAMEORIGIN, the "Open in new tab" fallback
// covers it.
const ROLES: Role[] = ["app-user", "app-provider", "sv"];

// LocalNet wallet login user names — the hardcoded
// AUTH_<ROLE>_WALLET_ADMIN_USER_NAME values from
// `env/<role>-auth-on.env`. Password is ignored: LocalNet auth is
// dev-only HS-256 with the literal secret "unsafe" (no MetaMask,
// no real OAuth provider).
const LOGIN_USER_FOR: Record<Role, string> = {
  "app-user": "app-user",
  "app-provider": "app-provider",
  sv: "sv",
};

// Per-role wallet endpoint keys — the logical port names from
// state.json. Endpoints are matched by key; labels are display-only.
const WALLET_ENDPOINT_KEY: Record<Role, string> = {
  "app-user": "app_user_ui",
  "app-provider": "app_provider_ui",
  sv: "sv_ui",
};

// walletEndpointFor returns the whole endpoint so callers can read
// both the URL and the backend's reachability verdict.
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
  // Bumped by Retry to re-fetch the instance, which re-runs the
  // backend reachability probe.
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
        <p style={{ color: W.dim, fontSize: 13 }}>Loading wallet…</p>
      </section>
    );
  }

  if (state.kind === "err") {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.err, fontSize: 13 }}>{state.error}</p>
      </section>
    );
  }

  // null when the instance doesn't yet have endpoints surfaced.
  const walletEndpoint = walletEndpointFor(role, state.instance.endpoints);
  const walletURL = walletEndpoint?.url ?? null;
  // Backend status probe verdict. An iframe pointed at a dead port
  // renders the browser's own gray error page; own the failure state
  // instead and point at the fix.
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
      {/* Header */}
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
            <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>Wallet</h2>
            <Pill>provided by Splice</Pill>
          </div>
          <div style={{ color: W.dim, fontSize: 12.5 }}>
            Embedded Splice Wallet · DevKit handles auth + party selection so
            you don't juggle browser tabs.
          </div>
        </div>
        <span style={{ marginLeft: "auto" }} />
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {/* Login help — surface the dev-mode credentials inline so
          users don't have to dig through env files. */}
      <div
        style={{
          background: `${tint(W.brand, 6)}`,
          border: `1px solid ${tint(W.brand, 25)}`,
          borderRadius: 4,
          padding: "10px 14px",
          display: "flex",
          alignItems: "center",
          gap: 12,
          fontSize: 12,
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

      {/* Active wallet info strip */}
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
            <span style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
              {role}
            </span>
            <span style={{ color: W.dim, fontSize: 12 }}>@{name}</span>
            <Dot color={W.brand} />
          </div>
          <div
            style={{
              color: W.dim,
              fontSize: 11.5,
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

      {/* Embedded wallet iframe */}
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
        {/* Fake browser chrome so devs know they're looking at the
            real Splice UI inside our shell, not a re-implementation. */}
        <div
          style={{
            background: W.border,
            borderBottom: `1px solid ${W.border}`,
            padding: "7px 12px",
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontSize: 11.5,
            fontFamily: wMono,
            color: W.text2,
          }}
        >
          <Dot color={W.brand} />
          <span style={{ color: W.dim }}>{walletURL ?? "—"}</span>
          <span style={{ marginLeft: "auto", color: W.dim, fontSize: 10.5 }}>
            signed in as {role} via DevKit JWT
          </span>
        </div>
        {walletUnreachable ? (
          <div
            role="alert"
            style={{ flex: 1, padding: 24, color: W.text2, fontSize: 13, lineHeight: 1.6 }}
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
            // Sandbox rationale: the "allow-same-origin +
            // allow-scripts neutralises the sandbox" foot-gun only
            // applies when the iframe origin EQUALS the parent's.
            // Here the parent (127.0.0.1:<port>) and the iframe
            // (wallet.localhost:<per-role port>) differ in host AND
            // port, so they are cross-origin: `allow-same-origin`
            // lets the wallet's scripts read ITS OWN cookies /
            // localStorage (needed for its auth session) but not the
            // parent's origin or `window.top`. Without it the iframe
            // would get a unique opaque origin and the wallet's
            // cookie-based auth would break. Deliberately omitted:
            // allow-top-navigation, allow-modals, allow-downloads,
            // allow-popups-to-escape-sandbox.
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
              fontSize: 12.5,
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
        fontSize: 13,
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
        fontSize: 9,
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
        fontSize: 10.5,
        fontFamily: wMono,
        background: `${tint(W.border, 25)}`,
      }}
    >
      {children}
    </span>
  );
}
