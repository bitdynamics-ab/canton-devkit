import { useEffect, useMemo, useState, type CSSProperties } from "react";
import {
  ApiError,
  fetchMetricsRange,
  fetchMetricsSummary,
  setObservability,
  type MetricsSummary,
  type PrometheusRangeResponse,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono, tint } from "../tokens";
import { Button } from "../components/Button";
import { IcX } from "../components/icons";
import { MetricCard } from "../components/MetricCard";
import { AreaChart } from "../components/charts/AreaChart";
import { MultiLine } from "../components/charts/MultiLine";
import { BarChart, type Bar } from "../components/charts/BarChart";
import { Heatmap, type Cell } from "../components/charts/Heatmap";
import {
  CHART_PALETTE,
  decodePrometheusRange,
  type Series,
} from "../components/charts/types";

// MetricsScreen — live Canton + Splice metrics for the selected
// instance: a 4-up MetricCard strip with deltas + sparklines, six
// PromQL-backed chart cards, and a full-width "Top error sources"
// bar chart. Auto-refreshes every 5 s (the cards load in parallel).
// When the observability profile isn't enabled, a friendly
// empty-state panel offers to turn it on.

interface CardState<T> {
  kind: "loading" | "ok" | "err";
  data?: T;
  error?: string;
}

// PromQL queries. Sourced from internal/metricsq for parity with the
// CLI's `localnet metrics` headline; the per-template / phase /
// heatmap queries are extensions specific to this screen.
//
// All metric names are the daml_* / db_client_* / jvm_* families the
// Splice OTel reporter actually emits (verified against a live obs
// profile). Some per-screen extensions have no direct daml_*
// equivalent on Splice 0.6.4 — the closest functional analogue is
// used instead, marked inline (see docs/observability.md).
const Q = {
  // Substitute: indexer-update counter, same as HeadlineLedgerTPS.
  throughputSeries:
    "sum(rate(daml_participant_api_indexer_updates[1m])) or vector(0)",
  p99: 'histogram_quantile(0.99, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))',
  // Live Splice does not expose total ACS cardinality as a stock
  // Prometheus metric. This is the audited ACS-related signal that
  // exists in 0.6.4; keep UI copy honest and call it a lookup buffer.
  acsLookupBuffer:
    "sum(daml_participant_api_index_db_active_contract_lookup_batch_buffer_length)",
  // No daml_* command-rejection counter on Splice 0.6.4 — use the
  // user-error completion-status counter as a proxy for "things
  // the participant refused to commit". Returns 0 if not exposed.
  errorsRate:
    'sum(rate(daml_grpc_server_handled_total{grpc_code!="OK"}[1m])) or vector(0)',
  latencyMedian:
    'histogram_quantile(0.50, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))',
  latencyP99:
    'histogram_quantile(0.99, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))',
  // Splice 0.6.x does not expose template-grain submission counters.
  // Use the live gRPC method counter as a command-throughput fallback
  // instead of querying a non-existent `daml_commands_*` family.
  commandThroughput:
    "sum by (grpc_method_name) (rate(daml_grpc_server_handled_total[5m]))",
  errors1m:
    'sum(rate(daml_grpc_server_handled_total{grpc_code!="OK"}[1m])) or vector(0)',
  errorsByCode:
    'sum by (grpc_code) (rate(daml_grpc_server_handled_total{grpc_code!="OK"}[1m]))',
  resourceUsage:
    'sum by (component) (jvm_memory_used_bytes{jvm_memory_type="heap"})',
};

// scopeQ injects instance="<scope>" into every metric selector of a chart
// query when the summary reports a scope — i.e. when this instance is
// served by the shared multi-instance Prometheus, so a chart shows
// one instance, not the sum across all of them. It targets our known
// metric-name prefixes, so it never touches function names (sum, rate,
// histogram_quantile) or `by (...)` label lists, and composes with a
// metric's existing label without an invalid trailing comma. An empty
// scope (the single-instance per-instance Prometheus) returns the query
// unchanged.
export function scopeQ(query: string, scope: string): string {
  if (!scope) return query;
  const inst = `instance="${scope}"`;
  return query.replace(
    /\b(daml_[a-z0-9_]+|jvm_[a-z0-9_]+|db_client[a-z0-9_]+)(\{[^}]*\})?/g,
    (_full: string, metric: string, braces?: string) => {
      if (!braces || braces === "{}") return `${metric}{${inst}}`;
      return `${metric}{${inst},${braces.slice(1)}`;
    },
  );
}

