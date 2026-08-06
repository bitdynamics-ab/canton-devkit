import { useEffect, useMemo, useState, type CSSProperties } from "react";
import {
  ApiError,
  type AppConfigFormat,
  type ContainerHealth,
  type ContainersResponse,
  type Endpoint,
  type Instance,
  type JwtResponse,
  type TransactionEvent,
  type TransactionRow,
  downInstance,
  fetchAppConfigJSON,
  fetchAppConfigText,
  fetchContainers,
  fetchContracts,
  fetchInstance,
  fetchMetricsRange,
  fetchMetricsSummary,
  fetchTransactions,
  issueJwt,
  restartContainer,
  pauseInstance,
  recreateInstance,
  removeInstance,
  setObservability,
  startInstance,
  stopInstance,
  unpauseInstance,
} from "../api";
import { W, wMono, wideCaps, tableCaps, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import {
  Dot,
  IcAlert,
  IcCopy,
  IcDownload,
  IcEject,
  IcPause,
  IcPlay,
  IcRefresh,
  IcStop,
  IcX,
} from "../components/icons";
import { StatusBadge } from "../components/StatusBadge";
import { MonoId } from "../components/MonoId";
import { confirmDialog } from "../components/ConfirmDialog";
import { AreaChart } from "../components/charts/AreaChart";
import { decodePrometheusRange, type Series } from "../components/charts/types";
import { Q, scopeQ, deltaFromSeries } from "./MetricsScreen";
import { ContainerLogsModal } from "./ContainerLogsModal";
import { BackupRestore } from "./BackupRestore";

// The per-instance Overview. One tabbed surface a developer can wire an
// app against without leaving: throughput (only when monitoring is on),
// the endpoints + containers pair, and the JWT + app-config generators.
// Activity and Snapshots ride along as sibling tabs.

type Tab = "overview" | "activity" | "snapshots";
type Monitoring = "unknown" | "on" | "off";

interface Props {
  name: string;
  // From the dashboard's fresh list; gates which action button renders
  // and supplies the header port range (Instance has no ports field).
  statusHint?: string;
  ports?: string;
  onChanged?: () => void;
}

const THROUGHPUT_COLOR = W.brand;

export function InstanceOverview({ name, statusHint, ports, onChanged }: Props) {
  const [tab, setTab] = useState<Tab>("overview");
  const [instState, setInstState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; instance: Instance }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [refetchTick, setRefetchTick] = useState(0);
  const [busy, setBusy] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);
  // A snapshot pauses the node containers for a consistent capture, which
  // the reconciler would otherwise surface as "partial". BackupRestore flips
  // this while a capture is in flight so the header reads "Snapshotting…".
  const [snapshotting, setSnapshotting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (refetchTick === 0) setInstState({ kind: "loading" });
    fetchInstance(name)
      .then((r) => {
        if (!cancelled) setInstState({ kind: "ok", instance: r });
      })
      .catch((e) => {
        if (cancelled) return;
        setInstState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load instance",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, refetchTick]);

  const instance = instState.kind === "ok" ? instState.instance : null;
  const status = statusHint ?? instance?.status ?? "";
  const running = (instance?.status ?? statusHint) === "running";

  // Shared container poll — the header pill, the metrics-off strip, and
  // the Containers panel all read one poll instead of three.
  const [containers, setContainers] = useState<ContainersResponse | null>(null);
  const [containersAbsent, setContainersAbsent] = useState(false);
  const [containersAt, setContainersAt] = useState<number | null>(null);
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      try {
        const r = await fetchContainers(name);
        if (!cancelled) {
          setContainers(r);
          setContainersAbsent(false);
          setContainersAt(Date.now());
        }
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 503) {
          setContainersAbsent(true);
          setContainers(null);
        }
      } finally {
        if (!cancelled) timer = setTimeout(poll, 3000);
      }
    }
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [name]);

  // Monitoring detection + throughput series. Same signal the Metrics
  // screen uses: fetchMetricsSummary throws OBSERVABILITY_PROFILE_OFF
  // when the observability profile isn't running. Only meaningful while
  // the instance is up.
  const [monitoring, setMonitoring] = useState<Monitoring>("unknown");
  const [tps, setTps] = useState<{
    value?: number;
    series?: Series;
    delta?: number;
    latencyMs?: number;
    err?: string;
  }>({});

  useEffect(() => {
    if (!running) {
      setMonitoring("unknown");
      setTps({});
      return;
    }
    let outer: AbortController | null = null;
    let timer: ReturnType<typeof setInterval> | null = null;
    const tick = async () => {
      // Abort the prior tick — a slow query from t=0 must not resolve
      // after t=5s and clobber a newer instance's data.
      outer?.abort();
      outer = new AbortController();
      const signal = outer.signal;
      let scope = "";
      try {
        const s = await fetchMetricsSummary(name, signal);
        if (signal.aborted) return;
        scope = s.scope ?? "";
        const tpsValue = s.metrics?.ledger_tps_5m;
        setMonitoring("on");
        setTps((prev) => ({ ...prev, value: tpsValue }));
      } catch (e) {
        if (signal.aborted) return;
        if (e instanceof ApiError && e.code === "OBSERVABILITY_PROFILE_OFF") {
          setMonitoring("off");
          return;
        }
        setMonitoring("on");
        setTps((prev) => ({
          ...prev,
          err: e instanceof ApiError ? e.message : "failed",
        }));
        return;
      }
      try {
        const r = await fetchMetricsRange(
          name,
          scopeQ(Q.throughputSeries, scope),
          "1h",
          undefined,
          signal,
        );
        if (signal.aborted) return;
        const decoded = decodePrometheusRange(r, () => "tx/s");
        const series =
          decoded[0] ?? { label: "tx/s", color: THROUGHPUT_COLOR, points: [] };
        setTps((prev) => ({
          value: prev.value,
          series,
          delta: deltaFromSeries(series),
          latencyMs: prev.latencyMs,
        }));
        // Avg mediator latency — real even where p99 is nil on Splice 0.6.x.
        const lat = await fetchMetricsRange(
          name,
          scopeQ(Q.avgLatency, scope),
          "1h",
          undefined,
          signal,
        );
        if (signal.aborted) return;
        const latVal = decodePrometheusRange(lat, () => "ms")[0]?.points.at(-1)?.v;
        setTps((prev) => ({ ...prev, latencyMs: latVal }));
      } catch (e) {
        if (signal.aborted || (e instanceof DOMException && e.name === "AbortError"))
          return;
        setTps((prev) => ({
          ...prev,
          err: e instanceof ApiError ? e.message : "failed",
        }));
      }
    };
    tick();
    timer = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      tick();
    }, 5000);
    return () => {
      outer?.abort();
      if (timer) clearInterval(timer);
    };
  }, [name, running]);

  // Active-contract count for the KPI rail. There is no honest ACS-cardinality
  // metric in Prometheus, so we count the ledger's ACS snapshot directly — a
  // real read that doesn't need the observability profile. `sv` is the broadest
  // single-party view; the count is capped at ACS_LIMIT (rendered "N+"). A
  // dedicated count endpoint would avoid shipping payloads just to count.
  // TODO(BIT-parity): if a backend ACS-count endpoint lands, mirror it to the CLI.
  const [acs, setAcs] = useState<{ count: number; capped: boolean } | undefined>(
    undefined,
  );
  useEffect(() => {
    if (!running) {
      setAcs(undefined);
      return;
    }
    const ACS_LIMIT = 1000;
    let cancelled = false;
    const tick = async () => {
      try {
        const r = await fetchContracts(name, "sv", ACS_LIMIT);
        if (!cancelled) {
          setAcs({ count: r.contracts.length, capped: r.contracts.length >= ACS_LIMIT });
        }
      } catch {
        // No sv visibility / ledger unreachable → em-dash, never a fake 0.
        if (!cancelled) setAcs(undefined);
      }
    };
    tick();
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      tick();
    }, 30_000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [name, running]);

  async function act(
    fn: () => Promise<unknown>,
    opts: { confirm?: Parameters<typeof confirmDialog>[0]; refetch?: boolean } = {},
  ) {
    if (opts.confirm && !(await confirmDialog(opts.confirm))) return;
    setBusy(true);
    setActionErr(null);
    try {
      await fn();
      if (opts.refetch !== false) setRefetchTick((n) => n + 1);
      onChanged?.();
    } catch (e) {
      setActionErr(e instanceof ApiError ? e.message : "action failed");
      if (opts.refetch !== false) setRefetchTick((n) => n + 1);
      onChanged?.();
    } finally {
      setBusy(false);
    }
  }

  const unreachable =
    instance?.endpoints?.filter((e) => e.reachability === "unreachable") ?? [];

  return (
    <section style={{ marginTop: 20, display: "flex", flexDirection: "column", gap: 16 }}>
      <Header
        name={name}
        status={status}
        snapshotting={snapshotting}
        ports={ports}
        instance={instance}
        busy={busy}
        onStop={() => act(() => stopInstance(name))}
        onPause={() => act(() => pauseInstance(name))}
        onResume={() => act(() => unpauseInstance(name))}
        onStart={() => act(() => startInstance(name))}
        onRecreate={() =>
          act(() => recreateInstance(name), {
            confirm: {
              title: "Recreate instance?",
              body: `Brings ${name} down then back up. The recorded Splice version and profiles are preserved. Data volumes are not touched.`,
              detail: `dpm localnet down ${name} && dpm localnet up ${name}`,
              confirmLabel: "Recreate",
            },
          })
        }
        onDown={() =>
          act(() => downInstance(name), {
            confirm: {
              title: "Tear down instance?",
              body: `Removes ${name}'s containers and networks. Data volumes are preserved, so Start recreates it.`,
              detail: `dpm localnet down ${name}`,
              confirmLabel: "Down",
              danger: true,
            },
          })
        }
        onRemove={() =>
          act(() => removeInstance(name), {
            confirm: {
              title: "Permanently remove instance?",
              body: `Deletes ${name}'s containers, Docker volumes, ledger data, and registry state. This cannot be undone.`,
              detail: `dpm localnet remove --name ${name}`,
              confirmLabel: "Remove permanently",
              danger: true,
            },
            refetch: false,
          })
        }
      />

      <TabBar tab={tab} onChange={setTab} />

      {running && (
        <VitalsRail
          ports={ports}
          createdAt={instance?.created_at}
          uptime={instance?.uptime}
          containers={containers}
          endpoints={instance?.endpoints ?? []}
          updatedAt={containersAt}
          snapshotting={snapshotting}
        />
      )}

      {actionErr && (
        <div
          role="alert"
          style={{
            background: tint(W.err, 6),
            color: W.err,
            border: `1px solid ${W.err}`,
            borderRadius: R.control,
            padding: "6px 10px",
            fontSize: fs.meta,
          }}
        >
          Action failed: {actionErr}
        </div>
      )}

      {instState.kind === "err" && (
        <div role="alert" style={{ color: W.err, fontSize: fs.data }}>
          {instState.error}
        </div>
      )}

      {tab === "overview" && (
        <>
          {running && monitoring === "on" && (
            <div style={{ display: "grid", gridTemplateColumns: "1fr 320px", gap: 14, alignItems: "start" }}>
              <ThroughputPanel name={name} tps={tps} />
              <KpiRail latencyMs={tps.latencyMs} acs={acs} containers={containers} />
            </div>
          )}
          {running && monitoring === "off" && (
            <MonitoringOffStrip
              containers={containers}
              uptime={instance?.uptime}
              busy={busy}
              onEnable={() =>
                act(
                  async () => {
                    await setObservability(name, {
                      prometheus: true,
                      grafana: true,
                    });
                    setMonitoring("unknown");
                  },
                  { refetch: false },
                )
              }
            />
          )}

          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: 14,
              alignItems: "start",
            }}
          >
            <EndpointsPanel
              name={name}
              endpoints={instance?.endpoints ?? []}
              unreachable={unreachable}
            />
            <ContainersPanel
              name={name}
              data={containers}
              absent={containersAbsent}
            />
          </div>

          {running && (
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1.25fr)",
                gap: 14,
                alignItems: "start",
              }}
            >
              <JwtPanel name={name} />
              <AppConfigPanel name={name} />
            </div>
          )}
        </>
      )}

      {tab === "activity" &&
        (running ? (
          <RecentActivity name={name} />
        ) : (
          <Panel>
            <div style={{ color: W.dim, fontSize: fs.body, padding: "4px 0" }}>
              Ledger activity is available once the instance is running.
            </div>
          </Panel>
        ))}

      {tab === "snapshots" && (
        <BackupRestore instanceName={name} onSnapshotting={setSnapshotting} />
      )}
    </section>
  );
}

