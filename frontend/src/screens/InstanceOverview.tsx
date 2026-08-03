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
  fetchInstance,
  fetchMetricsRange,
  fetchMetricsSummary,
  fetchTransactions,
  issueJwt,
  restartContainer,
  pauseInstance,
  recreateInstance,
  scrubInstance,
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
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      try {
        const r = await fetchContainers(name);
        if (!cancelled) {
          setContainers(r);
          setContainersAbsent(false);
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
        }));
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
          act(() => scrubInstance(name), {
            confirm: {
              title: "Remove from registry?",
              body: `Deletes the ${name} entry and its state.json. Docker volumes (if any) are not touched.`,
              detail: `dpm localnet remove --name ${name}`,
              confirmLabel: "Remove",
              danger: true,
            },
            refetch: false,
          })
        }
      />

      <TabBar tab={tab} onChange={setTab} />

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
            <ThroughputPanel name={name} tps={tps} />
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
                gridTemplateColumns: "1fr 1.25fr",
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

      {tab === "snapshots" && <BackupRestore instanceName={name} />}
    </section>
  );
}

// ── Header + action bar ─────────────────────────────────────

function Header({
  name,
  status,
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
  const meta: string[] = [];
  if (instance?.splice_version) meta.push(`Splice ${instance.splice_version}`);
  if (ports) meta.push(`ports ${ports}`);
  if (instance?.uptime) meta.push(`up ${instance.uptime}`);
  if (instance?.created_at) meta.push(`created ${instance.created_at}`);

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
          {status && <StatusBadge status={status} variant="pill" />}
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
          title="Remove the registry entry only. Won't touch docker — run Down first if containers are live."
        >
          {busy ? "Removing…" : "Remove entry"}
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
          title="Remove the registry entry + state.json. Docker volumes (if any) untouched."
        >
          {busy ? "Removing…" : "Remove entry"}
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
  // TODO(metrics): the mockup's KPI rail also showed Latency p99, Active
  // contracts, CPU and Memory. On Splice 0.6.4 p99 is NaN (+Inf-only
  // histogram), there is no ACS-cardinality metric, and CPU/mem have no
  // docker-stats source — so the rail is omitted and throughput runs full
  // width. Reinstate the rail here once those signals are populated.
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
            height={150}
            yLabel="tx/s"
          />
        )}
      </div>
      <PanelFooter left={`dpm localnet metrics ${name}`} />
    </div>
  );
}

// ── Monitoring OFF strip ────────────────────────────────────

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
  // Only Docker-answerable facts ride here: container count + healthy and
  // uptime. CPU/Memory are intentionally absent — ServiceStatus.CPUPct /
  // Memory are never populated (no docker-stats source on 0.6.4).
  const total = containers?.containers.length ?? 0;
  const healthy = containers?.healthy_count ?? 0;
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
                onClick={() => setLogsFor(c.name)}
                title={`View logs for ${c.name}`}
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

function JwtPanel({ name }: { name: string }) {
  const [role, setRole] = useState<Role>("app-provider");
  const [audience, setAudience] = useState("https://canton.network.global");
  const [jwt, setJwt] = useState<JwtResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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
  return (
    <div style={panelStyle}>
      <div style={{ ...panelHeaderStyle, flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
        <span style={panelTitleStyle}>JWT</span>
        <span style={{ fontSize: fs.meta, color: W.dim }}>
          signed with the Splice LocalNet dev secret — this instance only
        </span>
      </div>
      <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 12 }}>
        <FieldRow label="role">
          <Segmented
            options={ROLES as unknown as string[]}
            value={role}
            onChange={(v) => setRole(v as Role)}
          />
        </FieldRow>
        <FieldRow label="audience">
          <input
            value={audience}
            onChange={(e) => setAudience(e.target.value)}
            style={{
              flex: 1,
              background: W.inset,
              color: W.text,
              border: `1px solid ${W.border}`,
              borderRadius: R.control,
              padding: "6px 10px",
              fontSize: fs.meta,
              fontFamily: wMono,
            }}
          />
        </FieldRow>
        <FieldRow label="party">
          {jwt?.party ? (
            <MonoId value={jwt.party} size={fs.meta} color={W.text2} />
          ) : (
            <code style={{ color: W.dim, fontFamily: wMono, fontSize: fs.meta }}>—</code>
          )}
        </FieldRow>
        <TokenBox token={busy ? "…" : token ?? "—"} revealed={!!token} />
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <Button
            variant="secondary"
            onClick={() => token && navigator.clipboard?.writeText(token)}
            disabled={!token}
          >
            Copy token
          </Button>
          {jwtMeta(jwt) && (
            <span style={{ fontFamily: wMono, fontSize: fs.label, color: W.faint }}>
              {jwtMeta(jwt)}
            </span>
          )}
        </div>
        {jwt?.warning_dev_secret && (
          <div
            style={{
              display: "flex",
              gap: 8,
              alignItems: "flex-start",
              padding: "9px 10px",
              border: `1px solid ${W.warnBorder}`,
              background: W.warnBg,
              borderRadius: R.control,
              color: W.warn,
              fontSize: fs.meta,
              lineHeight: 1.5,
            }}
          >
            <IcAlert size={13} style={{ marginTop: 2 }} />
            <span>{jwt.warning_dev_secret}</span>
          </div>
        )}
        {err && <div style={{ color: W.err, fontSize: fs.meta }}>{err}</div>}
      </div>
    </div>
  );
}

