import { useEffect, useState, type ReactNode } from "react";
import { Route, Routes, useLocation } from "react-router-dom";
import { SCHEMA_VERSION, fetchVersion } from "./api";
import { Shell } from "./shell/Shell";
import { InstanceSelectionProvider } from "./shell/useInstanceSelection";
import { ErrorBoundary } from "./shell/ErrorBoundary";
import { ConfirmHost } from "./components/ConfirmDialog";
import { Dashboard } from "./screens/Dashboard";
import { DoctorScreen } from "./screens/DoctorScreen";
import { Placeholder } from "./screens/Placeholder";
import { MetricsScreen } from "./screens/MetricsScreen";
import { DARScreen } from "./screens/DARScreen";
import { AnalyzerScreen } from "./screens/AnalyzerScreen";
import { ExplorerScreen } from "./screens/ExplorerScreen";
import { WalletScreen } from "./screens/WalletScreen";
import { AgentSkillsScreen } from "./screens/AgentSkillsScreen";
import { TokensScreen } from "./screens/TokensScreen";
import { W, fs } from "./tokens";

// Boots with a schema-version handshake and renders the shell only on a
// match, so the bundle never mis-decodes a mismatched backend's responses.
export function App() {
  const [status, setStatus] = useState<"loading" | "ready" | "mismatch" | "offline">(
    "loading",
  );
  const [serverVersion, setServerVersion] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchVersion()
      .then((v) => {
        if (cancelled) return;
        setServerVersion(v.schema_version);
        setStatus(v.schema_version === SCHEMA_VERSION ? "ready" : "mismatch");
      })
      .catch(() => {
        if (!cancelled) setStatus("offline");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (status !== "ready") {
    return <BootGate status={status} serverVersion={serverVersion} />;
  }

  return (
    <InstanceSelectionProvider>
      <Shell>
        <RoutedSurface />
      </Shell>
      {/* One confirm-dialog host; confirmDialog() from anywhere resolves against it. */}
      <ConfirmHost />
    </InstanceSelectionProvider>
  );
}

// Each route gets its own ErrorBoundary keyed by pathname, so a crash on
// one screen neither follows the user nor takes down the shell.
function RoutedSurface() {
  const loc = useLocation();
  return (
    <Routes>
      <Route path="/" element={<Guard routeKey={loc.pathname}><Dashboard /></Guard>} />
      <Route path="/doctor/*" element={<Guard routeKey={loc.pathname}><DoctorScreen /></Guard>} />
      <Route path="/wallet/*" element={<Guard routeKey={loc.pathname}><WalletScreen /></Guard>} />
      <Route path="/explorer/*" element={<Guard routeKey={loc.pathname}><ExplorerScreen /></Guard>} />
      <Route path="/dar/*" element={<Guard routeKey={loc.pathname}><DARScreen /></Guard>} />
      <Route path="/analyzer/*" element={<Guard routeKey={loc.pathname}><AnalyzerScreen /></Guard>} />
      <Route path="/metrics/*" element={<Guard routeKey={loc.pathname}><MetricsScreen /></Guard>} />
      <Route path="/tokens/*" element={<Guard routeKey={loc.pathname}><TokensScreen /></Guard>} />
      <Route path="/agent/*" element={<Guard routeKey={loc.pathname}><AgentSkillsScreen /></Guard>} />
      <Route path="*" element={<Guard routeKey={loc.pathname}><Placeholder name="Not found" /></Guard>} />
    </Routes>
  );
}

function Guard({ routeKey, children }: { routeKey: string; children: ReactNode }) {
  return <ErrorBoundary routeKey={routeKey}>{children}</ErrorBoundary>;
}

interface BootGateProps {
  status: "loading" | "mismatch" | "offline";
  serverVersion: number | null;
}

function BootGate({ status, serverVersion }: BootGateProps) {
  const containerStyle: React.CSSProperties = {
    minHeight: "100vh",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: 24,
  };
  const cardStyle: React.CSSProperties = {
    background: W.surface,
    border: `1px solid ${W.border}`,
    borderRadius: 8,
    padding: 24,
    maxWidth: 480,
    color: W.text,
  };

  let title = "Connecting to canton-devkit…";
  let body: React.ReactNode = (
    <p style={{ color: W.dim, margin: 0 }}>
      Checking <code style={{ color: W.text2 }}>/api/version</code>.
    </p>
  );
  if (status === "mismatch") {
    title = "Schema-version mismatch";
    body = (
      <>
        <p style={{ color: W.text2, marginTop: 0 }}>
          This UI bundle was built for schema_version{" "}
          <strong style={{ color: W.text }}>{SCHEMA_VERSION}</strong> but the
          server reports{" "}
          <strong style={{ color: W.warn }}>{serverVersion}</strong>.
        </p>
        <p style={{ color: W.dim, marginBottom: 0 }}>
          Restart with the matching <code>dpm</code> binary, or rebuild the
          frontend (<code>make frontend</code>) against the running server.
        </p>
      </>
    );
  }
  if (status === "offline") {
    title = "Server unreachable";
    body = (
      <>
        <p style={{ color: W.text2, marginTop: 0 }}>
          Couldn't reach the canton-devkit API at{" "}
          <code style={{ color: W.text }}>/api/version</code>.
        </p>
        <p style={{ color: W.dim, marginBottom: 0 }}>
          Make sure <code>dpm localnet ui</code> is running. The Web UI is
          loopback-only; remote access requires an SSH tunnel.
        </p>
      </>
    );
  }
  return (
    <div style={containerStyle}>
      <div style={cardStyle}>
        <h2 style={{ marginTop: 0, fontSize: fs.strong, fontWeight: 600 }}>{title}</h2>
        {body}
      </div>
    </div>
  );
}