// ── Header + action bar ─────────────────────────────────────

function Header({
  name,
  status,
  snapshotting,
  ports,
  instance,
  busy,
  onStop,
  onPause,
  onResume,
  onStart,
  onRecreate,
  onDown,
  onRemove,
}: {
  name: string;
  status: string;
  snapshotting?: boolean;
  ports?: string;
  instance: Instance | null;
  busy: boolean;
  onStop: () => void;
  onPause: () => void;
  onResume: () => void;
  onStart: () => void;
  onRecreate: () => void;
  onDown: () => void;
  onRemove: () => void;
}) {
  // While running, the vitals rail below carries ports / uptime / created,
  // so the subtitle stays as just the Splice version. When stopped (rail
  // hidden), keep ports + created here so they're not lost.
  const running = status === "running";
  const meta: string[] = [];
  if (instance?.splice_version) meta.push(`Splice ${instance.splice_version}`);
  if (!running) {
    if (ports) meta.push(`ports ${ports}`);
    if (instance?.created_at) meta.push(`created ${instance.created_at}`);
  }

  return (
    <div
      style={{
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "space-between",
        gap: 24,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <h1
            style={{
              margin: 0,
              fontFamily: wMono,
              fontSize: fs.title,
              fontWeight: 600,
              color: W.text,
            }}
          >
            {name}
          </h1>
          {snapshotting ? (
            <StatusBadge status="snapshotting" variant="pill" pulse />
          ) : (
            status && <StatusBadge status={status} variant="pill" />
          )}
          {instance?.live_probe_failed && (
            <span
              style={{
                color: W.warn,
                fontSize: fs.label,
                border: `1px solid ${tint(W.warn, 34)}`,
                background: tint(W.warn, 13),
                borderRadius: R.control,
                padding: "2px 8px",
              }}
            >
              Live probe failed
            </span>
          )}
        </div>
        {meta.length > 0 && (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              fontFamily: wMono,
              fontSize: fs.meta,
              color: W.dim,
            }}
          >
            {meta.map((m, i) => (
              <span key={m} style={{ display: "flex", alignItems: "center", gap: 10 }}>
                {i > 0 && <span style={{ color: W.borderHi }}>·</span>}
                <span>{m}</span>
              </span>
            ))}
          </div>
        )}
      </div>
      <ActionBar
        status={status}
        busy={busy}
        onStop={onStop}
        onPause={onPause}
        onResume={onResume}
        onStart={onStart}
        onRecreate={onRecreate}
        onDown={onDown}
        onRemove={onRemove}
      />
    </div>
  );
}

