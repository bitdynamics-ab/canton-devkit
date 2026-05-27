import { useEffect, useState } from "react";
import { ApiError, fetchMetricsSummary, type MetricsSummary } from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// MetricsScreen — BIT-188.
//
// Headline panel for the per-instance Canton metrics, backed by
// `GET /api/instances/:name/metrics/summary` (which wraps the
// shared `internal/metricsq` PromQL queries). Renders four
// numbers and an "Open Grafana" action when the observability
// profile is up; falls back to a clear "enable observability"
// empty state otherwise.
//
// Why four headline cards and not live charts: the charts would
// add a chart library (or a hand-rolled SVG component) for
// marginal value over a 5-second refresh on the summary
// endpoint. Charts are tracked as a follow-up — see the BIT-188
// "Future" notes.
export function MetricsScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: MetricsSummary }
    | { kind: "observability-off"; instance: string; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    fetchMetricsSummary(name)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (
          e instanceof ApiError &&
          // OBSERVABILITY_PROFILE_OFF is the canonical marker the
          // backend emits when none of the headline queries returned
          // data AND Prometheus itself isn't running for the project.
          // The frontend treats that as a discoverable empty state,
          // not an error — the user has a clear next action.
          e.code === "OBSERVABILITY_PROFILE_OFF"
        ) {
          setState({
            kind: "observability-off",
            instance: name,
            remediation:
              e.remediation?.[0] ??
              `Restart this instance with \`dpm localnet up --profile observability --name ${name}\`.`,
          });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load metrics",
        });
      });
    // Refresh every 5s while the user is on the screen — keeps
    // the headline numbers live without a full chart library.
    const t = setInterval(() => {
      fetchMetricsSummary(name)
        .then((data) => {
          if (!cancelled) setState({ kind: "ok", data });
        })
        .catch(() => {
          /* keep last good state on transient errors */
        });
    }, 5000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [name]);

  if (!name) {
    return (
      <Card>
        <p style={{ color: W.dim }}>
          No instance selected. Create or pick one from the dashboard first.
        </p>
      </Card>
    );
  }

  return (
    <section style={{ padding: 24 }}>
      <h2 style={{ color: W.text, fontSize: 18, marginBottom: 16 }}>
        Metrics —{" "}
        <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>
      </h2>

      {state.kind === "loading" && (
        <Card>
          <p style={{ color: W.dim, fontSize: 13 }}>Loading…</p>
        </Card>
      )}

      {state.kind === "err" && (
        <Card>
          <p style={{ color: W.err, fontSize: 13 }}>{state.error}</p>
        </Card>
      )}

      {state.kind === "observability-off" && (
        <ObservabilityOffPanel
          instance={state.instance}
          remediation={state.remediation}
        />
      )}

      {state.kind === "ok" && <HeadlineGrid data={state.data} />}
    </section>
  );
}

function HeadlineGrid({ data }: { data: MetricsSummary }) {
  const m = data.metrics;
  const cards: Array<{ label: string; value: string; sub?: string }> = [
    {
      label: "Ledger throughput",
      value: formatTPS(m.ledger_tps_5m),
      sub: "tx/s · 5-min rate",
    },
    {
      label: "Mediator p95 latency",
      value: formatSeconds(m.mediator_p95_seconds),
      sub: "approval duration",
    },
    {
      label: "JVM heap used",
      value: formatBytes(m.jvm_heap_used_bytes),
      sub: "across all canton processes",
    },
    {
      label: "Postgres connections",
      value: formatCount(m.postgres_conn_count),
      sub: "active sessions",
    },
  ];
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
        gap: 12,
      }}
    >
      {cards.map((c) => (
        <div
          key={c.label}
          style={{
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderRadius: 10,
            padding: 16,
          }}
        >
          <div
            style={{
              color: W.dim,
              fontSize: 11.5,
              textTransform: "uppercase",
              letterSpacing: 0.4,
            }}
          >
            {c.label}
          </div>
          <div
            style={{
              color: W.text,
              fontSize: 28,
              fontWeight: 600,
              fontFamily: wMono,
              marginTop: 6,
            }}
          >
            {c.value}
          </div>
          {c.sub && (
            <div style={{ color: W.dim, fontSize: 11, marginTop: 4 }}>
              {c.sub}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

function ObservabilityOffPanel({
  instance,
  remediation,
}: {
  instance: string;
  remediation: string;
}) {
  return (
    <div
      style={{
        background: `${W.warn}10`,
        border: `1px solid ${W.warn}`,
        borderRadius: 10,
        padding: 20,
      }}
    >
      <h3
        style={{ color: W.warn, fontSize: 14, marginTop: 0, marginBottom: 8 }}
      >
        Observability profile not enabled
      </h3>
      <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>
        Instance{" "}
        <code style={{ fontFamily: wMono, color: W.brand }}>{instance}</code>{" "}
        was started without the observability profile. Prometheus and Grafana
        aren't running, so there's nothing to scrape.
      </p>
      <p
        style={{
          color: W.dim,
          fontSize: 12,
          marginTop: 12,
          marginBottom: 4,
        }}
      >
        {remediation.includes("`")
          ? remediation.split(/(`[^`]+`)/).map((part, i) =>
              part.startsWith("`") && part.endsWith("`") ? (
                <code
                  key={i}
                  style={{
                    fontFamily: wMono,
                    color: W.text,
                    background: W.border,
                    padding: "1px 6px",
                    borderRadius: 4,
                  }}
                >
                  {part.slice(1, -1)}
                </code>
              ) : (
                <span key={i}>{part}</span>
              ),
            )
          : remediation}
      </p>
      <p style={{ color: W.dim, fontSize: 11, marginTop: 16 }}>
        After bringing the instance up with{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>
          --profile observability
        </code>
        , this page will populate automatically.
      </p>
    </div>
  );
}

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 20,
      }}
    >
      {children}
    </div>
  );
}

// Formatting helpers — keep the dashboard quiet about missing
// metrics rather than rendering NaN. "—" is the universal "we
// don't have this number" placeholder used elsewhere in the
// dashboard (InstanceDetail's uptime row, for instance).
function formatTPS(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  if (v < 0.01) return v.toExponential(1);
  return v.toFixed(2);
}
function formatSeconds(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  if (v >= 1) return v.toFixed(2) + " s";
  return Math.round(v * 1000) + " ms";
}
function formatBytes(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let n = v;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return n.toFixed(n < 10 && i > 0 ? 1 : 0) + " " + units[i];
}
function formatCount(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return Math.round(v).toString();
}
