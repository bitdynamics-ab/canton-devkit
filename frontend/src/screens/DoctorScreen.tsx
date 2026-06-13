import { useCallback, useEffect, useState } from "react";
import {
  ApiError,
  type PreflightCheck,
  type PreflightReport,
  type SpliceVersionEntry,
  fetchDoctor,
  fetchSpliceVersions,
} from "../api";
import { W, wMono } from "../tokens";

// DoctorScreen — the Web UI surface for `dpm localnet doctor`.
//
// It calls GET /api/doctor, which runs the SAME shared
// localnet.CollectDoctor collector the CLI verb uses. That collector
// layers two advisory checks (platform-support matrix + host-port
// availability) on top of the resource/Docker gate that
// /api/preflight already exposes — advisories a Web UI operator
// previously could not see (BIT-131 parity gap). The report shape is
// types.PreflightReport, identical to the create-modal preflight panel,
// so the two surfaces can't drift.
//
// Not instance-scoped: doctor diagnoses the HOST, independent of any
// running instance — so it sits in the nav alongside Overview rather
// than under an instance selector.

export function DoctorScreen() {
  const [report, setReport] = useState<PreflightReport | null>(null);
  const [versions, setVersions] = useState<SpliceVersionEntry[]>([]);
  // "" → server's "latest" alias. The picker lets an operator grade
  // the memory checks against a heavier Splice version's floor before
  // they commit to creating an instance on that version.
  const [version, setVersion] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  // Load the curated version list once so the picker can offer the
  // same tags the create modal does. A failure here is non-fatal: the
  // doctor still runs against "latest", we just hide the picker.
  useEffect(() => {
    let cancelled = false;
    fetchSpliceVersions()
      .then((r) => {
        if (!cancelled) setVersions(r.versions);
      })
      .catch(() => {
        /* picker stays hidden; doctor still works against latest */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const run = useCallback((v: string) => {
    let cancelled = false;
    setLoading(true);
    setErr(null);
    fetchDoctor(v || undefined)
      .then((r) => {
        if (!cancelled) setReport(r);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : "failed to run doctor");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Re-run whenever the selected version changes (including the first
  // mount with the default "latest").
  useEffect(() => run(version), [run, version]);

  return (
    <div style={{ maxWidth: 920 }}>
      <Header
        versions={versions}
        version={version}
        onVersion={setVersion}
        onRerun={() => run(version)}
        loading={loading}
      />

      {report && <SummaryBanner report={report} />}

      {err && (
        <div
          style={{
            marginTop: 16,
            padding: "12px 14px",
            background: `${W.err}1A`,
            border: `1px solid ${W.err}`,
            borderRadius: 8,
            color: W.err,
            fontSize: 13,
          }}
        >
          {err}
        </div>
      )}

      {loading && !report && (
        <div style={{ marginTop: 24, color: W.dim, fontSize: 13 }}>
          Running host checks…
        </div>
      )}

      {report?.sections.map((sec) => (
        <Section key={sec.title} title={sec.title} checks={sec.checks} />
      ))}
    </div>
  );
}

function Header({
  versions,
  version,
  onVersion,
  onRerun,
  loading,
}: {
  versions: SpliceVersionEntry[];
  version: string;
  onVersion: (v: string) => void;
  onRerun: () => void;
  loading: boolean;
}) {
  return (
    <header
      style={{
        display: "flex",
        alignItems: "flex-end",
        gap: 16,
        marginBottom: 8,
      }}
    >
      <div style={{ flex: 1 }}>
        <h1 style={{ margin: 0, fontSize: 18, fontWeight: 600, color: W.text }}>
          Doctor
        </h1>
        <p style={{ margin: "4px 0 0", color: W.dim, fontSize: 13 }}>
          Host readiness for Canton LocalNet — Docker, resources, network,
          platform support. Same checks as{" "}
          <code style={{ color: W.text2, fontFamily: wMono }}>
            dpm localnet doctor
          </code>
          .
        </p>
      </div>
      {versions.length > 0 && (
        <label
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 4,
            color: W.dim,
            fontSize: 11,
          }}
        >
          version thresholds
          <select
            value={version}
            onChange={(e) => onVersion(e.target.value)}
            aria-label="Splice version for resource thresholds"
            style={{
              background: W.surface,
              color: W.text,
              border: `1px solid ${W.border}`,
              borderRadius: 6,
              padding: "5px 8px",
              fontSize: 12,
              fontFamily: wMono,
            }}
          >
            <option value="">latest</option>
            {versions.map((v) => (
              <option key={v.tag} value={v.tag}>
                {v.tag}
              </option>
            ))}
          </select>
        </label>
      )}
      <button
        onClick={onRerun}
        disabled={loading}
        style={{
          background: "transparent",
          color: W.brand,
          border: `1px solid ${W.brand}`,
          borderRadius: 6,
          padding: "6px 14px",
          fontSize: 12.5,
          fontWeight: 600,
          cursor: loading ? "default" : "pointer",
          opacity: loading ? 0.6 : 1,
        }}
      >
        {loading ? "Running…" : "Re-run"}
      </button>
    </header>
  );
}

// SummaryBanner colors itself by the worst result: failing → red,
// warning → amber, all-pass → brand. Mirrors the CLI's colored summary
// Box so the two surfaces read the same.
function SummaryBanner({ report }: { report: PreflightReport }) {
  const warned = report.sections.some((s) =>
    s.checks.some((c) => c.result === "warn"),
  );
  const accent = !report.ok ? W.err : warned ? W.warn : W.ok;
  const glyph = !report.ok ? "✗" : warned ? "⚠" : "✓";
  return (
    <div
      role="status"
      style={{
        marginTop: 16,
        padding: "12px 14px",
        background: `${accent}14`,
        border: `1px solid ${accent}`,
        borderRadius: 8,
        color: accent,
        fontSize: 13,
        fontWeight: 600,
        display: "flex",
        gap: 8,
        alignItems: "center",
      }}
    >
      <span aria-hidden>{glyph}</span>
      <span>{report.summary || (report.ok ? "host is ready" : "host is not ready")}</span>
    </div>
  );
}

function Section({
  title,
  checks,
}: {
  title: string;
  checks: PreflightCheck[];
}) {
  return (
    <section style={{ marginTop: 20 }}>
      <h2
        style={{
          margin: "0 0 8px",
          fontSize: 12,
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: 0.6,
          color: W.dim,
        }}
      >
        {title}
      </h2>
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
          overflow: "hidden",
        }}
      >
        {checks.map((c, i) => (
          <CheckRow key={c.label} check={c} last={i === checks.length - 1} />
        ))}
      </div>
    </section>
  );
}

function CheckRow({ check, last }: { check: PreflightCheck; last: boolean }) {
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        padding: "10px 14px",
        borderBottom: last ? "none" : `1px solid ${W.border}`,
        alignItems: "flex-start",
      }}
    >
      <span
        aria-hidden
        style={{ width: 14, textAlign: "center", marginTop: 1, flexShrink: 0 }}
      >
        <ResultGlyph result={check.result} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color: W.text, fontSize: 13 }}>
          {check.label}
          <span
            style={{
              marginLeft: 8,
              fontSize: 10.5,
              fontFamily: wMono,
              color: glyphColor(check.result),
            }}
          >
            {resultWord(check.result)}
          </span>
        </div>
        {check.detail && (
          <div
            style={{
              color: W.dim,
              fontSize: 11.5,
              fontFamily: wMono,
              marginTop: 2,
            }}
          >
            {check.detail}
          </div>
        )}
        {check.remediation && check.remediation.length > 0 && (
          <ol
            style={{
              margin: "8px 0 0",
              paddingLeft: 18,
              color: W.text2,
              fontSize: 11.5,
              lineHeight: 1.6,
            }}
          >
            {check.remediation.map((step, i) => (
              <li key={i}>{step}</li>
            ))}
          </ol>
        )}
      </div>
    </div>
  );
}

function ResultGlyph({ result }: { result: PreflightCheck["result"] }) {
  const map: Record<PreflightCheck["result"], string> = {
    pass: "✓",
    warn: "⚠",
    fail: "✗",
    skip: "○",
  };
  return <span style={{ color: glyphColor(result) }}>{map[result]}</span>;
}

function glyphColor(result: PreflightCheck["result"]): string {
  switch (result) {
    case "pass":
      return W.ok;
    case "warn":
      return W.warn;
    case "fail":
      return W.err;
    default:
      return W.faint;
  }
}

function resultWord(result: PreflightCheck["result"]): string {
  switch (result) {
    case "pass":
      return "PASS";
    case "warn":
      return "WARN";
    case "fail":
      return "FAIL";
    default:
      return "SKIP";
  }
}