// Dispatches verbs per status; failed/partial containers MAY still be up
// (compose down no-ops cleanly if not), so Down is offered there too.
function ActionBar({
  status,
  busy,
  onStop,
  onPause,
  onResume,
  onStart,
  onRecreate,
  onDown,
  onRemove,
}: {
  status: string;
  busy: boolean;
  onStop: () => void;
  onPause: () => void;
  onResume: () => void;
  onStart: () => void;
  onRecreate: () => void;
  onDown: () => void;
  onRemove: () => void;
}) {
  if (status === "running" || status === "paused") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        {status === "running" ? (
          <Button
            variant="secondary"
            icon={<IcPause />}
            onClick={onPause}
            disabled={busy}
            title="Freeze containers (docker compose pause) — hold state + ports, free CPU. Resume is instant."
          >
            {busy ? "…" : "Pause"}
          </Button>
        ) : (
          <Button
            variant="primary"
            icon={<IcPlay />}
            onClick={onResume}
            disabled={busy}
            title="Resume frozen containers (docker compose unpause) — no boot cost."
          >
            {busy ? "…" : "Resume"}
          </Button>
        )}
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={onRecreate}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
        >
          {busy ? "…" : "Recreate"}
        </Button>
        <Button
          variant="secondary"
          icon={<IcStop />}
          onClick={onStop}
          disabled={busy}
          title="Gracefully stop (docker compose stop) — processes exit and free CPU/memory, but containers are kept for a fast Start."
        >
          {busy ? "Stopping…" : "Stop"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Tear down (docker compose down) — remove containers and networks. Data volumes preserved; Start will recreate them."
        >
          {busy ? "…" : "Down"}
        </Button>
      </div>
    );
  }
  if (status === "failed" || status === "partial") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={onRecreate}
          disabled={busy}
          title="Bring containers down then back up. Splice version, profiles, credentials, and ports preserved."
        >
          {busy ? "…" : "Recreate"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Force docker compose down — use if containers are still running. Data volumes preserved."
        >
          {busy ? "…" : "Down containers"}
        </Button>
        <Button
          variant="ghost"
          icon={<IcX />}
          onClick={onRemove}
          disabled={busy}
          title="Permanently remove containers, Docker volumes, ledger data, and registry state."
        >
          {busy ? "Removing…" : "Remove instance"}
        </Button>
      </div>
    );
  }
  if (status === "stopped") {
    return (
      <div style={{ display: "flex", gap: 6 }}>
        <Button
          variant="primary"
          icon={<IcPlay />}
          onClick={onStart}
          disabled={busy}
          title="Start the instance. Fast when containers are present; otherwise recreates them with the recorded Splice version and ports."
        >
          {busy ? "Starting…" : "Start"}
        </Button>
        <Button
          variant="danger"
          icon={<IcEject />}
          onClick={onDown}
          disabled={busy}
          title="Tear down stopped containers (docker compose down). Data volumes preserved; Start will recreate them."
        >
          {busy ? "…" : "Down"}
        </Button>
        <Button
          variant="ghost"
          icon={<IcX />}
          onClick={onRemove}
          disabled={busy}
          title="Permanently remove containers, Docker volumes, ledger data, and registry state."
        >
          {busy ? "Removing…" : "Remove instance"}
        </Button>
      </div>
    );
  }
  return null;
}

function TabBar({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  const tabs: Array<[Tab, string]> = [
    ["overview", "Overview"],
    ["activity", "Activity"],
    ["snapshots", "Snapshots"],
  ];
  return (
    <div style={{ display: "flex", gap: 2, borderBottom: `1px solid ${W.border}` }}>
      {tabs.map(([key, label]) => {
        const active = key === tab;
        return (
          <button
            key={key}
            type="button"
            onClick={() => onChange(key)}
            style={{
              appearance: "none",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: "7px 12px",
              fontSize: fs.data,
              fontFamily: "inherit",
              fontWeight: active ? 600 : 400,
              color: active ? W.text : W.dim,
              borderBottom: `2px solid ${active ? W.brand : "transparent"}`,
              marginBottom: -1,
            }}
          >
            {label}
          </button>
        );
      })}
    </div>
  );
}

// ── Throughput (monitoring ON) ──────────────────────────────

function ThroughputPanel({
  name,
  tps,
}: {
  name: string;
  tps: { value?: number; series?: Series; delta?: number; err?: string };
}) {
  // The KPI rail (Latency avg / Active contracts / CPU / Memory) renders
  // beside this panel — see KpiRail. p99 is NaN on Splice 0.6.x (+Inf-only
  // histogram) so latency shows the real mediator avg instead.
  const delta = tps.delta;
  const deltaColor = delta === undefined ? W.dim : delta > 0 ? W.ok : delta < 0 ? W.dim : W.dim;
  const deltaText =
    delta === undefined || Math.abs(delta) < 0.05
      ? null
      : `${delta > 0 ? "+" : "−"}${Math.abs(delta).toFixed(1)}`;
  const value = tps.value;
  return (
    <div style={panelStyle}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          gap: 16,
          padding: "14px 14px 0",
        }}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <span style={{ ...wideCaps, fontSize: fs.label, color: W.dim } as CSSProperties}>
            Throughput
          </span>
          <span style={{ fontSize: fs.label, color: W.faint, fontFamily: wMono }}>
            last 5 min · app-provider participant
          </span>
        </div>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span
            style={{
              fontFamily: wMono,
              fontSize: fs.stat,
              fontWeight: 500,
              color: W.text,
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {typeof value === "number" && Number.isFinite(value) ? value.toFixed(1) : "—"}
          </span>
          <span style={{ fontSize: fs.meta, color: W.dim }}>tx/s</span>
          {deltaText && (
            <span
              style={{
                fontFamily: wMono,
                fontSize: fs.label,
                fontWeight: 600,
                color: deltaColor,
              }}
            >
              {deltaText}
            </span>
          )}
        </div>
      </div>
      <div style={{ padding: "6px 8px 4px" }}>
        {tps.err ? (
          <div style={{ color: W.dim, fontSize: fs.meta, padding: "24px 8px" }}>
            Throughput query failed. Retrying every 5 s.
          </div>
        ) : (
          <AreaChart
            series={tps.series ?? { label: "tx/s", color: THROUGHPUT_COLOR, points: [] }}
            width={860}
            height={182}
            yLabel="tx/s"
          />
        )}
      </div>
      <PanelFooter left={`dpm localnet metrics ${name}`} />
    </div>
  );
}

// ── Monitoring OFF strip ────────────────────────────────────

// gib formats a byte count as gibibytes with one decimal.
function gib(bytes: number): string {
  return (bytes / (1 << 30)).toFixed(1);
}

// KpiRail is the read-out card beside the throughput chart. Every tile has a
// real source: avg mediator latency (Prometheus; p99 is nil on 0.6.x so we
// show avg), active contracts (ledger ACS snapshot, sv view, capped → "N+"),
// and CPU/Memory (docker stats, summed across the project). Each value falls
// back to an em-dash (never a fabricated 0) when unsampled.
function KpiRail({
  latencyMs,
  acs,
  containers,
}: {
  latencyMs?: number;
  acs?: { count: number; capped: boolean };
  containers: ContainersResponse | null;
}) {
  const cpu = containers?.cpu_percent;
  const memUsed = containers?.mem_used_bytes;
  const memLimit = containers?.mem_limit_bytes;
  const n = containers?.containers.length ?? 0;
  const rows: Array<{ label: string; sub: string; value: string; unit: string; tip?: string }> = [
    {
      label: "Latency avg",
      sub: "mediator confirmation",
      value: latencyMs != null ? latencyMs.toFixed(0) : "—",
      unit: latencyMs != null ? "ms" : "",
      tip: latencyMs == null ? "no latency samples yet" : undefined,
    },
    {
      label: "Active contracts",
      sub: "ACS · sv view",
      value: acs ? (acs.capped ? "1000" : String(acs.count)) : "—",
      unit: acs?.capped ? "+" : "",
      tip: acs == null ? "ledger ACS snapshot unavailable" : acs.capped ? "capped at 1000 — more exist" : undefined,
    },
    {
      label: "CPU",
      sub: `${n} container${n === 1 ? "" : "s"}`,
      value: cpu != null ? cpu.toFixed(0) : "—",
      unit: cpu != null ? "%" : "",
      tip: cpu == null ? "docker stats unavailable" : undefined,
    },
    {
      label: "Memory",
      sub: memLimit ? "of granted" : "in use",
      value: memUsed != null ? gib(memUsed) : "—",
      unit: memUsed != null ? (memLimit ? `/ ${gib(memLimit)} GB` : "GB") : "",
      tip: memUsed == null ? "docker stats unavailable" : undefined,
    },
  ];
  return (
    <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: R.card, overflow: "hidden", display: "flex", flexDirection: "column" }}>
      {rows.map((r, i) => (
        <div
          key={r.label}
          title={r.tip}
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 10,
            padding: "13px 14px",
            borderBottom: i < rows.length - 1 ? `1px solid ${W.border}` : "none",
          }}
        >
          <span style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <span style={{ fontSize: fs.data, color: W.text }}>{r.label}</span>
            <span style={{ fontSize: fs.label, color: W.dim }}>{r.sub}</span>
          </span>
          <span style={{ display: "inline-flex", alignItems: "baseline", gap: 4 }}>
            <span style={{ fontFamily: wMono, fontSize: fs.lead, color: W.text, fontVariantNumeric: "tabular-nums" }}>{r.value}</span>
            {r.unit && <span style={{ fontSize: fs.label, color: W.dim }}>{r.unit}</span>}
          </span>
        </div>
      ))}
      <div style={{ marginTop: "auto", padding: "9px 14px", background: W.sunken, borderTop: `1px solid ${W.border}`, fontFamily: wMono, fontSize: fs.label, color: W.faint }}>
        docker stats · ledger ACS · metrics
      </div>
    </div>
  );
}