const TPS_COLOR = "#8FA3EE";
const P99_COLOR = "#DDB25E";
const ACS_COLOR = "#6480E6";
const ERR_COLOR = "#7BD2C6";

export function MetricsScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [summary, setSummary] = useState<CardState<MetricsSummary>>({
    kind: "loading",
  });
  const [observabilityOff, setObservabilityOff] = useState<string | null>(null);
  const [throughputSeries, setThroughputSeries] = useState<CardState<Series>>({
    kind: "loading",
  });
  const [p99Series, setP99Series] = useState<CardState<Series>>({
    kind: "loading",
  });
  const [acsSeries, setAcsSeries] = useState<CardState<Series>>({
    kind: "loading",
  });
  const [errorsSeries, setErrorsSeries] = useState<CardState<Series>>({
    kind: "loading",
  });
  const [latencyPhase, setLatencyPhase] = useState<CardState<Series[]>>({
    kind: "loading",
  });
  const [perTemplate, setPerTemplate] = useState<CardState<Bar[]>>({
    kind: "loading",
  });
  const [cpuSeries, setCpuSeries] = useState<CardState<Series[]>>({
    kind: "loading",
  });
  const [heatmap, setHeatmap] = useState<CardState<Cell[]>>({ kind: "loading" });
  const [topErrors, setTopErrors] = useState<CardState<Bar[]>>({
    kind: "loading",
  });

  useEffect(() => {
    if (!name) return;
    // An AbortSignal (not a boolean flag) reaches in-flight loaders:
    // fetch aborts mid-flight and loaders short-circuit on
    // signal.aborted, so nothing setStates on an unmounted component.
    // Polling is gated on document.visibilityState — no point
    // hammering Prometheus when the tab is hidden.
    let outer: AbortController | null = null;
    const tick = async () => {
      // Abort the prior tick's in-flight requests — a slow query from
      // t=0 must not resolve after the t=5s query and clobber it.
      outer?.abort();
      outer = new AbortController();
      const signal = outer.signal;
      // Instance label to scope the chart queries by — set when the
      // summary reports we're reading the shared multi-instance stack.
      let scope = "";
      try {
        const s = await fetchMetricsSummary(name, signal);
        if (signal.aborted) return;
        scope = s.scope ?? "";
        setSummary({ kind: "ok", data: s });
        setObservabilityOff(null);
      } catch (e) {
        if (signal.aborted) return;
        if (
          e instanceof ApiError &&
          e.code === "OBSERVABILITY_PROFILE_OFF"
        ) {
          setObservabilityOff(
            e.remediation?.[0] ??
              `dpm localnet up --profile observability --name ${name}`,
          );
          return;
        }
        setSummary({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed",
        });
      }
      await Promise.all([
        loadSeries(name, scopeQ(Q.throughputSeries, scope), "tx/s", setThroughputSeries, signal),
        loadSeries(name, scopeQ(Q.p99, scope), "p99", setP99Series, signal),
        loadSeries(name, scopeQ(Q.acsLookupBuffer, scope), "ACS lookup buffer", setAcsSeries, signal),
        loadSeries(name, scopeQ(Q.errorsRate, scope), "errors", setErrorsSeries, signal),
        loadMultiSeries(
          name,
          [
            { query: scopeQ(Q.latencyMedian, scope), label: "median", color: CHART_PALETTE[1] },
            { query: scopeQ(Q.latencyP99, scope), label: "p99", color: CHART_PALETTE[3] },
          ],
          setLatencyPhase,
          signal,
        ),
        loadBars(
          name,
          scopeQ(Q.commandThroughput, scope),
          (m) => m.grpc_method_name ?? "(unlabelled)",
          setPerTemplate,
          signal,
        ),
        loadBars(
          name,
          scopeQ(Q.errorsByCode, scope),
          (m) => m.grpc_code ?? "(unknown)",
          setTopErrors,
          signal,
        ),
        loadMultiSeriesGrouped(
          name,
          scopeQ(Q.resourceUsage, scope),
          (m) => m.component ?? "component",
          setCpuSeries,
          signal,
        ),
        loadHeatmap(
          name,
          scopeQ('sum(increase(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[1m])) by (le)', scope),
          setHeatmap,
          signal,
        ),
      ]);
    };
    tick();
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      tick();
    }, 5000);
    return () => {
      outer?.abort();
      clearInterval(t);
    };
  }, [name]);

  // These memos MUST sit above every conditional return below so hook
  // order is stable across the (!name) and (observabilityOff)
  // early-exit paths — rules of hooks.
  const tpsDelta = useMemo(() => deltaFromSeries(throughputSeries.data), [throughputSeries.data]);
  const p99Delta = useMemo(() => deltaFromSeries(p99Series.data, 1000), [p99Series.data]);
  const acsDelta = useMemo(() => deltaFromSeries(acsSeries.data), [acsSeries.data]);
  const errDelta = useMemo(() => deltaFromSeries(errorsSeries.data), [errorsSeries.data]);

  if (!name) {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.dim }}>
          No instance selected. Create or pick one from the dashboard first.
        </p>
      </section>
    );
  }

  if (observabilityOff !== null) {
    return (
      <section style={{ padding: 24 }}>
        <Header name={name} />
        <ObservabilityOffPanel
          name={name}
          onEnabled={() => {
            // Clearing the empty state lets the ongoing 5s poll
            // repopulate from the newly-running Prometheus.
            setObservabilityOff(null);
          }}
        />
      </section>
    );
  }

  const m = summary.data?.metrics;
  const p99Value =
    summary.kind === "ok" && summary.data
      ? (summary.data.latency?.p99_ms ?? Number.NaN)
      : undefined;

  return (
    <section style={{ padding: 24 }}>
      <Header name={name} />

      {/* 4-up top strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, 1fr)",
          gap: 12,
          marginBottom: 16,
        }}
      >
        <MetricCard
          title="Throughput"
          unit="tx/s"
          value={m?.ledger_tps_5m}
          sparkline={throughputSeries.data?.points}
          sparklineColor={TPS_COLOR}
          error={throughputSeries.kind === "err" ? throughputSeries.error : undefined}
          delta={tpsDelta}
          deltaPolarity="up-is-good"
        />
        <MetricCard
          title="Command completion p99"
          unit="ms"
          value={p99Value}
          sparkline={p99Series.data?.points.map((p) => ({ t: p.t, v: p.v * 1000 }))}
          sparklineColor={P99_COLOR}
          error={p99Series.kind === "err" ? p99Series.error : undefined}
          delta={p99Delta}
          deltaPolarity="down-is-good"
          format={(v) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1))}
        />
        <MetricCard
          title="ACS lookup buffer"
          value={acsSeries.data?.points[acsSeries.data.points.length - 1]?.v}
          sparkline={acsSeries.data?.points}
          sparklineColor={ACS_COLOR}
          error={acsSeries.kind === "err" ? acsSeries.error : undefined}
          delta={acsDelta}
          deltaPolarity="up-is-good"
          format={(v) => v.toLocaleString()}
        />
        <MetricCard
          title="Errors"
          unit="/min"
          value={
            errorsSeries.data?.points[errorsSeries.data.points.length - 1]?.v
          }
          sparkline={errorsSeries.data?.points}
          sparklineColor={ERR_COLOR}
          error={errorsSeries.kind === "err" ? errorsSeries.error : undefined}
          delta={errDelta}
          deltaPolarity="down-is-good"
        />
      </div>

      {/* 2-col chart grid */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: 12,
          marginBottom: 16,
        }}
      >
        <ChartCard title="Latency by phase" subtitle="median · p99 — last hour">
          {latencyPhase.kind === "err" ? (
            <ErrLine msg={latencyPhase.error ?? "failed"} />
          ) : (
            <MultiLine
              series={latencyPhase.data ?? []}
              width={420}
              height={170}
              format={(v) => (v >= 1 ? v.toFixed(2) + "s" : (v * 1000).toFixed(0) + "ms")}
            />
          )}
        </ChartCard>

        <ChartCard
          title="Command throughput"
          subtitle="best-effort · gRPC methods / 5m"
        >
          {perTemplate.kind === "err" ? (
            <ErrLine msg={perTemplate.error ?? "failed"} />
          ) : (
            <BarChart
              bars={perTemplate.data ?? []}
              defaultColor={CHART_PALETTE[1]}
              width={420}
            />
          )}
        </ChartCard>

        <ChartCard title="ACS lookup buffer" subtitle="buffer length · 1h">
          {acsSeries.kind === "err" ? (
            <ErrLine msg={acsSeries.error ?? "failed"} />
          ) : acsSeries.data ? (
            <AreaChart
              series={{ ...acsSeries.data, color: ACS_COLOR }}
              width={420}
              height={170}
              format={(v) => v.toLocaleString()}
            />
          ) : null}
        </ChartCard>

        <ChartCard title="Ledger errors" subtitle="commands rejected / min">
          {errorsSeries.kind === "err" ? (
            <ErrLine msg={errorsSeries.error ?? "failed"} />
          ) : errorsSeries.data ? (
            <AreaChart
              series={{ ...errorsSeries.data, color: ERR_COLOR }}
              width={420}
              height={170}
            />
          ) : null}
        </ChartCard>

        <ChartCard title="Resource usage" subtitle="JVM heap bytes — components">
          {cpuSeries.kind === "err" ? (
            <ErrLine msg={cpuSeries.error ?? "failed"} />
          ) : (
            <MultiLine
              series={cpuSeries.data ?? []}
              width={420}
              height={170}
              format={(v) => (v / (1024 * 1024)).toFixed(0) + " MiB"}
            />
          )}
        </ChartCard>

        <ChartCard
          title="Submit-to-commit heatmap"
          subtitle="latency density · 1m buckets · 1h"
        >
          {heatmap.kind === "err" ? (
            <ErrLine msg={heatmap.error ?? "failed"} />
          ) : (
            <Heatmap
              rows={6}
              cols={60}
              cells={heatmap.data ?? []}
              rowLabels={["<5ms", "<25ms", "<100ms", "<500ms", "<2s", ">2s"]}
              width={420}
              height={170}
              color={CHART_PALETTE[2]}
            />
          )}
        </ChartCard>
      </div>

      {/* Latency headline triplet — mirrors `dpm localnet metrics`
          text output so CLI and UI agree on the curated quantiles. */}
      <LatencyStrip
        p50={summary.data?.latency?.p50_ms ?? undefined}
        p95={summary.data?.latency?.p95_ms ?? undefined}
        p99={summary.data?.latency?.p99_ms ?? undefined}
      />

      {/* Top error sources — full width */}
      <ChartCard title="Top error sources" subtitle="last hour">
        {topErrors.kind === "err" ? (
          <ErrLine msg={topErrors.error ?? "failed"} />
        ) : (
          <BarChart
            bars={topErrors.data ?? []}
            defaultColor={ERR_COLOR}
            width={860}
            format={(v) => v.toFixed(2)}
          />
        )}
      </ChartCard>

      {/* Dashboards — deep link to the bundled Grafana view. Same UID
          the CLI's text output prints, so both surfaces point at the
          same view (CLI ↔ UI parity, see CONTRIBUTING.md). */}
      <DashboardsBlock url={summary.data?.dashboards?.grafana_url} />
    </section>
  );
}