function TokenBox({ token, revealed }: { token: string; revealed: boolean }) {
  const parts = token.split(".");
  const isJwt = parts.length === 3 && revealed;
  return (
    <div
      style={{
        background: W.inset,
        border: `1px solid ${W.border}`,
        borderRadius: R.control,
        padding: "10px 12px",
        fontFamily: wMono,
        fontSize: fs.label,
        wordBreak: "break-all",
        lineHeight: 1.6,
        color: W.text2,
      }}
    >
      {isJwt ? (
        <>
          <span style={{ color: W.brandText }}>{parts[0]}</span>
          <span style={{ color: W.faint }}>.</span>
          <span style={{ color: W.text2 }}>{parts[1]}</span>
          <span style={{ color: W.faint }}>.</span>
          <span style={{ color: W.amber }}>{parts[2]}</span>
        </>
      ) : (
        <span style={{ color: W.dim }}>{token}</span>
      )}
    </div>
  );
}

// ── App config panel ────────────────────────────────────────

function AppConfigPanel({ name }: { name: string }) {
  const [format, setFormat] = useState<AppConfigFormat>("env");
  const [body, setBody] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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

  return (
    <div style={panelStyle}>
      <div style={{ ...panelHeaderStyle, alignItems: "flex-start" }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          <span style={panelTitleStyle}>App config</span>
          <span style={{ fontSize: fs.meta, color: W.dim }}>
            endpoints and party ids for your app or CI job
          </span>
        </div>
        <div style={{ marginLeft: "auto" }}>
          <Segmented
            options={["env", "json", "yaml"] as AppConfigFormat[]}
            value={format}
            onChange={(v) => setFormat(v as AppConfigFormat)}
          />
        </div>
      </div>
      <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
        <pre
          style={{
            margin: 0,
            background: W.inset,
            border: `1px solid ${W.border}`,
            borderRadius: R.control,
            padding: "10px 12px",
            fontFamily: wMono,
            fontSize: fs.label,
            color: W.text2,
            lineHeight: 1.7,
            maxHeight: 200,
            overflow: "auto",
            whiteSpace: "pre-wrap",
            overflowWrap: "anywhere",
          }}
        >
          {busy ? "…" : body || "—"}
        </pre>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <Button
            variant="secondary"
            onClick={() => navigator.clipboard?.writeText(body)}
            disabled={!body}
          >
            Copy
          </Button>
          <Button
            variant="secondary"
            icon={<IcDownload />}
            onClick={() => saveEnv(name, body)}
            disabled={!body || format !== "env"}
            title={format === "env" ? "Download as .env" : "Switch to env to save a .env file"}
          >
            Save .env
          </Button>
          <span
            style={{
              marginLeft: "auto",
              fontFamily: wMono,
              fontSize: fs.label,
              color: W.faint,
            }}
          >
            eval "$(dpm localnet env {name})"
          </span>
        </div>
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

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "76px 1fr", gap: 12, alignItems: "center" }}>
      <span style={{ fontSize: fs.meta, color: W.dim }}>{label}</span>
      {children}
    </div>
  );
}

function Segmented({
  options,
  value,
  onChange,
}: {
  options: string[];
  value: string;
  onChange: (v: string) => void;
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
            {opt}
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