function MonitoringOffStrip({
  containers,
  uptime,
  busy,
  onEnable,
}: {
  containers: ContainersResponse | null;
  uptime?: string;
  busy: boolean;
  onEnable: () => void;
}) {
  // Docker-answerable facts ride here even with monitoring off: live
  // CPU/Memory (from `docker stats`), container count + healthy, and uptime.
  // Only the ledger metrics (throughput, latency) need Prometheus.
  const total = containers?.containers.length ?? 0;
  const healthy = containers?.healthy_count ?? 0;
  const cpu = containers?.cpu_percent;
  const memUsed = containers?.mem_used_bytes;
  const memLimit = containers?.mem_limit_bytes;
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 16,
        padding: "10px 14px",
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
      }}
    >
      <Stat label="cpu" value={cpu != null ? `${cpu.toFixed(0)}%` : "—"} />
      <Divider />
      <Stat
        label="memory"
        value={memUsed != null ? (memLimit ? `${gib(memUsed)} / ${gib(memLimit)} GB` : `${gib(memUsed)} GB`) : "—"}
      />
      <Divider />
      <Stat label="containers" value={String(total)} suffix={`${healthy} healthy`} />
      <Divider />
      <Stat label="uptime" value={uptime ?? "—"} />
      <span
        style={{
          marginLeft: "auto",
          display: "inline-flex",
          alignItems: "center",
          gap: 10,
        }}
      >
        <span style={{ fontSize: fs.meta, color: W.faint }}>metrics not collected</span>
        <button
          type="button"
          onClick={onEnable}
          disabled={busy}
          style={{
            appearance: "none",
            background: "transparent",
            border: "none",
            cursor: busy ? "default" : "pointer",
            padding: 0,
            fontFamily: "inherit",
            fontSize: fs.meta,
            fontWeight: 500,
            color: W.brandText,
          }}
        >
          {busy ? "Enabling…" : "Enable monitoring"}
        </button>
      </span>
    </div>
  );
}

function Stat({
  label,
  value,
  suffix,
}: {
  label: string;
  value: string;
  suffix?: string;
}) {
  return (
    <span style={{ display: "flex", alignItems: "baseline", gap: 7 }}>
      <span style={{ ...wideCaps, fontSize: fs.micro, color: W.dim } as CSSProperties}>
        {label}
      </span>
      <span
        style={{
          fontFamily: wMono,
          fontSize: fs.body,
          color: W.text,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {value}
      </span>
      {suffix && <span style={{ fontSize: fs.label, color: W.faint }}>{suffix}</span>}
    </span>
  );
}

function Divider() {
  return <span style={{ width: 1, height: 18, background: W.border }} />;
}

// ── Instance vitals rail ────────────────────────────────────
// Compact operational summary under the tabs: containers / uptime / CPU /
// memory / wallet UIs, drawn from the same live sources the detail panels
// use (docker stats + the status-endpoint HTTP probes). Additive to the
// Overview throughput + KPI rail — an at-a-glance "is my stack up?" strip
// that rides on every tab while the instance is running.
function VitalsRail({
  ports,
  createdAt,
  uptime,
  containers,
  endpoints,
  updatedAt,
  snapshotting,
}: {
  ports?: string;
  createdAt?: string;
  uptime?: string;
  containers: ContainersResponse | null;
  endpoints: Endpoint[];
  updatedAt: number | null;
  snapshotting?: boolean;
}) {
  const list = containers?.containers ?? [];
  const total = list.length;
  const running = total ? list.filter((c) => c.state === "running").length : 0;
  const healthy = containers?.healthy_count ?? 0;
  const starting = containers?.starting_count ?? 0;
  const unhealthy = containers?.unhealthy_count ?? 0;
  const noHealthcheck = Math.max(0, running - healthy - starting - unhealthy);
  const cpu = containers?.cpu_percent;
  const memUsed = containers?.mem_used_bytes;
  const memLimit = containers?.mem_limit_bytes;

  // Wallet UIs = the HTTP-probed wallet endpoints; "serving" = probe OK.
  const walletUIs = endpoints.filter(
    (e) => /wallet/i.test(e.label) && e.reachability != null,
  );
  const walletServing = walletUIs.filter((e) => e.reachability === "ok").length;

  const contSub = [
    `${healthy} probed healthy`,
    unhealthy > 0 ? `${unhealthy} unhealthy` : null,
    starting > 0 ? `${starting} starting` : null,
    noHealthcheck > 0 ? `${noHealthcheck} no healthcheck` : null,
  ]
    .filter(Boolean)
    .join(" · ");

  const sep = <span style={{ width: 1, alignSelf: "stretch", background: W.border }} />;

  return (
    <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: R.card }}>
      <div style={{ display: "flex", alignItems: "stretch", gap: 20, padding: "13px 16px", overflowX: "auto" }}>
        <VitalTile
          caption="Containers"
          value={snapshotting ? String(total || running) : String(running || total)}
          unit={snapshotting ? "containers" : "running"}
          sub={snapshotting ? "writers paused · capturing snapshot" : contSub || undefined}
          tone={
            snapshotting
              ? W.brand
              : total > 0 && unhealthy === 0
                ? W.ok
                : unhealthy > 0
                  ? W.warn
                  : undefined
          }
        />
        {sep}
        <VitalTile
          caption="Uptime"
          value={uptime ?? "—"}
          sub={createdAt ? `since ${createdAt.slice(11, 19)}Z` : undefined}
        />
        {sep}
        <VitalTile
          caption="CPU"
          value={cpu != null ? cpu.toFixed(0) : "—"}
          unit={cpu != null ? "%" : ""}
          sub="all containers"
        />
        {sep}
        <VitalTile
          caption="Memory"
          value={memUsed != null ? gib(memUsed) : "—"}
          unit={memUsed != null ? (memLimit ? `/ ${gib(memLimit)} GB` : "GB") : ""}
          sub="of granted"
        />
        {sep}
        <VitalTile
          caption="Wallet UIs"
          value={walletUIs.length ? String(walletServing) : "—"}
          unit={walletUIs.length ? `/ ${walletUIs.length} serving` : ""}
          sub="http probed"
          tone={
            walletUIs.length > 0 && walletServing === walletUIs.length
              ? W.ok
              : walletUIs.length > 0
                ? W.warn
                : undefined
          }
        />
      </div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 12,
          flexWrap: "wrap",
          padding: "8px 16px",
          borderTop: `1px solid ${W.border}`,
          fontFamily: wMono,
          fontSize: fs.label,
          color: W.faint,
        }}
      >
        <span>
          {ports ? `ports ${ports}` : ""}
          {ports && createdAt ? " · " : ""}
          {createdAt ? `created ${createdAt}` : ""}
        </span>
        <Freshness at={updatedAt} />
      </div>
    </div>
  );
}

