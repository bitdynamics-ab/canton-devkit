import { useEffect, useState } from "react";
import {
  ApiError,
  fetchMetricsRange,
  fetchMetricsSummary,
  type MetricsSummary,
  type PrometheusRangeResponse,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";
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

// MetricsScreen — BIT-188 production layout.
//
// Matches docs/design/mockups/webui-metrics-agent.jsx:
//   - 4-up MetricCard strip (Throughput, Command completion p99,
//     Active contracts, Errors) with delta vs prior window +
//     inline sparkline
//   - Six chart cards in a 2-col grid:
//       Latency by phase · Per-template throughput
//       Active contracts trend · Ledger errors
//       Resource usage · Submit-to-commit heatmap
//   - "Top error sources" full-width bar chart at the bottom
//
// Every card hits a real PromQL query via /api/instances/:name/
// metrics/range. Loading / empty / error states are first-class.
// Auto-refresh every 5 s (cheap — the cards run in parallel).
//
// When the observability profile isn't enabled the screen renders
// the same friendly empty-state panel as before.

interface CardState<T> {
  kind: "loading" | "ok" | "err";
  data?: T;
  error?: string;
}

// PromQL queries. Sourced from internal/metricsq for parity with the
// CLI's `localnet metrics` headline. Per-template / phase / heatmap
// queries are extensions specific to this screen.
const Q = {
  throughputSeries: "sum(rate(canton_participant_transactions_total[1m]))",
  p99: 'histogram_quantile(0.99, sum(rate(canton_mediator_approval_duration_bucket[5m])) by (le))',
  acsCount: "sum(canton_participant_active_contracts)",
  errorsRate: "sum(rate(canton_participant_command_rejections_total[1m]))",
  latencyMedian:
    'histogram_quantile(0.50, sum(rate(canton_mediator_approval_duration_bucket[5m])) by (le))',
  latencyP99:
    'histogram_quantile(0.99, sum(rate(canton_mediator_approval_duration_bucket[5m])) by (le))',
  perTemplate:
    "sum by (template) (rate(canton_participant_exercises_total[5m]))",
  errors1m: "sum(rate(canton_participant_command_rejections_total[1m]))",
  cpu: "sum by (container) (rate(container_cpu_usage_seconds_total[1m]))",
};

const TPS_COLOR = "#7CB5F7";
const P99_COLOR = "#F5BF55";
const ACS_COLOR = "#5BD7C5";
const ERR_COLOR = "#F08FB5";

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
    let cancelled = false;
    const tick = async () => {
      if (cancelled) return;
      try {
        const s = await fetchMetricsSummary(name);
        if (cancelled) return;
        setSummary({ kind: "ok", data: s });
        setObservabilityOff(null);
      } catch (e) {
        if (cancelled) return;
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
        loadSeries(name, Q.throughputSeries, "tx/s", setThroughputSeries, cancelled),
        loadSeries(name, Q.p99, "p99", setP99Series, cancelled),
        loadSeries(name, Q.acsCount, "ACS", setAcsSeries, cancelled),
        loadSeries(name, Q.errorsRate, "errors", setErrorsSeries, cancelled),
        loadMultiSeries(
          name,
          [
            { query: Q.latencyMedian, label: "median", color: CHART_PALETTE[1] },
            { query: Q.latencyP99, label: "p99", color: CHART_PALETTE[3] },
          ],
          setLatencyPhase,
          cancelled,
        ),
        loadBars(
          name,
          Q.perTemplate,
          (m) => m.template ?? "unknown",
          setPerTemplate,
          cancelled,
        ),
        loadBars(
          name,
          Q.errors1m + " by (reason)",
          (m) => m.reason ?? "(unknown)",
          setTopErrors,
          cancelled,
        ),
        loadMultiSeriesGrouped(
          name,
          Q.cpu,
          (m) => m.container ?? "container",
          setCpuSeries,
          cancelled,
        ),
        loadHeatmap(
          name,
          'sum(increase(canton_mediator_approval_duration_bucket[1m])) by (le)',
          setHeatmap,
          cancelled,
        ),
      ]);
    };
    tick();
    const t = setInterval(tick, 5000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [name]);

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
        <ObservabilityOffPanel name={name} remediation={observabilityOff} />
      </section>
    );
  }

  const m = summary.data?.metrics;

  return (
    <section style={{ padding: 24 }}>
      <Header name={name} />

      {/* 4-up top strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, 1fr)",
          gap: 12,
          marginBottom: 14,
        }}
      >
        <MetricCard
          title="Throughput"
          unit="tx/s"
          value={m?.ledger_tps_5m}
          sparkline={throughputSeries.data?.points}
          sparklineColor={TPS_COLOR}
          error={throughputSeries.kind === "err" ? throughputSeries.error : undefined}
          delta={deltaFromSeries(throughputSeries.data)}
          deltaPolarity="up-is-good"
        />
        <MetricCard
          title="Command completion p99"
          unit="ms"
          value={m?.mediator_p95_seconds !== undefined ? m.mediator_p95_seconds * 1000 : undefined}
          sparkline={p99Series.data?.points.map((p) => ({ t: p.t, v: p.v * 1000 }))}
          sparklineColor={P99_COLOR}
          error={p99Series.kind === "err" ? p99Series.error : undefined}
          delta={deltaFromSeries(p99Series.data, 1000)}
          deltaPolarity="down-is-good"
          format={(v) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1))}
        />
        <MetricCard
          title="Active contracts"
          value={acsSeries.data?.points[acsSeries.data.points.length - 1]?.v}
          sparkline={acsSeries.data?.points}
          sparklineColor={ACS_COLOR}
          error={acsSeries.kind === "err" ? acsSeries.error : undefined}
          delta={deltaFromSeries(acsSeries.data)}
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
          delta={deltaFromSeries(errorsSeries.data)}
          deltaPolarity="down-is-good"
        />
      </div>

      {/* 2-col chart grid */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: 12,
          marginBottom: 14,
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
          title="Per-template throughput"
          subtitle="exercises / 5m"
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

        <ChartCard title="Active contracts" subtitle="trend · 1h">
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

        <ChartCard title="Resource usage" subtitle="CPU seconds / sec — containers">
          {cpuSeries.kind === "err" ? (
            <ErrLine msg={cpuSeries.error ?? "failed"} />
          ) : (
            <MultiLine
              series={cpuSeries.data ?? []}
              width={420}
              height={170}
              format={(v) => v.toFixed(2)}
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
    </section>
  );
}

function Header({ name }: { name: string }) {
  return (
    <header style={{ marginBottom: 14 }}>
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
        borderRadius: 10,
        padding: 14,
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <div style={{ marginBottom: 8 }}>
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
    <div role="alert" style={{ color: "#F08FB5", fontSize: 12, padding: "20px 0" }}>
      {msg}
    </div>
  );
}

function ObservabilityOffPanel({
  name,
  remediation,
}: {
  name: string;
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
      <h3 style={{ color: W.warn, fontSize: 14, marginTop: 0, marginBottom: 8 }}>
        Observability profile not enabled
      </h3>
      <p style={{ color: W.text2, fontSize: 13, lineHeight: 1.5 }}>
        Instance{" "}
        <code style={{ fontFamily: wMono, color: W.brand }}>{name}</code>{" "}
        was started without the observability profile. Prometheus and Grafana
        aren't running, so there's nothing to scrape.
      </p>
      <p style={{ color: W.dim, fontSize: 12, marginTop: 12 }}>
        Restart with{" "}
        <code
          style={{
            fontFamily: wMono,
            color: W.text,
            background: W.border,
            padding: "1px 6px",
            borderRadius: 4,
          }}
        >
          {remediation
            .replace(/^.*`/, "")
            .replace(/`.*$/, "")
            .trim() || `dpm localnet up --profile observability --name ${name}`}
        </code>{" "}
        to enable.
      </p>
    </div>
  );
}

// ── Loaders ──────────────────────────────────────────────────────

async function loadSeries(
  name: string,
  query: string,
  label: string,
  set: (s: CardState<Series>) => void,
  cancelled: boolean,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h");
    if (cancelled) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      () => label,
    );
    set({ kind: "ok", data: decoded[0] ?? { label, color: TPS_COLOR, points: [] } });
  } catch (e) {
    if (cancelled) return;
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
  cancelled: boolean,
) {
  try {
    const rs = await Promise.all(
      specs.map((s) => fetchMetricsRange(name, s.query, "1h")),
    );
    if (cancelled) return;
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
    if (cancelled) return;
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
  cancelled: boolean,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h");
    if (cancelled) return;
    const decoded = decodePrometheusRange(
      r as unknown as PrometheusRangeResponse,
      labelFn,
    );
    set({ kind: "ok", data: decoded });
  } catch (e) {
    if (cancelled) return;
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
  cancelled: boolean,
) {
  try {
    const r = await fetchMetricsRange(name, query, "5m");
    if (cancelled) return;
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
    if (cancelled) return;
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
  cancelled: boolean,
) {
  try {
    const r = await fetchMetricsRange(name, query, "1h", "1m");
    if (cancelled) return;
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
    if (cancelled) return;
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