// LatencyStrip surfaces the same three quantiles `dpm localnet
// metrics` prints, making the SLA shape visible at a glance.
function LatencyStrip(props: {
  p50?: number;
  p95?: number;
  p99?: number;
}) {
  const fmt = (v?: number) => (v === undefined ? "—" : `${Math.round(v)} ms`);
  const cell: CSSProperties = {
    padding: "10px 14px",
    background: W.surface,
    border: `1px solid ${W.border}`,
    borderRadius: 2,
    fontFamily: wMono,
    fontSize: 13,
    color: W.text,
  };
  const label: CSSProperties = {
    color: W.dim,
    marginRight: 8,
  };
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(3, max-content)",
        gap: 12,
        marginBottom: 16,
      }}
    >
      <div style={cell}>
        <span style={label}>p50</span>
        {fmt(props.p50)}
      </div>
      <div style={cell}>
        <span style={label}>p95</span>
        {fmt(props.p95)}
      </div>
      <div style={cell}>
        <span style={label}>p99</span>
        {fmt(props.p99)}
      </div>
    </div>
  );
}

// DashboardsBlock surfaces the Grafana deep link from the summary
// handler. When the URL is empty we render the same hint as the CLI
// rather than hiding the section, so users learn the profile exists.
function DashboardsBlock(props: { url?: string }) {
  const wrap: CSSProperties = {
    marginTop: 16,
    padding: "10px 14px",
    background: W.surface,
    border: `1px solid ${W.border}`,
    borderRadius: 2,
    fontFamily: wMono,
    fontSize: 13,
    color: W.text,
  };
  if (!props.url) {
    return (
      <div style={wrap}>
        <strong style={{ marginRight: 8 }}>Dashboards:</strong>
        <span style={{ color: W.dim }}>
          enable observability profile to see Grafana
        </span>
      </div>
    );
  }
  return (
    <div style={wrap}>
      <strong style={{ marginRight: 8 }}>Dashboards:</strong>
      <a href={props.url} target="_blank" rel="noreferrer" style={{ color: W.brandText }}>
        Grafana
      </a>
    </div>
  );
}