function VitalTile({
  caption,
  value,
  unit,
  sub,
  tone,
}: {
  caption: string;
  value: string;
  unit?: string;
  sub?: string;
  tone?: string;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 3, minWidth: 0, flex: "1 1 0" }}>
      <span style={{ ...wideCaps, fontSize: fs.micro, color: W.dim } as CSSProperties}>{caption}</span>
      <span style={{ display: "flex", alignItems: "baseline", gap: 6, whiteSpace: "nowrap" }}>
        <span
          style={{
            fontFamily: wMono,
            fontSize: fs.lead,
            fontWeight: 600,
            color: tone ?? W.text,
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {value}
        </span>
        {unit && <span style={{ fontSize: fs.label, color: W.faint }}>{unit}</span>}
      </span>
      {sub && (
        <span
          title={sub}
          style={{
            fontSize: fs.micro,
            color: W.faint,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {sub}
        </span>
      )}
    </div>
  );
}

// Self-ticking relative timestamp so only this label re-renders each second,
// not the whole overview. `at` is epoch ms of the last docker-stats poll.
function Freshness({ at }: { at: number | null }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => (n + 1) % 1000), 1000);
    return () => clearInterval(id);
  }, []);
  if (!at) return null;
  const secs = Math.max(0, Math.round((Date.now() - at) / 1000));
  const rel = secs < 1 ? "just now" : secs < 60 ? `${secs}s ago` : `${Math.round(secs / 60)}m ago`;
  return <span>docker · updated {rel}</span>;
}

// ── Endpoints panel ─────────────────────────────────────────

function EndpointsPanel({
  name,
  endpoints,
  unreachable,
}: {
  name: string;
  endpoints: Endpoint[];
  unreachable: Endpoint[];
}) {
  const [copied, setCopied] = useState(false);
  async function copyAsEnv() {
    try {
      const text = await fetchAppConfigText(name, "env");
      await navigator.clipboard?.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1100);
    } catch {
      /* clipboard or fetch unavailable — no-op */
    }
  }
  return (
    <div style={scrollPanelStyle}>
      <div style={panelHeaderStyle}>
        <span style={panelTitleStyle}>Endpoints</span>
        <span style={{ fontSize: fs.meta, color: W.dim }}>what your app connects to</span>
        <button type="button" onClick={copyAsEnv} style={headerLinkStyle}>
          {copied ? "Copied" : "Copy as env"}
        </button>
      </div>
      <div style={{ ...gridRowStyle(ENDPOINT_COLS), ...columnHeaderStyle }}>
        <span>Service</span>
        <span>URL</span>
        <span>State</span>
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        {endpoints.length === 0 ? (
          <div style={{ color: W.dim, fontSize: fs.meta, padding: "12px 14px" }}>
            No endpoints registered yet.
          </div>
        ) : (
          endpoints.map((e) => (
            <div key={e.key ?? e.label} style={{ ...gridRowStyle(ENDPOINT_COLS), ...cellRowStyle }}>
              <span>{e.label}</span>
              <span>{e.url}</span>
              <Reachability value={e.reachability} />
            </div>
          ))
        )}
      </div>
      {unreachable.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            padding: "9px 14px",
            background: W.warnBg,
            borderTop: `1px solid ${W.warnBorder}`,
            fontSize: fs.meta,
            color: W.warn,
          }}
        >
          <IcAlert size={13} />
          <span>
            {unreachable.map((e) => e.label).join(", ")} not serving HTTP — usually a
            stale port overlay. <strong style={{ fontWeight: 600 }}>Recreate</strong>{" "}
            regenerates it.
          </span>
        </div>
      )}
    </div>
  );
}

// Reachability is meaningful only for the http UI endpoints. gRPC / JSON /
// Postgres are never probed, so they render a neutral em-dash — never a
// fabricated "reachable".
function Reachability({ value }: { value?: "ok" | "unreachable" }) {
  if (value === "ok") return <span style={{ color: W.ok }}>reachable</span>;
  if (value === "unreachable") return <span style={{ color: W.warn }}>unreachable</span>;
  return <span style={{ color: W.dim }}>—</span>;
}

// ── Containers panel ────────────────────────────────────────

