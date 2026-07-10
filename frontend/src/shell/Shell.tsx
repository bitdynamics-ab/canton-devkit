import { NavLink, useLocation, useSearchParams } from "react-router-dom";
import { useState } from "react";
import { W, wMono, wSans, wideCaps, tint, R } from "../tokens";
import { StatusBadge } from "../components/StatusBadge";
import {
  Dot,
  IcOverview,
  IcDoctor,
  IcWallet,
  IcExplorer,
  IcPackage,
  IcMetrics,
  IcTokens,
  IcAgent,
  IcSun,
  IcMoon,
  IcBook,
  IcChevronDown,
} from "../components/icons";
import { useTheme, toggleTheme } from "../theme";
import { type ConnectionState, useConnectionHealth } from "./useConnectionHealth";
import { type InstanceSelection, useInstanceSelection } from "./useInstanceSelection";
import { CommandPalette, openPalette } from "./CommandPalette";
import { NAV, linkTo } from "./routes";

const NAV_ICON: Record<string, (p: { size?: number }) => JSX.Element> = {
  "/": IcOverview,
  "/doctor": IcDoctor,
  "/wallet": IcWallet,
  "/explorer": IcExplorer,
  "/dar": IcPackage,
  "/metrics": IcMetrics,
  "/tokens": IcTokens,
  "/agent": IcAgent,
};

const DOCS_URL = "https://bitdynamics-ab.github.io/canton-devkit/";

interface ShellProps {
  children: React.ReactNode;
}

export function Shell({ children }: ShellProps) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "232px 1fr",
        gridTemplateRows: "52px 1fr",
        height: "100vh",
        fontFamily: wSans,
        background: W.bg,
      }}
    >
      <SkipLink />
      <TopBar />
      <Sidebar />
      <main
        id="main-content"
        // tabIndex=-1 so the SkipLink can programmatically focus it.
        tabIndex={-1}
        style={{
          gridColumn: "2",
          gridRow: "2",
          overflow: "auto",
          padding: 24,
          background: W.bg,
          // SkipLink target is a focus destination, not a control; no outline.
          outline: "none",
        }}
      >
        {children}
      </main>
      <CommandPalette />
    </div>
  );
}

function SkipLink() {
  return (
    <a
      href="#main-content"
      className="skip-link"
      onClick={(e) => {
        // Hash-jump alone scrolls but doesn't move keyboard focus.
        e.preventDefault();
        const main = document.getElementById("main-content");
        if (main) {
          main.focus();
          main.scrollIntoView({ block: "start" });
        }
      }}
    >
      Skip to main content
    </a>
  );
}

// Match on pathname only; instance-scoped routes carry a query string.
function currentRouteLabel(pathname: string): string {
  const hit = NAV.find((n) => n.to === pathname);
  return hit ? hit.label : "";
}

function TopBar() {
  const conn = useConnectionHealth();
  const sel = useInstanceSelection();
  const { pathname } = useLocation();
  const title = currentRouteLabel(pathname);
  return (
    <header
      style={{
        gridColumn: "1 / span 2",
        gridRow: "1",
        display: "flex",
        alignItems: "center",
        gap: 14,
        padding: "0 16px",
        background: W.bg,
        borderBottom: `1px solid ${W.border}`,
      }}
    >
      <LogoLockup />
      {title && (
        <span
          style={{
            fontSize: 14,
            fontWeight: 600,
            fontStretch: "104%",
            color: W.text,
          }}
        >
          {title}
        </span>
      )}
      <InstanceSwitcher sel={sel} />
      <div style={{ flex: 1 }} />
      <PaletteHint />
      <HealthPill conn={conn} />
      <ThemeToggle />
      <a
        href={DOCS_URL}
        target="_blank"
        rel="noopener noreferrer"
        title="Open the documentation"
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          padding: "0 10px",
          height: 28,
          borderRadius: 2,
          border: `1px solid ${W.border}`,
          color: W.text2,
          fontSize: 12,
          textDecoration: "none",
        }}
      >
        <IcBook size={13} />
        Docs
      </a>
    </header>
  );
}