function Header({ name }: { name: string }) {
  return (
    <header style={{ marginBottom: 16 }}>
      <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>
        Metrics —{" "}
        <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>
      </h2>
      <p style={{ color: W.dim, fontSize: 12.5, margin: "3px 0 0" }}>
        Live Canton + Splice metrics scraped from Prometheus. Auto-refresh 5 s.
      </p>
    </header>
  );
}

function ChartCard({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 14,
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <div style={{ marginBottom: 12 }}>
        <div style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}>
          {title}
        </div>
        {subtitle && (
          <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2 }}>
            {subtitle}
          </div>
        )}
      </div>
      {children}
    </div>
  );
}

function ErrLine({ msg }: { msg: string }) {
  return (
    <div role="alert" style={{ color: "#7BD2C6", fontSize: 12, padding: "20px 0" }}>
      {msg}
    </div>
  );
}

function ObservabilityOffPanel({
  name,
  onEnabled,
}: {
  name: string;
  onEnabled: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function enable() {
    setBusy(true);
    setErr(null);
    try {
      // Send BOTH: the Metrics screen needs Prometheus (for data)
      // AND Grafana (for the dashboards link).
      await setObservability(name, { prometheus: true, grafana: true });
      onEnabled();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "failed");
      setBusy(false);
    }
  }

  return (
    <div
      style={{
        background: `${tint(W.warn, 6)}`,
        border: `1px solid ${W.warn}`,
        borderRadius: 4,
        padding: 20,
      }}
    >
      <h3 style={{ color: W.warn, fontSize: 14, marginTop: 0, marginBottom: 8 }}>
        Observability profile not enabled
      </h3>
      <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>
        Instance{" "}
        <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>{" "}
        was started without the observability profile. Prometheus and Grafana
        aren't running, so there's nothing to scrape.
      </p>

      <div style={{ display: "flex", gap: 10, marginTop: 14, alignItems: "center" }}>
        <Button variant="primary" size="md" onClick={enable} disabled={busy}>
          {busy ? "Enabling…" : "Enable observability now"}
        </Button>
        <span style={{ color: W.dim, fontSize: 11.5 }}>
          Brings up Prometheus + Grafana on this instance without
          restarting Canton.
        </span>
      </div>

      {err && (
        <div role="alert" style={{ color: W.err, fontSize: 12, marginTop: 8 }}>
          <span
            style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
          >
            <IcX size={12} /> {err}
          </span>
        </div>
      )}

      <p style={{ color: W.dim, fontSize: 12, marginTop: 14 }}>
        Or from the CLI (same hot toggle, no restart):{" "}
        <code
          style={{
            fontFamily: wMono,
            color: W.text,
            background: W.border,
            padding: "1px 6px",
            borderRadius: 2,
          }}
        >
          {`dpm localnet observability enable --name ${name}`}
        </code>
      </p>
    </div>
  );
}