function ContainersPanel({
  name,
  data,
  absent,
}: {
  name: string;
  data: ContainersResponse | null;
  absent: boolean;
}) {
  const [logsFor, setLogsFor] = useState<string | null>(null);
  const [restarting, setRestarting] = useState<Set<string>>(new Set());
  const [restartErr, setRestartErr] = useState<string | null>(null);
  const rows = useMemo(
    () => (data ? [...data.containers].sort((a, b) => severity(a) - severity(b)) : []),
    [data],
  );
  const total = data?.containers.length ?? 0;
  const healthy = data?.healthy_count ?? 0;

  // Restart one container (docker restart) — the CLI has `container restart`,
  // so the Web UI must keep a path to it. Confirms first (in-flight requests
  // to it drop), then the 3s poll reflects the new state.
  async function onRestart(container: string) {
    if (
      !(await confirmDialog({
        title: "Restart container?",
        body: `Stops then starts ${container}. In-flight requests to it may drop.`,
        detail: `docker restart ${container}`,
        confirmLabel: "Restart",
        danger: true,
      }))
    ) {
      return;
    }
    setRestarting((s) => new Set([...s, container]));
    setRestartErr(null);
    try {
      await restartContainer(name, container);
    } catch (e) {
      setRestartErr(`Restart ${container} failed: ` + (e instanceof ApiError ? e.message : "unknown error"));
    } finally {
      setRestarting((s) => {
        const next = new Set(s);
        next.delete(container);
        return next;
      });
    }
  }
  return (
    <div style={scrollPanelStyle}>
      <div style={panelHeaderStyle}>
        <span style={panelTitleStyle}>Containers</span>
        <span style={{ fontSize: fs.meta, color: W.dim, fontFamily: wMono }}>polled 3s</span>
        {data && (
          <span
            style={{
              marginLeft: "auto",
              display: "inline-flex",
              alignItems: "center",
              height: 20,
              padding: "0 8px",
              borderRadius: R.control,
              border: `1px solid ${W.okBorder}`,
              background: W.okBg,
              color: W.ok,
              fontSize: fs.label,
              fontWeight: 500,
              whiteSpace: "nowrap",
            }}
          >
            {healthy} of {total} healthy
          </span>
        )}
      </div>
      <div style={{ ...gridRowStyle(CONTAINER_COLS), ...columnHeaderStyle }}>
        <span>Service</span>
        <span>State</span>
        <span>Up</span>
        <span />
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        {absent ? (
          <div style={{ color: W.dim, fontSize: fs.meta, padding: "12px 14px" }}>
            No docker project — the instance isn't running.
          </div>
        ) : rows.length === 0 ? (
          <div style={{ color: W.dim, fontSize: fs.meta, padding: "12px 14px" }}>
            {data ? "No containers found for this compose project." : "Querying docker…"}
          </div>
        ) : (
          rows.map((c) => {
            const color = signalFor(c);
            return (
              <div
                key={c.name}
                role="button"
                tabIndex={0}
                onClick={() => setLogsFor(c.name)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    setLogsFor(c.name);
                  }
                }}
                title={`View logs for ${c.name}`}
                aria-label={`View logs for ${c.name}`}
                style={{ ...gridRowStyle(CONTAINER_COLS), ...cellRowStyle, cursor: "pointer" }}
              >
                <span style={{ display: "inline-flex", alignItems: "center", gap: 8, overflow: "hidden" }}>
                  <Dot color={color} />
                  <span
                    style={{
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {c.service}
                  </span>
                </span>
                <span style={{ color }}>
                  {c.state}
                  {c.health && <span style={{ color: W.dim }}> · {c.health}</span>}
                </span>
                <span style={{ color: W.dim }}>{deriveUp(c)}</span>
                <button
                  type="button"
                  title={`Restart ${c.service}`}
                  aria-label={`Restart ${c.service}`}
                  disabled={restarting.has(c.name)}
                  onClick={(e) => {
                    e.stopPropagation();
                    void onRestart(c.name);
                  }}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    width: 22,
                    height: 22,
                    padding: 0,
                    border: "none",
                    background: "transparent",
                    color: restarting.has(c.name) ? W.brand : W.dim,
                    cursor: restarting.has(c.name) ? "default" : "pointer",
                  }}
                >
                  <IcRefresh size={13} />
                </button>
              </div>
            );
          })
        )}
      </div>
      {restartErr && (
        <div role="alert" style={{ color: W.err, fontSize: fs.label, padding: "6px 14px", borderTop: `1px solid ${W.border}` }}>
          {restartErr}
        </div>
      )}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "9px 14px",
          background: W.sunken,
          borderTop: `1px solid ${W.border}`,
        }}
      >
        <span style={{ fontFamily: wMono, fontSize: fs.label, color: W.faint }}>
          docker compose ps
        </span>
        <button
          type="button"
          onClick={() => rows[0] && setLogsFor(rows[0].name)}
          disabled={rows.length === 0}
          style={{ ...headerLinkStyle, marginLeft: 0 }}
        >
          Logs
        </button>
      </div>
      <ContainerLogsModal
        open={logsFor !== null}
        instance={name}
        container={logsFor ?? ""}
        onClose={() => setLogsFor(null)}
      />
    </div>
  );
}

// ── JWT panel ───────────────────────────────────────────────

const ROLES = ["app-provider", "app-user", "sv"] as const;
type Role = (typeof ROLES)[number];

// "app-provider" → "App-provider", "sv" → "SV" for the segmented control.
const roleLabel = (r: string) => (r === "sv" ? "SV" : r.charAt(0).toUpperCase() + r.slice(1));

