import { useEffect, useState } from "react";
import { ApiError, fetchInstance, type Instance, type Role } from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { ROLE_COLOR, W, wMono } from "../tokens";

// WalletScreen — BIT-192 (new in design drop 2026-05-26).
//
// Embeds Splice's per-role Wallet UI inside the DevKit shell so
// users don't juggle three browser tabs (one per party). The
// iframe target is the existing `<role>_ui` host port from
// state.json — Splice exposes its wallet on those ports already;
// we just frame them with a switcher.
//
// X-Frame-Options check: confirmed empty on Splice 0.6.4's
// wallet UI (served by nginx with no frame-options header), so
// the iframe loads without CORS gymnastics. If a future Splice
// release ships SAMEORIGIN headers, the "Open in new tab"
// fallback covers that case.
const ROLES: Role[] = ["app-user", "app-provider", "sv"];

// LocalNet wallet login — Splice's LocalNet ships a self-signed
// auth flow (NOT MetaMask, NOT a real OAuth provider). On the
// wallet landing page click "Log in" and enter the role's
// validator user name when prompted. These are the hardcoded
// names from `env/<role>-auth-on.env`:
//   AUTH_<ROLE>_WALLET_ADMIN_USER_NAME
// (`app-user`, `app-provider`, `sv`). Password is ignored —
// auth is dev-only HS-256 with the literal secret "unsafe".
const LOGIN_USER_FOR: Record<Role, string> = {
  "app-user": "app-user",
  "app-provider": "app-provider",
  sv: "sv",
};

// roleLabel matches the backend's Endpoint.Label format:
// "Wallet · <role>". Resolves a role to the matching endpoint URL.
function walletURLFor(role: Role, endpoints: Instance["endpoints"]): string | null {
  if (!endpoints) return null;
  const want = `Wallet · ${role}`;
  return endpoints.find((e) => e.label === want)?.url ?? null;
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
  }, [name]);

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

  // Resolve the per-role wallet URL from the endpoints projection
  // (BIT-192 — detail handler emits one per role). Falls back to
  // null if the instance doesn't yet have endpoints surfaced.
  const walletURL = walletURLFor(role, state.instance.endpoints);

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

      {/* Login help — Splice's LocalNet wallet uses a self-signed
          dev-mode auth flow (NOT MetaMask). Surface the credentials
          inline so users don't have to dig through env files. */}
      <div
        style={{
          background: `${W.brand}10`,
          border: `1px solid ${W.brand}40`,
          borderRadius: 10,
          padding: "10px 14px",
          display: "flex",
          alignItems: "center",
          gap: 12,
          fontSize: 12,
          color: W.text2,
        }}
      >
        <span style={{ color: W.brand, fontSize: 14 }}>🔑</span>
        <div style={{ lineHeight: 1.5 }}>
          <strong style={{ color: W.text }}>Login:</strong> on the wallet
          landing page, click <em>Log in</em> and enter user name{" "}
          <code
            style={{
              fontFamily: wMono,
              color: W.text,
              background: W.border,
              padding: "1px 6px",
              borderRadius: 4,
            }}
          >
            {LOGIN_USER_FOR[role]}
          </code>
          . Password is unused — LocalNet auth is dev-mode HS-256 with the
          shared secret <code style={{ fontFamily: wMono }}>"unsafe"</code>.
          No MetaMask required.
        </div>
      </div>

      {/* Active wallet info strip */}
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
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
              style={btn(W.brand)}
            >
              ↗ Open in new tab
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
          borderRadius: 10,
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
          <span style={{ color: W.brand }}>●</span>
          <span style={{ color: W.dim }}>{walletURL ?? "—"}</span>
          <span style={{ marginLeft: "auto", color: W.dim, fontSize: 10.5 }}>
            signed in as {role} via DevKit JWT
          </span>
        </div>
        {walletURL ? (
          <iframe
            key={walletURL}
            src={walletURL}
            title={`Splice Wallet — ${role}`}
            // Cross-origin sandbox analysis (review follow-up to B3).
            //
            // The well-known "allow-same-origin + allow-scripts
            // neutralises the sandbox" foot-gun applies when the
            // iframe origin EQUALS the parent's. Here the parent is
            // `127.0.0.1:7777` (our devkit server) and the iframe is
            // `wallet.localhost:<per-role port>` (Splice's nginx vhost).
            // The HTML spec's origin tuple is (scheme, host, port) —
            // host AND port differ — so they are cross-origin. With
            // `allow-same-origin`, the iframe's own scripts can read
            // ITS OWN document.cookie / localStorage (which the wallet
            // needs for its auth session), but it CANNOT touch the
            // parent's origin (the devkit's cookies, our /api/* JWTs
            // in localStorage, or `window.top`). Without
            // `allow-same-origin`, the iframe would be in a unique
            // opaque origin and the wallet's cookie-based auth would
            // break on every render.
            //
            // What we deliberately did NOT include:
            //   - allow-top-navigation       (would let wallet redirect us)
            //   - allow-modals               (no alert/confirm spam)
            //   - allow-downloads            (no arbitrary downloads)
            //   - allow-storage-access-by-user-activation (not needed)
            //   - allow-popups-to-escape-sandbox — dropped here. We
            //     don't expect the wallet to open auth popups (Splice
            //     LocalNet uses self-signed inline auth, no OAuth
            //     redirect). If a future wallet release needs popups
            //     that escape, we'll add it back deliberately.
            sandbox="allow-scripts allow-forms allow-same-origin allow-popups"
            referrerPolicy="no-referrer"
            style={{ flex: 1, border: 0, background: "#FAFAF8" }}
          />
        ) : (
          <div style={{ flex: 1, padding: 24, color: W.dim }}>
            No wallet endpoint recorded for{" "}
            <code style={{ fontFamily: wMono, color: W.text2 }}>{role}</code>{" "}
            on this instance. Splice publishes a per-role wallet UI on a host
            port — re-run{" "}
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
        borderRadius: 9,
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
              borderRadius: 6,
              border: "none",
              background: active ? W.surface : "transparent",
              cursor: active ? "default" : "pointer",
              fontSize: 12.5,
              fontFamily: wMono,
              fontWeight: active ? 600 : 500,
              color: active ? W.text : W.dim,
              boxShadow: active ? `0 0 0 1px ${W.brand}` : "none",
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
        background: `linear-gradient(135deg, ${color}, ${W.brand})`,
        color: "#0B0E13",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontWeight: 700,
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
        background: `linear-gradient(135deg, ${color}, ${W.brand})`,
        color: "#0B0E13",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontWeight: 700,
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
        borderRadius: 4,
        fontSize: 10.5,
        fontFamily: wMono,
        background: `${W.border}40`,
      }}
    >
      {children}
    </span>
  );
}

function Dot({ color }: { color: string }) {
  return (
    <span
      style={{
        width: 6,
        height: 6,
        borderRadius: "50%",
        background: color,
        display: "inline-block",
      }}
    />
  );
}

function btn(color: string): React.CSSProperties {
  return {
    background: "transparent",
    color,
    border: `1px solid ${color}`,
    borderRadius: 6,
    padding: "5px 12px",
    fontSize: 12,
    fontWeight: 600,
    textDecoration: "none",
    fontFamily: "inherit",
  };
}