// ── Loaders ──────────────────────────────────────────────────────

// isAborted treats an AbortError thrown by fetch the same as the
// signal being already aborted at the moment we check it.
function isAborted(signal: AbortSignal, e: unknown): boolean {
  if (signal.aborted) return true;
  return e instanceof DOMException && e.name === "AbortError";
}

async function loadSeries(
  name: string,
  query: string,
  label: string,
  set: (s: CardState<Series>) => void,
  signal: AbortSignal,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h", undefined, signal);
    if (signal.aborted) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      () => label,
    );
    set({ kind: "ok", data: decoded[0] ?? { label, color: TPS_COLOR, points: [] } });
  } catch (e) {
    if (isAborted(signal, e)) return;
    set({
      kind: "err",
      error: e instanceof ApiError ? e.message : "failed",
    });
  }
}

async function loadMultiSeries(
  name: string,
  specs: Array<{ query: string; label: string; color: string }>,
  set: (s: CardState<Series[]>) => void,
  signal: AbortSignal,
) {
  try {
    const rs = await Promise.all(
      specs.map((s) => fetchMetricsRange(name, s.query, "1h", undefined, signal)),
    );
    if (signal.aborted) return;
    const out: Series[] = rs.map((r, i) => {
      const decoded = decodePrometheusRange(
        r as unknown as PrometheusRangeResponse,
        () => specs[i].label,
        () => specs[i].color,
      );
      return decoded[0] ?? { label: specs[i].label, color: specs[i].color, points: [] };
    });
    set({ kind: "ok", data: out });
  } catch (e) {
    if (isAborted(signal, e)) return;
    set({
      kind: "err",
      error: e instanceof ApiError ? e.message : "failed",
    });
  }
}