function JwtPanel({ name }: { name: string }) {
  const [role, setRole] = useState<Role>("app-provider");
  const [audience, setAudience] = useState("https://canton.network.global");
  const [jwt, setJwt] = useState<JwtResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    issueJwt(name, { role, audience })
      .then((r) => {
        if (!cancelled) {
          setJwt(r);
          setErr(null);
        }
      })
      .catch((e) => {
        if (!cancelled) setErr(e instanceof ApiError ? e.message : "failed to issue JWT");
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [name, role, audience]);

  const token = jwt?.token ?? null;
  const subject = token ? decodeJwtSub(token) ?? jwt?.party ?? roleLabel(role) : "—";
  const meta = jwtMeta(jwt);

  function copyToken() {
    if (!token) return;
    navigator.clipboard?.writeText(token);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div style={panelStyle}>
      <div style={{ ...panelHeaderStyle, flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
        <span style={panelTitleStyle}>JWT generator</span>
        <span style={{ fontSize: fs.meta, color: W.dim }}>
          Signed against Splice LocalNet dev secret
        </span>
      </div>
      <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 14 }}>
        <div style={fieldColStyle}>
          <span style={fieldCaptionStyle}>Role</span>
          <Segmented
            options={ROLES as unknown as string[]}
            value={role}
            onChange={(v) => setRole(v as Role)}
            format={roleLabel}
          />
        </div>
        <div style={fieldColStyle}>
          <span style={fieldCaptionStyle}>Audience</span>
          <input
            value={audience}
            onChange={(e) => setAudience(e.target.value)}
            aria-label="JWT audience"
            style={appInputStyle}
          />
        </div>

        {/* Result card: subject + copy, then the color-coded token. */}
        <div
          style={{
            border: `1px solid ${W.border}`,
            borderRadius: R.control,
            overflow: "hidden",
            background: W.inset,
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 8,
              padding: "8px 12px",
              borderBottom: `1px solid ${W.border}`,
            }}
          >
            <span
              style={{
                fontFamily: wMono,
                fontSize: fs.meta,
                color: W.text2,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {subject}
            </span>
            <button
              type="button"
              onClick={copyToken}
              disabled={!token}
              aria-label="Copy JWT"
              title={copied ? "Copied" : "Copy token"}
              style={iconBtnStyle(!token)}
            >
              <IcCopy size={14} />
            </button>
          </div>
          <div
            style={{
              padding: "10px 12px",
              fontFamily: wMono,
              fontSize: fs.label,
              wordBreak: "break-all",
              lineHeight: 1.6,
            }}
          >
            <JwtToken token={busy ? "…" : token ?? "—"} revealed={!!token} />
          </div>
          {meta && (
            <div
              style={{
                padding: "6px 12px",
                borderTop: `1px solid ${W.border}`,
                fontFamily: wMono,
                fontSize: fs.label,
                color: W.faint,
              }}
            >
              {meta}
            </div>
          )}
        </div>

        {jwt?.warning_dev_secret && (
          <div style={{ fontSize: fs.meta, color: W.warn, lineHeight: 1.5 }}>
            {jwt.warning_dev_secret}
          </div>
        )}
        {err && <div style={{ color: W.err, fontSize: fs.meta }}>{err}</div>}
      </div>
    </div>
  );
}

// Color-coded JWT: header · payload · signature (blue · muted · amber),
// matching the jwt.io convention. Borderless — the result card frames it.
function JwtToken({ token, revealed }: { token: string; revealed: boolean }) {
  const parts = token.split(".");
  if (parts.length === 3 && revealed) {
    return (
      <>
        <span style={{ color: W.brandText }}>{parts[0]}</span>
        <span style={{ color: W.faint }}>.</span>
        <span style={{ color: W.text2 }}>{parts[1]}</span>
        <span style={{ color: W.faint }}>.</span>
        <span style={{ color: W.amber }}>{parts[2]}</span>
      </>
    );
  }
  return <span style={{ color: W.dim }}>{token}</span>;
}

// ── App config panel ────────────────────────────────────────

function AppConfigPanel({ name }: { name: string }) {
  const [format, setFormat] = useState<AppConfigFormat>("env");
  const [body, setBody] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    const p =
      format === "json"
        ? fetchAppConfigJSON(name).then((j) => JSON.stringify(j, null, 2))
        : fetchAppConfigText(name, format);
    p.then((text) => {
      if (cancelled) return;
      setBody(text);
      setErr(null);
    })
      .catch((e) => {
        if (!cancelled) setErr(e instanceof ApiError ? e.message : "failed to load config");
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [name, format]);

  function copyBody() {
    if (!body) return;
    navigator.clipboard?.writeText(body);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div style={panelStyle}>
      <div style={{ ...panelHeaderStyle, flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
        <span style={panelTitleStyle}>App config</span>
        <span style={{ fontSize: fs.meta, color: W.dim }}>
          endpoints + party IDs in your preferred format
        </span>
      </div>
      <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
        {/* Format tabs + copy / download. */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 2,
            borderBottom: `1px solid ${W.border}`,
          }}
        >
          {(["env", "json", "yaml"] as AppConfigFormat[]).map((f) => {
            const active = f === format;
            return (
              <button
                key={f}
                type="button"
                onClick={() => setFormat(f)}
                style={{
                  appearance: "none",
                  background: "transparent",
                  border: "none",
                  borderBottom: `2px solid ${active ? W.brand : "transparent"}`,
                  cursor: "pointer",
                  padding: "6px 10px",
                  marginBottom: -1,
                  fontSize: fs.meta,
                  fontFamily: "inherit",
                  fontWeight: active ? 600 : 400,
                  letterSpacing: 0.4,
                  textTransform: "uppercase",
                  color: active ? W.text : W.dim,
                }}
              >
                {f}
              </button>
            );
          })}
          <span style={{ marginLeft: "auto", display: "flex", gap: 2 }}>
            <button
              type="button"
              onClick={copyBody}
              disabled={!body}
              aria-label="Copy config"
              title={copied ? "Copied" : "Copy"}
              style={iconBtnStyle(!body)}
            >
              <IcCopy size={14} />
            </button>
            <button
              type="button"
              onClick={() => saveEnv(name, body)}
              disabled={!body || format !== "env"}
              aria-label="Download .env"
              title={format === "env" ? "Download as .env" : "Switch to ENV to save a .env file"}
              style={iconBtnStyle(!body || format !== "env")}
            >
              <IcDownload size={14} />
            </button>
          </span>
        </div>
        <pre
          style={{
            margin: 0,
            minWidth: 0,
            background: W.inset,
            border: `1px solid ${W.border}`,
            borderRadius: R.control,
            padding: "10px 12px",
            fontFamily: wMono,
            fontSize: fs.label,
            color: W.text2,
            lineHeight: 1.7,
            maxHeight: 220,
            overflow: "auto",
            whiteSpace: "pre",
            overflowWrap: "normal",
          }}
        >
          {busy ? "…" : body || "—"}
        </pre>
        <span style={{ fontFamily: wMono, fontSize: fs.label, color: W.faint }}>
          eval "$(dpm localnet env {name})"
        </span>
        {err && <div style={{ color: W.err, fontSize: fs.meta }}>{err}</div>}
      </div>
    </div>
  );
}

// ── Activity tab ────────────────────────────────────────────

// Snapshot with manual refresh — /transactions is a window scan, not an
// SSE stream. Mounted only while the Activity tab is open.
function RecentActivity({ name }: { name: string }) {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; rows: TransactionRow[]; truncated: boolean }
    | { kind: "needs-jwt" }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchTransactions(name, "app-provider", 40)
      .then((r) => {
        if (cancelled) return;
        setState({ kind: "ok", rows: r.transactions, truncated: !!r.window_truncated });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({ kind: "needs-jwt" });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load activity",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, tick]);

  const events = useMemo(() => {
    if (state.kind !== "ok") return [];
    const flat: {
      offset: number;
      key: string;
      time: string;
      kind: TransactionEvent["kind"];
      event: string;
      cid: string;
    }[] = [];
    for (const tx of state.rows) {
      if (tx.kind !== "transaction" || !tx.events) continue;
      const time = tx.record_time
        ? tx.record_time.replace("T", " ").slice(11, 19)
        : `@${tx.offset}`;
      tx.events.forEach((ev, i) => {
        flat.push({
          offset: tx.offset,
          key: `${tx.offset}:${i}`,
          time,
          kind: ev.kind,
          event: shortTemplate(ev.template),
          cid: ev.contract_id,
        });
      });
    }
    flat.sort((a, b) => b.offset - a.offset);
    return flat.slice(0, 14);
  }, [state]);

  const kindColor: Record<TransactionEvent["kind"], string> = {
    create: W.ok,
    exercise: W.info,
    archive: W.err,
  };

  return (
    <Panel>
      <header style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
        <h3 style={{ margin: 0, fontSize: fs.lead, fontWeight: 600, color: W.text }}>
          Recent activity
        </h3>
        <span style={{ color: W.dim, fontSize: fs.meta }}>
          ledger events · as seen by the app-provider participant
        </span>
        <Button
          variant="secondary"
          icon={<IcRefresh />}
          onClick={() => setTick((t) => t + 1)}
          title="Refresh"
          style={{ marginLeft: "auto" }}
        >
          Refresh
        </Button>
      </header>

      {state.kind === "loading" && (
        <div style={{ color: W.dim, fontSize: fs.data, padding: "8px 0" }}>
          Scanning recent ledger updates…
        </div>
      )}
      {state.kind === "needs-jwt" && (
        <div style={{ color: W.dim, fontSize: fs.body, padding: "8px 0" }}>
          Ledger activity needs a party-rights JWT. Splice LocalNet signs user-id tokens
          by default. Open the Explorer to project through a specific party.
        </div>
      )}
      {state.kind === "err" && (
        <div style={{ color: W.dim, fontSize: fs.body, padding: "8px 0" }}>
          {/no jwt recorded/i.test(state.error)
            ? "Ledger activity needs recorded role JWTs. Restart the instance to capture them (older instances predate JWT capture)."
            : `Ledger activity unavailable. ${state.error}.`}{" "}
          Open the Explorer for the full ledger view.
        </div>
      )}
      {state.kind === "ok" && events.length === 0 && (
        <div style={{ color: W.dim, fontSize: fs.body, padding: "8px 0" }}>
          No recent ledger activity. Run a token or DAR operation to see updates.
        </div>
      )}
      {state.kind === "ok" && events.length > 0 && (
        <table
          style={{ width: "100%", borderCollapse: "collapse", fontSize: fs.data, marginTop: 8 }}
        >
          <thead>
            <tr style={{ color: W.dim, textAlign: "left" }}>
              <th style={actTh}>TIME</th>
              <th style={actTh}>KIND</th>
              <th style={actTh}>EVENT</th>
              <th style={actTh}>CID</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.key} style={{ borderTop: `1px solid ${W.border}` }}>
                <td
                  style={{
                    ...actTd,
                    color: W.dim,
                    fontFamily: wMono,
                    fontVariantNumeric: "tabular-nums",
                    fontSize: fs.label,
                  }}
                >
                  {e.time}
                </td>
                <td style={actTd}>
                  <span
                    style={{
                      color: kindColor[e.kind],
                      border: `1px solid ${tint(kindColor[e.kind], 34)}`,
                      background: tint(kindColor[e.kind], 13),
                      borderRadius: R.control,
                      padding: "1px 6px",
                      fontSize: fs.label,
                    }}
                  >
                    {e.kind}
                  </span>
                </td>
                <td style={{ ...actTd, fontFamily: wMono, color: W.text2 }}>{e.event}</td>
                <td style={actTd}>
                  <MonoId value={e.cid} head={6} tail={6} size={11} color={W.info} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {state.kind === "ok" && state.truncated && (
        <div style={{ color: W.dim, fontSize: fs.label, marginTop: 8 }}>
          Showing the newest events of a clipped scan.
        </div>
      )}
    </Panel>
  );
}

// ── shared bits ─────────────────────────────────────────────

function Panel({ children, padded = true }: { children: React.ReactNode; padded?: boolean }) {
  return (
    <section
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: padded ? 16 : 0,
        overflow: "hidden",
      }}
    >
      {children}
    </section>
  );
}

function PanelFooter({ left }: { left: string }) {
  return (
    <div
      style={{
        marginTop: 8,
        padding: "8px 14px",
        background: W.sunken,
        borderTop: `1px solid ${W.border}`,
        fontFamily: wMono,
        fontSize: fs.label,
        color: W.faint,
      }}
    >
      {left}
    </div>
  );
}

function Segmented({
  options,
  value,
  onChange,
  format,
}: {
  options: string[];
  value: string;
  onChange: (v: string) => void;
  format?: (v: string) => string;
}) {
  return (
    <div
      style={{
        display: "inline-flex",
        padding: 2,
        gap: 2,
        background: W.inset,
        borderRadius: R.control,
        width: "max-content",
      }}
    >
      {options.map((opt) => {
        const active = opt === value;
        return (
          <button
            key={opt}
            type="button"
            onClick={() => onChange(opt)}
            style={{
              appearance: "none",
              border: "none",
              cursor: "pointer",
              padding: "4px 12px",
              borderRadius: R.control,
              fontSize: fs.meta,
              fontFamily: "inherit",
              fontWeight: active ? 600 : 400,
              whiteSpace: "nowrap",
              background: active ? W.accentSolid : "transparent",
              color: active ? W.onAccentSolid : W.text2,
            }}
          >
            {format ? format(opt) : opt}
          </button>
        );
      })}
    </div>
  );
}

const panelStyle: CSSProperties = {
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: R.card,
  display: "flex",
  flexDirection: "column",
};

const scrollPanelStyle: CSSProperties = {
  ...panelStyle,
  height: 326,
  overflow: "hidden",
};

const panelHeaderStyle: CSSProperties = {
  display: "flex",
  alignItems: "baseline",
  gap: 12,
  padding: "12px 14px",
  borderBottom: `1px solid ${W.border}`,
};

const panelTitleStyle: CSSProperties = {
  fontSize: fs.lead,
  fontWeight: 600,
  color: W.text,
};

// Label-above-control field group (Role / Audience in the JWT card).
const fieldColStyle: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 6,
  alignItems: "stretch",
};

const fieldCaptionStyle: CSSProperties = {
  fontSize: fs.meta,
  fontWeight: 600,
  color: W.text2,
};

const appInputStyle: CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  background: W.inset,
  color: W.text,
  border: `1px solid ${W.border}`,
  borderRadius: R.control,
  padding: "6px 10px",
  fontSize: fs.meta,
  fontFamily: wMono,
};

// Borderless icon button for the copy / download affordances in card headers.
function iconBtnStyle(disabled: boolean): CSSProperties {
  return {
    appearance: "none",
    background: "transparent",
    border: "none",
    cursor: disabled ? "default" : "pointer",
    color: disabled ? W.faint : W.dim,
    padding: 4,
    borderRadius: R.control,
    display: "inline-flex",
    alignItems: "center",
    opacity: disabled ? 0.5 : 1,
  };
}

const headerLinkStyle: CSSProperties = {
  marginLeft: "auto",
  appearance: "none",
  background: "transparent",
  border: "none",
  cursor: "pointer",
  padding: 0,
  fontFamily: "inherit",
  fontSize: fs.meta,
  fontWeight: 500,
  color: W.brandText,
  whiteSpace: "nowrap",
};

const columnHeaderStyle: CSSProperties = {
  background: W.surface2,
  borderBottom: `1px solid ${W.border}`,
  ...tableCaps,
  fontSize: fs.label,
  color: W.dim,
};

const cellRowStyle: CSSProperties = {
  borderBottom: `1px solid ${W.border}`,
  alignItems: "center",
  fontFamily: wMono,
  fontSize: fs.meta,
  color: W.text2,
};

const ENDPOINT_COLS = "1.2fr 1.3fr 0.8fr";
const CONTAINER_COLS = "1.4fr 0.85fr 0.85fr 30px";

function gridRowStyle(cols: string): CSSProperties {
  return {
    display: "grid",
    gridTemplateColumns: cols,
    gap: 12,
    padding: "9px 14px",
  };
}

const actTh: CSSProperties = { ...tableCaps, padding: "6px 10px 6px 0", fontSize: fs.label };
const actTd: CSSProperties = { padding: "8px 10px 8px 0", verticalAlign: "middle" };

// Lower sorts earlier, so failure-mode containers surface first.
function severity(c: { state: string; health?: string }): number {
  if (c.state === "restarting") return 0;
  if (c.state === "dead" || c.state === "exited") return 1;
  if (c.health === "unhealthy") return 2;
  if (c.health === "starting") return 3;
  if (c.state === "paused") return 4;
  return 5;
}

function signalFor(c: { state: string; health?: string }): string {
  if (c.state === "restarting") return W.warn;
  if (c.state === "dead" || c.state === "exited") return W.err;
  if (c.state === "paused") return W.dim;
  if (c.health === "unhealthy") return W.err;
  if (c.health === "starting") return W.brand;
  if (c.health === "healthy") return W.ok;
  if (c.state === "running") return W.ok;
  return W.dim;
}

// "Up" column, derived from docker's raw status string. Restarting rows
// surface the attempt count; running rows drop the "Up " prefix and the
// trailing "(health: …)" so only the duration shows.
function deriveUp(c: ContainerHealth): string {
  const s = c.status ?? "";
  if (c.state === "restarting") {
    const m = s.match(/Restarting\s*\((\d+)\)/i);
    if (m) return `${m[1]} attempt${m[1] === "1" ? "" : "s"}`;
    return "restarting";
  }
  const up = s.match(/^Up\s+(.*)/i);
  // Drop docker's trailing health parenthetical — "(healthy)",
  // "(unhealthy)", "(health: starting)" — so only the duration shows.
  if (up) return up[1].replace(/\s*\([^)]*\)\s*$/, "").trim();
  if (c.state === "exited" || c.state === "dead") {
    const ex = s.match(/Exited\s*\((\d+)\)/i);
    return ex ? `exited (${ex[1]})` : "exited";
  }
  return c.state || "—";
}

// Derive the raw token's alg (decoded header) and TTL for the meta line —
// both real, so nothing is invented on the "expires in 24h · HS256" hint.
function jwtMeta(jwt: JwtResponse | null): string {
  const parts: string[] = [];
  const ttl = jwt?.expires_in_seconds;
  if (typeof ttl === "number" && ttl > 0) {
    const hours = Math.round(ttl / 3600);
    parts.push(hours >= 1 ? `expires in ${hours}h` : `expires in ${Math.round(ttl / 60)}m`);
  }
  const alg = decodeJwtAlg(jwt?.token);
  if (alg) parts.push(alg);
  return parts.join(" · ");
}

function decodeJwtAlg(token?: string): string | null {
  const seg = token?.split(".")[0];
  if (!seg) return null;
  try {
    const json = atob(seg.replace(/-/g, "+").replace(/_/g, "/"));
    const hdr = JSON.parse(json) as { alg?: unknown };
    return typeof hdr.alg === "string" ? hdr.alg : null;
  } catch {
    return null;
  }
}

// The token's `sub` claim — the ledger-api user the JWT authenticates as.
// Labels the result card (e.g. "ledger-api-user").
function decodeJwtSub(token?: string | null): string | null {
  const seg = token?.split(".")[1];
  if (!seg) return null;
  try {
    const json = atob(seg.replace(/-/g, "+").replace(/_/g, "/"));
    const payload = JSON.parse(json) as { sub?: unknown };
    return typeof payload.sub === "string" ? payload.sub : null;
  } catch {
    return null;
  }
}

function saveEnv(name: string, body: string) {
  const blob = new Blob([body], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}.env`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

// `<pkg>:Module:Entity` → `Module:Entity` for a compact EVENT column.
function shortTemplate(t?: string): string {
  if (!t) return "—";
  const parts = t.split(":");
  return parts.length >= 3 ? `${parts[parts.length - 2]}:${parts[parts.length - 1]}` : t;
}