function ThemeToggle() {
  const theme = useTheme();
  const next = theme === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      onClick={toggleTheme}
      title={`Switch to ${next} theme`}
      aria-label={`Switch to ${next} theme`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: 28,
        height: 28,
        borderRadius: 2,
        border: `1px solid ${W.border}`,
        background: "transparent",
        color: W.text2,
        cursor: "pointer",
      }}
    >
      {theme === "dark" ? <IcSun size={14} /> : <IcMoon size={14} />}
    </button>
  );
}

function InstanceSwitcher({ sel }: { sel: InstanceSelection }) {
  const [open, setOpen] = useState(false);
  if (sel.loading) {
    return <span style={pillStyle(W.dim)}>Loading instances…</span>;
  }
  if (sel.error || sel.instances.length === 0) {
    return <span style={pillStyle(W.dim)}>No instances</span>;
  }
  const selected = sel.instances.find((i) => i.name === sel.selected);
  return (
    <div style={{ position: "relative" }}>
      <button
        onClick={() => setOpen((v) => !v)}
        onBlur={() => {
          // Defer so a click on a menu item registers before we unmount.
          setTimeout(() => setOpen(false), 100);
        }}
        aria-haspopup="listbox"
        aria-expanded={open}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          height: 28,
          padding: "0 10px",
          borderRadius: 2,
          border: `1px solid ${W.border}`,
          background: W.surface,
          cursor: "pointer",
        }}
      >
        <StatusDot status={selected?.status ?? "stopped"} />
        <span
          style={{
            ...wideCaps,
            fontSize: 10,
            color: W.dim,
          }}
        >
          instance
        </span>
        <strong
          style={{
            color: W.text,
            fontFamily: wMono,
            fontSize: 12,
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {sel.selected ?? "—"}
        </strong>
        <IcChevronDown size={12} style={{ color: W.dim }} />
      </button>
      {open && (
        <ul
          role="listbox"
          style={{
            position: "absolute",
            top: "calc(100% + 4px)",
            left: 0,
            margin: 0,
            padding: 4,
            listStyle: "none",
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderRadius: R.card,
            minWidth: 240,
            zIndex: 10,
            boxShadow: "0 6px 20px rgba(0,0,0,0.16)",
          }}
        >
          {sel.instances.map((i) => (
            <li key={i.name}>
              <button
                role="option"
                aria-selected={i.name === sel.selected}
                onMouseDown={(e) => {
                  // mouseDown fires before the button's onBlur, so the
                  // menu doesn't close before the click lands.
                  e.preventDefault();
                  sel.select(i.name);
                  setOpen(false);
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  width: "100%",
                  padding: "7px 10px",
                  background:
                    i.name === sel.selected ? W.brandSoft : "transparent",
                  border: "none",
                  borderRadius: R.control,
                  color: i.name === sel.selected ? W.brandText : W.text,
                  fontFamily: wMono,
                  fontSize: 12,
                  textAlign: "left",
                  cursor: "pointer",
                }}
              >
                <span style={{ flex: 1 }}>{i.name}</span>
                <StatusBadge status={i.status} />
                <span
                  style={{
                    color: W.dim,
                    fontSize: 11,
                    fontVariantNumeric: "tabular-nums",
                  }}
                >
                  {i.splice_version}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function statusColor(status: string): string {
  return status === "running"
    ? W.ok
    : status === "failed"
      ? W.err
      : status === "stopped"
        ? W.dim
        : W.warn;
}

function StatusDot({ status }: { status: string }) {
  return <Dot color={statusColor(status)} size={6} />;
}

function PaletteHint() {
  // ⌘ on Mac, Ctrl elsewhere.
  const isMac =
    typeof navigator !== "undefined" && /Mac/i.test(navigator.platform);
  const mod = isMac ? "⌘" : "Ctrl";
  return (
    <button
      type="button"
      onClick={openPalette}
      title="Open the command palette"
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 8,
        height: 28,
        padding: "0 10px",
        borderRadius: 2,
        border: `1px solid ${W.border}`,
        background: "transparent",
        color: W.dim,
        fontSize: 12,
        cursor: "pointer",
      }}
    >
      Commands
      <kbd
        style={{
          background: W.surface2,
          border: `1px solid ${W.border}`,
          borderRadius: 2,
          padding: "1px 5px",
          fontFamily: wMono,
          fontSize: 10,
          color: W.text2,
        }}
      >
        {mod} K
      </kbd>
    </button>
  );
}

function pillStyle(color: string): React.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    height: 28,
    padding: "0 10px",
    borderRadius: 2,
    border: `1px solid ${W.border}`,
    color,
    fontFamily: wMono,
    fontSize: 11,
  };
}

function HealthPill({ conn }: { conn: ConnectionState }) {
  const { color, label, tooltip } = (() => {
    switch (conn.health) {
      case "ok":
        return {
          color: W.ok,
          label: "Connected",
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
          label: "Offline",
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
        gap: 7,
        height: 28,
        padding: "0 11px",
        borderRadius: 2,
        border: `1px solid ${tint(color, 40)}`,
        background: tint(color, 10),
        color,
        fontSize: 12,
        cursor: "help",
      }}
    >
      <Dot color={color} size={7} pulse={conn.health !== "ok"} />
      {label}
    </span>
  );
}

function Sidebar() {
  // Thread the selected instance into per-instance routes so sidebar
  // clicks don't drop the selection.
  const [params] = useSearchParams();
  const instance = params.get("instance");
  const conn = useConnectionHealth();
  return (
    <nav
      style={{
        gridColumn: "1",
        gridRow: "2",
        display: "flex",
        flexDirection: "column",
        background: W.sunken,
        borderRight: `1px solid ${W.border}`,
        padding: "16px 10px 10px",
      }}
    >
      <div
        style={{
          ...wideCaps,
          fontSize: 10,
          color: W.faint,
          padding: "0 8px 8px",
        }}
      >
        LocalNet
      </div>
      {NAV.map((item) => {
        const Icon = NAV_ICON[item.to] ?? IcOverview;
        return (
          <NavLink
            key={item.to}
            to={linkTo(item.to, item.instanceScoped, instance)}
            end={item.to === "/"}
            className="side-nav-link"
          >
            <Icon size={15} />
            {item.label}
          </NavLink>
        );
      })}
      <div style={{ flex: 1 }} />
      <div
        style={{
          borderTop: `1px solid ${W.border}`,
          padding: "10px 8px 2px",
          display: "flex",
          flexDirection: "column",
          gap: 4,
          fontSize: 11,
          color: W.faint,
          fontFamily: wMono,
        }}
      >
        <span>loopback only</span>
        <span>
          {conn.serverVersion != null
            ? `schema v${conn.serverVersion}`
            : "connecting…"}
        </span>
      </div>
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
      <svg width="20" height="20" viewBox="0 0 64 64" aria-label="BitDynamics">
        <rect x="10" y="10" width="44" height="44" fill="none" stroke={W.dim} strokeWidth="3" />
        <rect x="22" y="22" width="20" height="20" fill={W.brand} />
        <line x1="32" y1="0" x2="32" y2="8" stroke={W.brand} strokeWidth="4" />
        <line x1="32" y1="56" x2="32" y2="64" stroke={W.dim} strokeWidth="3" />
      </svg>
      <span
        style={{
          fontFamily: wSans,
          fontStretch: "118%",
          color: W.text,
          fontWeight: 600,
          letterSpacing: "0.14em",
          fontSize: 12,
        }}
      >
        BITDYNAMICS
      </span>
      <span style={{ fontFamily: wMono, fontSize: 10, color: W.dim }}>.cc</span>
    </span>
  );
}