async function loadMultiSeriesGrouped(
  name: string,
  query: string,
  labelFn: (m: Record<string, string>) => string,
  set: (s: CardState<Series[]>) => void,
  signal: AbortSignal,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h", undefined, signal);
    if (signal.aborted) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      labelFn,
    );
    set({ kind: "ok", data: decoded });
  } catch (e) {
    if (isAborted(signal, e)) return;
    set({
      kind: "err",
      error: e instanceof ApiError ? e.message : "failed",
    });
  }
}

async function loadBars(
  name: string,
  query: string,
  labelFn: (m: Record<string, string>) => string,
  set: (s: CardState<Bar[]>) => void,
  signal: AbortSignal,
) {
  try {
    const r = await fetchMetricsRange(name, query, "5m", undefined, signal);
    if (signal.aborted) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      labelFn,
    );
    // For a "right now" bar chart we just want the latest value per series.
    const bars: Bar[] = decoded
      .map((s, i) => ({
        label: s.label,
        value: s.points[s.points.length - 1]?.v ?? 0,
        color: CHART_PALETTE[i % CHART_PALETTE.length],
      }))
      .filter((b) => b.value > 0)
      .sort((a, b) => b.value - a.value)
      .slice(0, 8);
    set({ kind: "ok", data: bars });
  } catch (e) {
    if (isAborted(signal, e)) return;
    set({
      kind: "err",
      error: e instanceof ApiError ? e.message : "failed",
    });
  }
}

async function loadHeatmap(
  name: string,
  query: string,
  set: (s: CardState<Cell[]>) => void,
  signal: AbortSignal,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h", "1m", signal);
    if (signal.aborted) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      (m) => m.le ?? "+Inf",
    );
    // Map le buckets to row indices (6 rows: <5ms, <25ms, <100ms,
    // <500ms, <2s, >2s). Skip series we don't have a row for.
    const rowFor = (le: string): number | null => {
      const n = Number(le);
      if (!Number.isFinite(n)) return 5; // +Inf
      if (n <= 0.005) return 0;
      if (n <= 0.025) return 1;
      if (n <= 0.1) return 2;
      if (n <= 0.5) return 3;
      if (n <= 2) return 4;
      return 5;
    };
    // Determine global max for normalisation.
    let max = 0;
    for (const s of decoded) {
      for (const p of s.points) {
        if (p.v > max) max = p.v;
      }
    }
    if (max === 0) max = 1;
    const cells: Cell[] = [];
    for (const s of decoded) {
      const r = rowFor(s.label);
      if (r === null) continue;
      s.points.forEach((p, c) => {
        cells.push({ r, c, i: p.v / max });
      });
    }
    set({ kind: "ok", data: cells });
  } catch (e) {
    if (isAborted(signal, e)) return;
    set({
      kind: "err",
      error: e instanceof ApiError ? e.message : "failed",
    });
  }
}

// deltaFromSeries: latest minus the value 5 minutes back.
function deltaFromSeries(s: Series | undefined, scale = 1): number | undefined {
  if (!s || s.points.length < 2) return undefined;
  const last = s.points[s.points.length - 1].v * scale;
  // 5 minutes back in points: assume step is consistent; find nearest.
  const targetT = s.points[s.points.length - 1].t - 5 * 60 * 1000;
  let nearest = s.points[0];
  let nd = Math.abs(s.points[0].t - targetT);
  for (const p of s.points) {
    const d = Math.abs(p.t - targetT);
    if (d < nd) {
      nearest = p;
      nd = d;
    }
  }
  return last - nearest.v * scale;
}
