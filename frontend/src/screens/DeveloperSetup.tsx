import { useEffect, useState } from "react";
import {
  ApiError,
  type AppConfigFormat,
  type JwtResponse,
  fetchAppConfigJSON,
  fetchAppConfigText,
  issueJwt,
} from "../api";
import { W, wMono } from "../tokens";

// DeveloperSetup — the "Developer setup" card. Two sub-panels:
//
//  1. JWT generator: role/audience picker + a usable token preview +
//     copy button. LocalNet is loopback-only with dev-secret tokens
//     (the dev-secret warning renders below), so the raw token is
//     surfaced directly — no redaction toggle.
//
//  2. App config exporter: format tabs (env / json / yaml) + monospace
//     preview + copy button, all backed by
//     /api/instances/{name}/app-config?format=.
//
// The Dashboard owns instance selection; this component just receives
// `name` as a prop.

const ROLES = ["app-provider", "app-user", "sv"] as const;
type Role = (typeof ROLES)[number];

// The backend redacts JWTs by default; this LocalNet-only UI opts
// into the raw token (?include_jwt=true) so the generated token is
// usable as-is. The dev-secret warning makes the trade-off explicit.
export function DeveloperSetup({ name }: { name: string }) {
  return (
    <div
      style={{
        marginTop: 24,
        display: "grid",
        gap: 16,
        gridTemplateColumns: "1fr 1fr",
      }}
    >
      <JwtPanel name={name} />
      <AppConfigPanel name={name} />
    </div>
  );
}

function JwtPanel({ name }: { name: string }) {
  const [role, setRole] = useState<Role>("app-provider");
  const [audience, setAudience] = useState("https://canton.network.global");
  const [jwt, setJwt] = useState<JwtResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Issue a usable JWT on mount + whenever role/audience/name changes.
  // include_jwt=true so the raw token is returned — LocalNet only.
  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    issueJwt(name, { role, audience }, true)
      .then((r) => {
        if (!cancelled) setJwt(r);
        setErr(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : "failed to issue JWT");
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [name, role, audience]);

  const token = jwt?.token ?? null;

  return (
    <Card title="JWT generator" subtitle="Signed against the Splice LocalNet dev secret">
      <Row label="role">
        <ChipRow
          options={ROLES as unknown as string[]}
          value={role}
          onChange={(v) => setRole(v as Role)}
        />
      </Row>
      <Row label="audience">
        <input
          value={audience}
          onChange={(e) => setAudience(e.target.value)}
          style={{
            flex: 1,
            background: W.bg,
            color: W.text,
            border: `1px solid ${W.border}`,
            borderRadius: 6,
            padding: "6px 10px",
            fontSize: 13,
            fontFamily: wMono,
          }}
        />
      </Row>
      <Row label="party">
        <code style={{ color: W.text2, fontFamily: wMono, fontSize: 12 }}>
          {jwt?.party ?? "—"}
        </code>
      </Row>
      <div style={{ marginTop: 12 }}>
        <TokenBox token={busy ? "…" : token ?? "—"} revealed={!!token} />
        <div
          style={{
            display: "flex",
            gap: 8,
            marginTop: 8,
            alignItems: "center",
          }}
        >
          <button
            onClick={() => token && navigator.clipboard.writeText(token)}
            disabled={!token}
            style={btnStyle(W.brand)}
          >
            Copy
          </button>
        </div>
      </div>
      {jwt?.warning_dev_secret && (
        <p
          style={{
            color: W.warn,
            fontSize: 11.5,
            marginTop: 12,
            marginBottom: 0,
            lineHeight: 1.5,
          }}
        >
          {jwt.warning_dev_secret}
        </p>
      )}
      {err && <ErrorLine msg={err} />}
    </Card>
  );
}

function AppConfigPanel({ name }: { name: string }) {
  const [format, setFormat] = useState<AppConfigFormat>("env");
  const [body, setBody] = useState<string>("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    const p =
      format === "json"
        ? fetchAppConfigJSON(name).then((j) =>
            JSON.stringify(j, null, 2),
          )
        : fetchAppConfigText(name, format);
    p.then((text) => {
      if (cancelled) return;
      setBody(text);
      setErr(null);
    })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : "failed to load config");
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [name, format]);

  return (
    <Card
      title="App config"
      subtitle="endpoints + party IDs in your preferred format"
    >
      <Row label="format">
        <ChipRow
          options={["env", "json", "yaml"] as AppConfigFormat[]}
          value={format}
          onChange={(v) => setFormat(v as AppConfigFormat)}
        />
      </Row>
      <pre
        style={{
          margin: "12px 0 0",
          background: W.bg,
          border: `1px solid ${W.border}`,
          borderRadius: 7,
          padding: "10px 12px",
          fontFamily: wMono,
          fontSize: 11.5,
          color: W.text2,
          lineHeight: 1.5,
          maxHeight: 200,
          overflow: "auto",
          whiteSpace: "pre-wrap",
        }}
      >
        {busy ? "…" : body || "—"}
      </pre>
      <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
        <button
          onClick={() => navigator.clipboard.writeText(body)}
          style={btnStyle(W.brand)}
          disabled={!body}
        >
          Copy
        </button>
      </div>
      {err && <ErrorLine msg={err} />}
    </Card>
  );
}

