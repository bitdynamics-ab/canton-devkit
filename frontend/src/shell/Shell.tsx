import { NavLink } from "react-router-dom";
import { W, wMono, wSans } from "../tokens";
import { type ConnectionState, useConnectionHealth } from "./useConnectionHealth";

// Shell — sidebar + topbar layout from docs/design/mockups/webui-shell.jsx
// (AppShell + TopBar). Children render in the main content area.
//
// Routes are hard-listed (matching App.tsx) rather than scraped
// from a config because the sidebar order is a design decision,
// not an alphabetical accident. New screens add one row here +
// one route in App.tsx — the intentional friction keeps the
// sidebar curated.
//
// LogoLockup is inlined as an inline SVG that mirrors the
// webui-shell.jsx::LogoLockup component (chip+candle mark +
// "BITDYNAMICS" wordmark). Same source as terminal.jsx +
// docs/design/mockups/assets/bitdynamics-lockup.svg; we draw it
// inline here so the shell renders correctly even before the
// public/assets/ files are served.

interface ShellProps {
  children: React.ReactNode;
}

export function Shell({ children }: ShellProps) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "240px 1fr",
        gridTemplateRows: "48px 1fr",
        height: "100vh",
        fontFamily: wSans,
      }}
    >
      <TopBar />
      <Sidebar />
      <main
        style={{
          gridColumn: "2",
          gridRow: "2",
          overflow: "auto",
          padding: 24,
          background: W.bg,
        }}
      >
        {children}
      </main>
    </div>
  );
}

function TopBar() {
  const conn = useConnectionHealth();
  return (
    <header
      style={{
        gridColumn: "1 / span 2",
        gridRow: "1",
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "0 16px",
        background: W.surface,
        borderBottom: `1px solid ${W.border}`,
      }}
    >
      <LogoLockup />
      <span style={{ color: W.dim, fontSize: 12 }}>
        canton-devkit · local development
      </span>
      <div style={{ flex: 1 }} />
      <span style={{ color: W.faint, fontSize: 11 }}>
        loopback only · ssh -L for remote
      </span>
      <HealthPill conn={conn} />
    </header>
  );
}

function HealthPill({ conn }: { conn: ConnectionState }) {
  const { color, label, tooltip } = (() => {
    switch (conn.health) {
      case "ok":
        return {
          color: W.ok,
          label: `v${conn.serverVersion}`,
          tooltip: `Connected · schema v${conn.serverVersion}`,
        };
      case "mismatch":
        return {
          color: W.warn,
          label: `v${conn.serverVersion} ≠ ours`,
          tooltip: `Server speaks schema v${conn.serverVersion}; this bundle was built for a different version. Reload after rebuilding.`,
        };
      case "offline":
        return {
          color: W.err,
          label: "offline",
          tooltip:
            conn.serverVersion != null
              ? `Lost connection · last seen schema v${conn.serverVersion}`
              : "Server unreachable",
        };
    }
  })();
  return (
    <span
      title={tooltip}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        padding: "3px 9px",
        borderRadius: 999,
        border: `1px solid ${color}`,
        background: `${color}1A`,
        color,
        fontFamily: wMono,
        fontSize: 11,
        cursor: "help",
      }}
    >
      <span
        aria-hidden
        style={{
          width: 7,
          height: 7,
          borderRadius: "50%",
          background: color,
          // Pulse only when degraded so a healthy connection
          // doesn't add ambient motion to the chrome.
          animation: conn.health === "ok" ? undefined : "pulse 1.6s ease-in-out infinite",
        }}
      />
      {label}
    </span>
  );
}

const NAV: Array<{ to: string; label: string }> = [
  { to: "/", label: "Overview" },
  { to: "/explorer", label: "Explorer" },
  { to: "/dar", label: "DAR Manager" },
  { to: "/metrics", label: "Metrics" },
  { to: "/tokens", label: "Tokens" },
  { to: "/agent", label: "Agent Skills" },
];

function Sidebar() {
  return (
    <nav
      style={{
        gridColumn: "1",
        gridRow: "2",
        background: W.surface,
        borderRight: `1px solid ${W.border}`,
        padding: "16px 8px",
      }}
    >
      {NAV.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to === "/"}
          style={({ isActive }) => ({
            display: "block",
            padding: "8px 12px",
            margin: "2px 0",
            borderRadius: 6,
            color: isActive ? W.text : W.text2,
            background: isActive ? W.surface2 : "transparent",
            fontWeight: isActive ? 600 : 400,
          })}
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}

function LogoLockup() {
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 8,
        lineHeight: 1,
      }}
    >
      <svg width="22" height="22" viewBox="0 0 64 64" aria-label="BitDynamics">
        <rect x="10" y="10" width="44" height="44" fill="none" stroke={W.dim} strokeWidth="3" />
        <rect x="22" y="22" width="20" height="20" fill={W.brand} />
        <line x1="32" y1="0" x2="32" y2="8" stroke={W.brand} strokeWidth="4" />
        <line x1="32" y1="56" x2="32" y2="64" stroke={W.dim} strokeWidth="3" />
      </svg>
      <span
        style={{
          fontFamily: "'JetBrains Mono', ui-monospace, monospace",
          color: W.text,
          fontWeight: 600,
          letterSpacing: 1.2,
          fontSize: 12,
        }}
      >
        BITDYNAMICS
      </span>
    </span>
  );
}
