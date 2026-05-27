import { useEffect, useState } from "react";
import { Route, Routes } from "react-router-dom";
import { SCHEMA_VERSION, fetchVersion } from "./api";
import { Shell } from "./shell/Shell";
import { InstanceSelectionProvider } from "./shell/useInstanceSelection";
import { Dashboard } from "./screens/Dashboard";
import { Placeholder } from "./screens/Placeholder";
import { W } from "./tokens";

// App boots by doing the schema-version handshake against the
// backend. Until the handshake completes (or fails) we render a
// minimal loading panel — we never want to render UI that
// silently mis-decodes a v2 backend.
//
// Handshake outcomes:
//   - match: render the shell + routed screens
//   - mismatch: refuse to render, tell the user to restart
//   - network error: refuse to render, suggest the binary isn't running
//
// All three outcomes show the same loopback-only / dev-binary
// guidance so the user knows what's expected of their host.
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
        <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/explorer/*" element={<Placeholder name="Explorer" ticket="BIT-132" />} />
        <Route path="/dar/*" element={<Placeholder name="DAR Manager" ticket="BIT-127" />} />
        <Route path="/metrics/*" element={<Placeholder name="Metrics" ticket="BIT-134" />} />
        <Route path="/tokens/*" element={<Placeholder name="Tokens" ticket="BIT-140" />} />
        <Route path="/agent/*" element={<Placeholder name="Agent Skills" ticket="BIT-135" />} />
        <Route path="*" element={<Placeholder name="Not found" />} />
        </Routes>
      </Shell>
    </InstanceSelectionProvider>
  );
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
    borderRadius: 12,
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
        <h2 style={{ marginTop: 0, fontSize: 16, fontWeight: 600 }}>{title}</h2>
        {body}
      </div>
    </div>
  );
}
