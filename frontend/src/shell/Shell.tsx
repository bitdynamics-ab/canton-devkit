import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useEffect, useState } from "react";
import { W, wMono, wSans, wideCaps, tint, R, fs } from "../tokens";
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
  IcPlus,
} from "../components/icons";
import { useTheme, toggleTheme } from "../theme";
import { type ConnectionState, useConnectionHealth } from "./useConnectionHealth";
import { type InstanceSelection, useInstanceSelection } from "./useInstanceSelection";
import { useCreateInstance } from "./useCreateInstance";
import { CommandPalette, openPalette } from "./CommandPalette";
import { NAV, linkTo } from "./routes";
import { ApiError, fetchMetricsSummary } from "../api";

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
      className="app-shell"
      style={{
        display: "grid",
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
        className="app-main"
        // tabIndex=-1 so the SkipLink can programmatically focus it.
        tabIndex={-1}
        style={{
          overflow: "auto",
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

// Match on pathname only; every route may carry the selection query string.
// The Overview ("/") and the fleet table ("/instances") are two faces of
// one section, so both read "Instances" in the breadcrumb — the switcher,
// not the breadcrumb, says which instance you're looking at.
function currentRouteLabel(pathname: string): string {
  if (pathname === "/" || pathname.startsWith("/instances")) return "Instances";
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
      className="app-topbar"
      style={{
        display: "flex",
        alignItems: "center",
        background: W.bg,
        borderBottom: `1px solid ${W.border}`,
      }}
    >
      <LogoLockup />
      {title && (
        <span
          className="app-route-title"
          style={{
            fontSize: fs.body,
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
        className="app-docs-link"
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
          fontSize: fs.meta,
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
      className="app-theme-toggle"
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
  const navigate = useNavigate();
  const create = useCreateInstance();
  if (sel.loading) {
    return <span style={pillStyle(W.dim)}>Loading instances…</span>;
  }
  if (sel.error || sel.instances.length === 0) {
    // No instances (fresh install / registry error): the switcher has nothing
    // to switch, so it becomes the create affordance — a dead "No instances"
    // pill would strand a first-time user with no way to spin one up.
    return (
      <button
        onClick={() => create.open()}
        title="Create a LocalNet instance"
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          height: 28,
          padding: "0 12px",
          borderRadius: 2,
          border: `1px solid ${W.border}`,
          background: W.surface,
          color: W.brandText,
          fontSize: fs.meta,
          fontWeight: 500,
          cursor: "pointer",
        }}
      >
        <IcPlus size={13} />
        New instance
      </button>
    );
  }
  const selected = sel.instances.find((i) => i.name === sel.selected);
  return (
    <div className="instance-switcher" style={{ position: "relative" }}>
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
            fontSize: fs.micro,
            color: W.dim,
          }}
        >
          instance
        </span>
        <strong
          style={{
            color: W.text,
            fontFamily: wMono,
            fontSize: fs.meta,
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {sel.selected ?? "—"}
        </strong>
        <IcChevronDown size={12} style={{ color: W.dim }} />
      </button>
      {open && (
        <div
          className="instance-switcher-menu"
          style={{
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderRadius: R.card,
            zIndex: 10,
            boxShadow: "0 6px 20px rgba(0,0,0,0.16)",
            overflow: "hidden",
          }}
        >
          <div
            className="instance-switcher-grid"
            style={{
              ...SWITCHER_GRID,
              padding: "7px 12px",
              background: W.surface2,
              borderBottom: `1px solid ${W.border}`,
            }}
          >
            <span style={switcherCap}>Instance</span>
            <span style={switcherCap}>State</span>
            <span className="instance-switcher-optional" style={{ ...switcherCap, textAlign: "right" }}>Uptime</span>
            <span className="instance-switcher-optional" style={{ ...switcherCap, textAlign: "right" }}>Ports</span>
          </div>
          <ul role="listbox" style={{ margin: 0, padding: 0, listStyle: "none" }}>
            {sel.instances.map((i) => {
              const isSel = i.name === sel.selected;
              return (
                <li key={i.name}>
                  <button
                    className="instance-switcher-grid"
                    role="option"
                    aria-selected={isSel}
                    onMouseDown={(e) => {
                      // mouseDown fires before the trigger's onBlur, so the
                      // menu doesn't close before the click lands.
                      e.preventDefault();
                      sel.select(i.name);
                      setOpen(false);
                    }}
                    style={{
                      ...SWITCHER_GRID,
                      width: "100%",
                      padding: "8px 12px",
                      background: isSel ? W.brandSoft : "transparent",
                      border: "none",
                      borderTop: `1px solid ${W.border}`,
                      cursor: "pointer",
                      textAlign: "left",
                    }}
                  >
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 8,
                        minWidth: 0,
                      }}
                    >
                      <StatusDot status={i.status} />
                      <strong
                        style={{
                          color: isSel ? W.brandText : W.text,
                          fontFamily: wMono,
                          fontSize: fs.meta,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {i.name}
                      </strong>
                    </span>
                    <StatusBadge status={i.status} />
                    <span className="instance-switcher-optional" style={switcherNum}>{i.started_ago || "—"}</span>
                    <span className="instance-switcher-optional" style={switcherNum}>{i.ports || "—"}</span>
                  </button>
                </li>
              );
            })}
          </ul>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "6px 8px",
              borderTop: `1px solid ${W.border}`,
              background: W.surface2,
            }}
          >
            <button
              onMouseDown={(e) => {
                e.preventDefault();
                setOpen(false);
                create.open();
              }}
              style={switcherAction(W.brandText)}
            >
              <IcPlus size={13} />
              New instance
            </button>
            <button
              onMouseDown={(e) => {
                e.preventDefault();
                setOpen(false);
                navigate(linkTo("/instances", sel.selected));
              }}
              style={switcherAction(W.dim)}
            >
              All instances
              <kbd style={kbdHint}>⌘K</kbd>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// Shared column template for the switcher header + rows so they align.
const SWITCHER_GRID: React.CSSProperties = {
  display: "grid",
  alignItems: "center",
  gap: 12,
};

const switcherCap: React.CSSProperties = {
  ...wideCaps,
  fontSize: fs.micro,
  color: W.dim,
};

const switcherNum: React.CSSProperties = {
  textAlign: "right",
  fontFamily: wMono,
  fontSize: fs.label,
  color: W.dim,
  fontVariantNumeric: "tabular-nums",
};

function switcherAction(color: string): React.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    padding: "5px 8px",
    background: "transparent",
    border: "none",
    borderRadius: R.control,
    color,
    fontSize: fs.meta,
    fontWeight: 500,
    cursor: "pointer",
  };
}

const kbdHint: React.CSSProperties = {
  fontFamily: wMono,
  fontSize: fs.micro,
  color: W.faint,
  border: `1px solid ${W.border}`,
  borderRadius: 3,
  padding: "0 4px",
  marginLeft: 6,
};

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
      className="app-palette-hint"
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
        fontSize: fs.meta,
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
          fontSize: fs.micro,
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
    fontSize: fs.label,
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
      className="app-health-pill"
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
        fontSize: fs.meta,
        cursor: "help",
      }}
    >
      <Dot color={color} size={7} pulse={conn.health !== "ok"} />
      {label}
    </span>
  );
}

// useMonitoringOff probes the selected instance's metrics: the summary
// endpoint throws OBSERVABILITY_PROFILE_OFF when the instance was started
// without the observability profile. Drives the "off" tag on the Metrics
// nav row so the capability's absence is flagged where it's owned. Any
// other outcome (running, unreachable, no instance) shows no tag.
function useMonitoringOff(instance: string | null): boolean {
  const [off, setOff] = useState(false);
  useEffect(() => {
    if (!instance) {
      setOff(false);
      return;
    }
    let cancelled = false;
    fetchMetricsSummary(instance)
      .then(() => {
        if (!cancelled) setOff(false);
      })
      .catch((e: unknown) => {
        if (!cancelled) setOff(e instanceof ApiError && e.code === "OBSERVABILITY_PROFILE_OFF");
      });
    return () => {
      cancelled = true;
    };
  }, [instance]);
  return off;
}

function Sidebar() {
  // Use the resolved selection, not only the raw query parameter: on a bare
  // URL the provider may auto-pick a running instance, and links must carry
  // that pick forward instead of showing it in the header then dropping it.
  const sel = useInstanceSelection();
  const instance = sel.selected;
  const metricsOff = useMonitoringOff(instance);
  return (
    <nav
      className="app-sidebar"
      style={{
        display: "flex",
        background: W.sunken,
      }}
    >
      <div
        className="app-sidebar-label"
        style={{
          ...wideCaps,
          fontSize: fs.micro,
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
            to={linkTo(item.to, instance)}
            end={item.to === "/"}
            className="side-nav-link"
          >
            <Icon size={15} />
            {item.label}
            {item.to === "/metrics" && metricsOff && (
              <span
                title="Started without the observability profile — no metrics collected"
                style={{
                  marginLeft: "auto",
                  ...wideCaps,
                  fontSize: fs.micro,
                  color: W.faint,
                  border: `1px solid ${W.border}`,
                  borderRadius: R.control,
                  padding: "0 5px",
                  lineHeight: "15px",
                }}
              >
                off
              </span>
            )}
          </NavLink>
        );
      })}
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
      <span
        style={{
          fontFamily: wSans,
          fontStretch: "118%",
          color: W.text,
          fontWeight: 600,
          letterSpacing: "0.14em",
          fontSize: fs.body,
        }}
      >
        CANTON DEVKIT
      </span>
    </span>
  );
}
