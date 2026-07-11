import { useCallback, useEffect, useState } from "react";
import {
  ApiError,
  type PreflightCheck,
  type PreflightReport,
  type SpliceVersionEntry,
  fetchDoctor,
  fetchSpliceVersions,
} from "../api";
import { W, wMono, wideCaps, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import { Dot, IcAlert, IcCheck, IcRefresh, IcX } from "../components/icons";

// Web UI surface for `dpm localnet doctor`: GET /api/doctor runs the
// same shared CollectDoctor collector as the CLI. Host-scoped, not per-instance.
export function DoctorScreen() {
  const [report, setReport] = useState<PreflightReport | null>(null);
  const [versions, setVersions] = useState<SpliceVersionEntry[]>([]);
  // "" → server's "latest" alias; the picker grades memory checks
  // against a chosen Splice version's floor before committing to it.
  const [version, setVersion] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  // Non-fatal: on failure the picker hides and doctor runs against "latest".
  useEffect(() => {
    let cancelled = false;
    fetchSpliceVersions()
      .then((r) => {
        if (!cancelled) setVersions(r.versions);
      })
      .catch(() => {});
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

      {err && <DoctorError message={err} onRetry={() => run(version)} />}

      {loading && !report && !err && <DoctorLoading />}

      {report?.sections.map((sec) => (
        <Section key={sec.title} title={sec.title} checks={sec.checks} />
      ))}
    </div>
  );
}

function DoctorError({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div
      role="alert"
      style={{
        marginTop: 16,
        padding: "12px 14px",
        background: `${tint(W.err, 8)}`,
        border: `1px solid ${W.err}`,
        borderRadius: R.control,
        color: W.text,
        fontSize: fs.body,
      }}
    >
      <strong style={{ color: W.err }}>Couldn't run host checks.</strong>{" "}
      The doctor endpoint didn't respond. Confirm the devkit server is up,
      then retry.
      <div style={{ marginTop: 10, display: "flex", gap: 8 }}>
        <Button variant="secondary" size="sm" icon={<IcRefresh />} onClick={onRetry}>
          Retry
        </Button>
      </div>
      <details style={{ marginTop: 8 }}>
        <summary style={{ cursor: "pointer", color: W.dim, fontSize: fs.small }}>
          Server message
        </summary>
        <div
          style={{
            marginTop: 6,
            color: W.text2,
            fontFamily: wMono,
            fontSize: fs.small,
          }}
        >
          {message}
        </div>
      </details>
    </div>
  );
}

function DoctorLoading() {
  const shown = useLoadingDelay(true);
  if (!shown) return null;
  return (
    <div style={{ marginTop: 20 }}>
      <SkeletonTable columns={["18px", 3, "60px"]} rows={4} rowHeight={40} />
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
        <h1 style={{ margin: 0, fontSize: fs.h3, fontWeight: 600, color: W.text }}>
          Doctor
        </h1>
        <p style={{ margin: "4px 0 0", color: W.dim, fontSize: fs.body }}>
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
            fontSize: fs.small,
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
              borderRadius: R.control,
              padding: "5px 8px",
              fontSize: fs.small,
              fontFamily: wMono,
              fontVariantNumeric: "tabular-nums",
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
      <Button
        variant="secondary"
        size="sm"
        icon={<IcRefresh />}
        onClick={onRerun}
        disabled={loading}
      >
        {loading ? "Running…" : "Re-run"}
      </Button>
    </header>
  );
}

// Colored by the worst result: fail → red, warn → amber, all-pass → brand.
function SummaryBanner({ report }: { report: PreflightReport }) {
  const warned = report.sections.some((s) =>
    s.checks.some((c) => c.result === "warn"),
  );
  const accent = !report.ok ? W.err : warned ? W.warn : W.ok;
  const glyph = !report.ok ? (
    <IcX size={13} />
  ) : warned ? (
    <IcAlert size={13} />
  ) : (
    <IcCheck size={13} />
  );
  return (
    <div
      role="status"
      style={{
        marginTop: 16,
        padding: "12px 14px",
        background: tint(accent, 8),
        border: `1px solid ${accent}`,
        borderRadius: R.control,
        color: accent,
        fontSize: fs.body,
        fontWeight: 600,
        display: "flex",
        gap: 8,
        alignItems: "center",
      }}
    >
      <span
        aria-hidden
        style={{ display: "inline-flex", alignItems: "center", flexShrink: 0 }}
      >
        {glyph}
      </span>
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
          ...wideCaps,
          margin: "0 0 8px",
          fontSize: fs.small,
          color: W.dim,
        }}
      >
        {title}
      </h2>
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: R.card,
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
        style={{
          width: 14,
          display: "inline-flex",
          justifyContent: "center",
          marginTop: 3,
          flexShrink: 0,
        }}
      >
        <ResultGlyph result={check.result} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color: W.text, fontSize: fs.body }}>
          {check.label}
          <span
            style={{
              marginLeft: 8,
              fontSize: fs.caption,
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
              fontSize: fs.small,
              fontFamily: wMono,
              fontVariantNumeric: "tabular-nums",
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
              fontSize: fs.small,
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
  const color = glyphColor(result);
  const inner =
    result === "pass" ? (
      <IcCheck size={12} />
    ) : result === "warn" ? (
      <IcAlert size={12} />
    ) : result === "fail" ? (
      <IcX size={12} />
    ) : (
      <Dot color={color} size={6} />
    );
  return (
    <span style={{ display: "inline-flex", alignItems: "center", color }}>
      {inner}
    </span>
  );
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