// ──────────────────────── shared primitives ─────────────────────────
//
// Kept inline while this screen is the only consumer; promote to a
// shared module when a second screen needs them.

interface CardProps {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}

function Card({ title, subtitle, children }: CardProps) {
  return (
    <section
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 16,
      }}
    >
      <header style={{ marginBottom: 12 }}>
        <div style={{ fontWeight: 600, fontSize: 14, color: W.text }}>{title}</div>
        {subtitle && (
          <div style={{ color: W.dim, fontSize: 12, marginTop: 2 }}>
            {subtitle}
          </div>
        )}
      </header>
      {children}
    </section>
  );
}

interface RowProps {
  label: string;
  children: React.ReactNode;
}

function Row({ label, children }: RowProps) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        margin: "6px 0",
      }}
    >
      <span
        style={{
          color: W.dim,
          fontSize: 12,
          width: 80,
          flexShrink: 0,
        }}
      >
        {label}
      </span>
      {children}
    </div>
  );
}

interface ChipRowProps {
  options: string[];
  value: string;
  onChange: (v: string) => void;
}

function ChipRow({ options, value, onChange }: ChipRowProps) {
  return (
    <div style={{ display: "flex", gap: 6 }}>
      {options.map((opt) => (
        <button
          key={opt}
          onClick={() => onChange(opt)}
          style={{
            background: opt === value ? W.brand : W.surface2,
            color: opt === value ? "#082018" : W.text2,
            border: `1px solid ${opt === value ? W.brand : W.border}`,
            borderRadius: 6,
            padding: "4px 10px",
            fontSize: 11.5,
            fontWeight: opt === value ? 600 : 400,
            cursor: "pointer",
          }}
        >
          {opt}
        </button>
      ))}
    </div>
  );
}

function TokenBox({ token, revealed }: { token: string; revealed: boolean }) {
  // Split the JWT into header.payload.signature for the colored
  // preview. Placeholders ("—", "…") aren't 3-part tokens, so they
  // render as plain text.
  const parts = token.split(".");
  const isJwt = parts.length === 3 && revealed;
  return (
    <div
      style={{
        background: W.bg,
        border: `1px solid ${W.border}`,
        borderRadius: 7,
        padding: "10px 12px",
        fontFamily: wMono,
        fontSize: 11,
        wordBreak: "break-all",
        lineHeight: 1.5,
        color: W.text2,
      }}
    >
      {isJwt ? (
        <>
          <span style={{ color: W.mag }}>{parts[0]}</span>
          <span style={{ color: W.dim }}>.</span>
          <span style={{ color: W.brand }}>{parts[1]}</span>
          <span style={{ color: W.dim }}>.</span>
          <span style={{ color: W.amber }}>{parts[2]}</span>
        </>
      ) : (
        <span style={{ color: W.dim }}>{token}</span>
      )}
    </div>
  );
}

function btnStyle(accent: string): React.CSSProperties {
  return {
    background: accent === W.brand ? W.brand : "transparent",
    color: accent === W.brand ? "#082018" : accent,
    border: `1px solid ${accent}`,
    borderRadius: 6,
    padding: "4px 10px",
    fontSize: 11.5,
    fontWeight: 600,
    cursor: "pointer",
  };
}

function ErrorLine({ msg }: { msg: string }) {
  return (
    <div
      style={{
        color: W.err,
        marginTop: 8,
        fontSize: 12,
      }}
    >
      {msg}
    </div>
  );
}
