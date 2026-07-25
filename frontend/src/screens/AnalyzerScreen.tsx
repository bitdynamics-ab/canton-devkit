import { useEffect, useMemo, useState } from "react";
import {
  fetchAnalyzerStatus,
  fetchDARList,
  analyzeDeployedDar,
  analyzeUploadedDar,
  ApiError,
  type Role,
  type AnalyzerStatusResponse,
  type AnalyzerReport,
  type AnalyzerInteraction,
  type AnalyzerEndpoint,
  type DARRow,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono, tableCaps, R, tint, fs } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";

// Cross-package interaction analysis (Certora daml-analyzer). Analyze DARs
// deployed to the selected instance or uploaded ad hoc; several reports can
// be loaded at once so they can be compared. The views mirror the upstream
// analyzer's own viewer: highlights, a target-package summary pivot, the
// package graph, the raw interaction list, and a two-report diff.

const ROLES: Role[] = ["app-user", "app-provider", "sv"];
type Tab = "highlights" | "summary" | "graph" | "interactions" | "diff";
const TABS: Tab[] = ["highlights", "summary", "graph", "interactions", "diff"];

type Loaded = { id: string; darName: string; report: AnalyzerReport };
// Filter applied from the summary pivot: a target package, optionally
// narrowed to one interaction type.
type Filter = { pkg: string; type?: string } | null;

export function AnalyzerScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [status, setStatus] = useState<AnalyzerStatusResponse | null>(null);
  const [dars, setDars] = useState<DARRow[]>([]);
  const [reports, setReports] = useState<Loaded[]>([]);
  const [activeId, setActiveId] = useState<string>("");
  const [tab, setTab] = useState<Tab>("highlights");
  const [filter, setFilter] = useState<Filter>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let off = false;
    fetchAnalyzerStatus()
      .then((s) => !off && setStatus(s))
      .catch(() => !off && setStatus(null));
    return () => {
      off = true;
    };
  }, []);

  useEffect(() => {
    if (!name) {
      setDars([]);
      return;
    }
    let off = false;
    fetchDARList(name, role)
      .then((r) => !off && setDars(r.dars))
      .catch(() => !off && setDars([]));
    return () => {
      off = true;
    };
  }, [name, role]);

  function add(darName: string, report: AnalyzerReport) {
    const id = `${darName}#${report.analyzed_package.package_id.slice(0, 8)}`;
    setReports((prev) => [...prev.filter((r) => r.id !== id), { id, darName, report }]);
    setActiveId(id);
    setFilter(null);
  }

  async function run(what: string, p: Promise<{ dar_name?: string; report: AnalyzerReport | null }>) {
    setBusy(what);
    setErr(null);
    try {
      const resp = await p;
      if (resp.report) add(resp.dar_name || what, resp.report);
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  // Upload analyses run sequentially so a directory of DARs surfaces one
  // report per file without flooding the backend.
  async function runUploads(files: File[]) {
    setErr(null);
    for (const f of files) {
      setBusy(`upload:${f.name}`);
      try {
        const resp = await analyzeUploadedDar(f);
        if (resp.report) add(resp.dar_name || f.name, resp.report);
      } catch (e) {
        setErr(e instanceof ApiError ? e.message : String(e));
      }
    }
    setBusy(null);
  }

  const active = reports.find((r) => r.id === activeId) ?? reports[reports.length - 1];
  const gated = status !== null && !status.available;

  return (
    <section style={{ padding: 24 }}>
      <header style={{ display: "flex", alignItems: "baseline", gap: 12, marginBottom: 4, flexWrap: "wrap" }}>
        <h2 style={{ margin: 0 }}>Analyzer</h2>
        <span style={{ color: W.dim, fontSize: fs.lead }}>
          Cross-package interaction analysis · Certora daml-analyzer
        </span>
        <span style={{ marginLeft: "auto", display: "flex", gap: 4 }}>
          {ROLES.map((r) => (
            <button
              key={r}
              onClick={() => setRole(r)}
              style={{
                background: role === r ? W.brand : "transparent",
                color: role === r ? W.onAccent : W.dim,
                border: `1px solid ${role === r ? W.brand : W.border}`,
                borderRadius: R.control,
                padding: "3px 10px",
                fontSize: fs.label,
                cursor: "pointer",
              }}
            >
              {r}
            </button>
          ))}
        </span>
      </header>

      {gated && status && <NotConfigured status={status} />}

      {!gated && (
        <>
          <div style={{ display: "flex", alignItems: "center", gap: 12, margin: "18px 0", flexWrap: "wrap" }}>
            <label style={uploadStyle(!!busy)}>
              {busy?.startsWith("upload") ? `Analyzing ${busy.slice(7)}…` : "Analyze .dar file(s)"}
              <input
                type="file"
                accept=".dar"
                multiple
                disabled={!!busy}
                style={{ display: "none" }}
                onChange={(e) => {
                  const files = Array.from(e.target.files ?? []);
                  if (files.length) void runUploads(files);
                  e.target.value = "";
                }}
              />
            </label>
            {status?.source && (
              <span style={{ color: W.faint, fontSize: fs.meta, fontFamily: wMono }}>
                via {status.runtime === "component" ? "DPM component" : status.runtime} · {status.source}
              </span>
            )}
          </div>

          {name && dars.length > 0 && (
            <div style={{ marginBottom: 18 }}>
              <div style={{ ...tableCaps, color: W.dim, fontSize: fs.label, marginBottom: 6 }}>
                Deployed DARs on {role}
              </div>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                {dars.map((d) => (
                  <Button
                    key={d.main}
                    variant="secondary"
                    size="sm"
                    disabled={!!busy}
                    onClick={() => run(`${d.name} ${d.version}`, analyzeDeployedDar(name, d.main, role))}
                  >
                    {busy === `${d.name} ${d.version}` ? "Analyzing…" : `${d.name} ${d.version}`}
                  </Button>
                ))}
              </div>
            </div>
          )}

          {err && <div style={{ color: W.err, fontSize: fs.body, margin: "12px 0" }}>{err}</div>}

          {reports.length > 1 && (
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 12 }}>
              {reports.map((r) => (
                <button
                  key={r.id}
                  onClick={() => {
                    setActiveId(r.id);
                    setFilter(null);
                  }}
                  style={{
                    background: r.id === active?.id ? tint(W.brand, 14) : "transparent",
                    color: r.id === active?.id ? W.text : W.dim,
                    border: `1px solid ${r.id === active?.id ? W.brand : W.border}`,
                    borderRadius: R.control,
                    padding: "3px 10px",
                    fontSize: fs.label,
                    fontFamily: wMono,
                    cursor: "pointer",
                  }}
                >
                  {r.report.analyzed_package.name} {r.report.analyzed_package.version}
                </button>
              ))}
            </div>
          )}

          {active && (
            <ReportPanel
              loaded={active}
              all={reports}
              tab={tab}
              setTab={setTab}
              filter={filter}
              setFilter={setFilter}
            />
          )}

          {!active && !err && !busy && (
            <p style={{ color: W.dim, fontSize: fs.body }}>
              Pick a deployed DAR above{name ? "" : " (select an instance)"} or upload one or more .dar files.
            </p>
          )}
        </>
      )}
    </section>
  );
}

function NotConfigured({ status }: { status: AnalyzerStatusResponse }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        marginTop: 16,
        maxWidth: 640,
      }}
    >
      <div style={{ color: W.text, fontWeight: 600, marginBottom: 6 }}>Analyzer not configured</div>
      <div style={{ color: W.dim, fontSize: fs.body, marginBottom: 10 }}>
        {status.detail || "The analyzer runtime is not available in this environment."}
      </div>
      <div style={{ fontSize: fs.meta, fontFamily: wMono, color: W.dim }}>
        Install it as a DPM component: add{" "}
        <span style={{ color: W.text2 }}>oci://ghcr.io/certora/daml-analyzer:0.1.0</span> to daml.yaml,
        then run <span style={{ color: W.text2 }}>dpm install package</span>.
      </div>
    </div>
  );
}

function ReportPanel({
  loaded,
  all,
  tab,
  setTab,
  filter,
  setFilter,
}: {
  loaded: Loaded;
  all: Loaded[];
  tab: Tab;
  setTab: (t: Tab) => void;
  filter: Filter;
  setFilter: (f: Filter) => void;
}) {
  const report = loaded.report;
  const p = report.analyzed_package;
  const s = report.summary;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10, flexWrap: "wrap", marginBottom: 4 }}>
        <h3 style={{ margin: 0, color: W.text }}>{p.name}</h3>
        <span style={{ color: W.dim, fontFamily: wMono, fontSize: fs.meta }}>
          {p.version} · LF {p.lf_version ?? "?"}
        </span>
        {loaded.darName && <span style={{ color: W.faint, fontSize: fs.meta }}>{loaded.darName}</span>}
        <span style={{ marginLeft: "auto" }}>
          <Button variant="secondary" size="sm" onClick={() => downloadReport(loaded)}>
            Export JSON
          </Button>
        </span>
      </div>

      <div style={{ display: "flex", gap: 20, flexWrap: "wrap", margin: "12px 0", alignItems: "baseline" }}>
        <Stat label="Interactions" value={String(s.total_interactions)} />
        <Stat label="Dependencies" value={String(report.dependencies.length)} />
        <Stat label="Target packages" value={String(Object.keys(s.by_target_package).length)} />
      </div>

      <div style={{ display: "flex", gap: 4, margin: "6px 0 14px", borderBottom: `1px solid ${W.border}` }}>
        {TABS.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            disabled={t === "diff" && all.length < 2}
            title={t === "diff" && all.length < 2 ? "Load a second DAR to compare" : undefined}
            style={{
              background: "transparent",
              border: "none",
              borderBottom: `2px solid ${tab === t ? W.brand : "transparent"}`,
              color: tab === t ? W.text : t === "diff" && all.length < 2 ? W.faint : W.dim,
              padding: "6px 12px",
              fontSize: fs.data,
              fontWeight: tab === t ? 600 : 400,
              textTransform: "capitalize",
              cursor: t === "diff" && all.length < 2 ? "default" : "pointer",
            }}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "highlights" && <Highlights report={report} onJump={(f) => { setFilter(f); setTab("interactions"); }} />}
      {tab === "summary" && (
        <SummaryPivot
          report={report}
          onPick={(f) => {
            setFilter(f);
            setTab("interactions");
          }}
        />
      )}
      {tab === "graph" && <GraphView report={report} />}
      {tab === "interactions" && (
        <InteractionsTable report={report} filter={filter} clearFilter={() => setFilter(null)} />
      )}
      {tab === "diff" && <DiffView current={loaded} all={all} />}
    </div>
  );
}

// --- Highlights ----------------------------------------------------------

// Derived "what matters here" findings: the destructive and structural
// interactions an operator should notice first.
function Highlights({ report, onJump }: { report: AnalyzerReport; onJump: (f: Filter) => void }) {
  const ix = report.interactions;
  const consuming = ix.filter((i) => i.target.consuming);
  const impls = ix.filter((i) => i.type === "ImplementsInterface");
  const creates = ix.filter((i) => i.type === "Create");
  const byPkg = report.summary.by_target_package;
  const top = Object.entries(byPkg).sort((a, b) => b[1] - a[1])[0];
  const noSource = ix.filter((i) => !i.source?.file).length;

  const items: Array<{ tone: string; label: string; detail: string; pkg?: string; type?: string }> = [];
  if (consuming.length) {
    items.push({
      tone: W.warn,
      label: `${consuming.length} consuming exercise${consuming.length > 1 ? "s" : ""}`,
      detail: "archives a contract in another package — the destructive cross-package calls",
      type: "Exercise",
    });
  }
  if (impls.length) {
    items.push({
      tone: W.teal,
      label: `${impls.length} interface implementation${impls.length > 1 ? "s" : ""}`,
      detail: "this package implements interfaces owned by a dependency",
      type: "ImplementsInterface",
    });
  }
  if (creates.length) {
    items.push({
      tone: W.brandText,
      label: `${creates.length} cross-package create${creates.length > 1 ? "s" : ""}`,
      detail: "writes contracts defined by another package",
      type: "Create",
    });
  }
  if (top) {
    items.push({
      tone: W.brandText,
      label: `${top[0]} is the most-reached package`,
      detail: `${top[1]} of ${report.summary.total_interactions} interactions target it`,
      pkg: top[0],
    });
  }
  if (noSource) {
    items.push({
      tone: W.faint,
      label: `${noSource} interaction${noSource > 1 ? "s" : ""} without a source location`,
      detail: "compiled without source info — file:line is unavailable for these",
    });
  }

  if (!items.length) {
    return <p style={{ color: W.dim, fontSize: fs.body }}>No notable findings — no cross-package interactions.</p>;
  }
  return (
    <div style={{ display: "grid", gap: 8, maxWidth: 760 }}>
      {items.map((it, i) => (
        <button
          key={i}
          onClick={() => (it.pkg || it.type ? onJump({ pkg: it.pkg ?? "", type: it.type }) : undefined)}
          style={{
            textAlign: "left",
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderLeft: `3px solid ${it.tone}`,
            borderRadius: R.card,
            padding: "10px 14px",
            cursor: it.pkg || it.type ? "pointer" : "default",
          }}
        >
          <div style={{ color: W.text, fontSize: fs.data, fontWeight: 600 }}>{it.label}</div>
          <div style={{ color: W.dim, fontSize: fs.meta, marginTop: 2 }}>{it.detail}</div>
        </button>
      ))}
    </div>
  );
}

// --- Summary pivot -------------------------------------------------------

// Rows = target packages, columns = interaction types, with totals — the
// analyzer viewer's primary view. Click a row to see every finding against
// that package; click a cell to narrow to one interaction type.
function SummaryPivot({ report, onPick }: { report: AnalyzerReport; onPick: (f: Filter) => void }) {
  const [q, setQ] = useState("");
  const types = useMemo(
    () => [...new Set(report.interactions.map((i) => i.type))].sort(),
    [report],
  );
  const rows = useMemo(() => {
    const m = new Map<string, { version: string; counts: Record<string, number>; total: number }>();
    for (const it of report.interactions) {
      const e = m.get(it.target.package) ?? { version: it.target.version, counts: {}, total: 0 };
      e.counts[it.type] = (e.counts[it.type] ?? 0) + 1;
      e.total++;
      m.set(it.target.package, e);
    }
    return [...m.entries()]
      .map(([pkg, e]) => ({ pkg, ...e }))
      .filter((r) => r.pkg.toLowerCase().includes(q.trim().toLowerCase()))
      .sort((a, b) => b.total - a.total);
  }, [report, q]);

  const colTotal = (t: string) => rows.reduce((n, r) => n + (r.counts[t] ?? 0), 0);
  const grand = rows.reduce((n, r) => n + r.total, 0);

  return (
    <div>
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Filter target packages…"
        style={{
          background: W.inset,
          border: `1px solid ${W.border}`,
          borderRadius: R.control,
          color: W.text,
          padding: "5px 10px",
          fontSize: fs.data,
          marginBottom: 10,
          minWidth: 240,
        }}
      />
      <div style={{ overflowX: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: fs.data }}>
          <thead>
            <tr style={{ color: W.dim, textAlign: "left" }}>
              <th style={th}>TARGET PACKAGE</th>
              <th style={th}>VERSION</th>
              {types.map((t) => (
                <th key={t} style={{ ...th, textAlign: "right" }}>
                  {t.toUpperCase()}
                </th>
              ))}
              <th style={{ ...th, textAlign: "right" }}>TOTAL</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.pkg} style={{ borderTop: `1px solid ${W.border}` }}>
                <td style={{ ...td, cursor: "pointer" }} onClick={() => onPick({ pkg: r.pkg })}>
                  <span style={{ fontFamily: wMono, color: W.brandText }}>{r.pkg}</span>
                </td>
                <td style={{ ...td, color: W.dim, fontFamily: wMono }}>{r.version}</td>
                {types.map((t) => (
                  <td
                    key={t}
                    onClick={() => r.counts[t] && onPick({ pkg: r.pkg, type: t })}
                    style={{
                      ...td,
                      textAlign: "right",
                      fontFamily: wMono,
                      color: r.counts[t] ? W.text : W.faint,
                      cursor: r.counts[t] ? "pointer" : "default",
                    }}
                  >
                    {r.counts[t] ?? "·"}
                  </td>
                ))}
                <td style={{ ...td, textAlign: "right", fontFamily: wMono, color: W.text, fontWeight: 600 }}>
                  {r.total}
                </td>
              </tr>
            ))}
            <tr style={{ borderTop: `1px solid ${W.borderHi}` }}>
              <td style={{ ...td, color: W.dim }}>Σ total</td>
              <td style={td} />
              {types.map((t) => (
                <td key={t} style={{ ...td, textAlign: "right", fontFamily: wMono, color: W.text2 }}>
                  {colTotal(t)}
                </td>
              ))}
              <td style={{ ...td, textAlign: "right", fontFamily: wMono, color: W.text, fontWeight: 600 }}>
                {grand}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}

// --- Interactions --------------------------------------------------------

function InteractionsTable({
  report,
  filter,
  clearFilter,
}: {
  report: AnalyzerReport;
  filter: Filter;
  clearFilter: () => void;
}) {
  const rows = report.interactions.filter(
    (it) =>
      !filter ||
      ((!filter.pkg || it.target.package === filter.pkg) && (!filter.type || it.type === filter.type)),
  );
  return (
    <div>
      {filter && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <span style={{ color: W.dim, fontSize: fs.meta }}>
            Filtered to{" "}
            <span style={{ fontFamily: wMono, color: W.brandText }}>
              {filter.pkg || "all packages"}
              {filter.type ? ` · ${filter.type}` : ""}
            </span>{" "}
            — {rows.length} of {report.interactions.length}
          </span>
          <Button variant="secondary" size="sm" onClick={clearFilter}>
            Clear
          </Button>
        </div>
      )}
      <div style={{ overflowX: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: fs.data }}>
          <thead>
            <tr style={{ color: W.dim, textAlign: "left" }}>
              <th style={th}>TYPE</th>
              <th style={th}>CALLER</th>
              <th style={th}>TARGET</th>
              <th style={th}>SOURCE</th>
              <th style={th}>PACKAGE</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((it, i) => (
              <tr key={i} style={{ borderTop: `1px solid ${W.border}` }}>
                <td style={td}>
                  <span style={{ fontFamily: wMono, color: W.brandText }}>{it.type}</span>
                </td>
                <td style={{ ...td, fontFamily: wMono }}>{endpointLabel(it.caller)}</td>
                <td style={{ ...td, fontFamily: wMono }}>
                  {endpointLabel(it.target)}
                  {it.target.consuming ? <span style={{ color: W.warn }}> (consuming)</span> : null}
                </td>
                <td style={{ ...td, fontFamily: wMono, color: W.dim, fontSize: fs.meta }}>
                  {sourceLabel(it)}
                </td>
                <td style={{ ...td, color: W.dim }}>
                  <MonoId value={it.target.package_id} size={12} color={W.dim} />
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5} style={{ ...td, color: W.dim }}>
                  No interactions match this filter.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// --- Diff ----------------------------------------------------------------

// Compares two loaded reports by interaction identity, so an upgrade shows
// what a new DAR version added, dropped, or kept.
function DiffView({ current, all }: { current: Loaded; all: Loaded[] }) {
  const others = all.filter((r) => r.id !== current.id);
  const [baseId, setBaseId] = useState(others[0]?.id ?? "");
  const base = all.find((r) => r.id === baseId) ?? others[0];
  if (!base) return <p style={{ color: W.dim, fontSize: fs.body }}>Load a second DAR to compare.</p>;

  const a = new Map(base.report.interactions.map((i) => [ixKey(i), i]));
  const b = new Map(current.report.interactions.map((i) => [ixKey(i), i]));
  const added = [...b.entries()].filter(([k]) => !a.has(k)).map(([, v]) => v);
  const removed = [...a.entries()].filter(([k]) => !b.has(k)).map(([, v]) => v);
  const kept = [...b.keys()].filter((k) => a.has(k)).length;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14, flexWrap: "wrap" }}>
        <span style={{ color: W.dim, fontSize: fs.meta }}>Compare against</span>
        <select
          value={base.id}
          onChange={(e) => setBaseId(e.target.value)}
          style={{
            background: W.inset,
            border: `1px solid ${W.border}`,
            borderRadius: R.control,
            color: W.text,
            padding: "4px 8px",
            fontSize: fs.data,
            fontFamily: wMono,
          }}
        >
          {others.map((r) => (
            <option key={r.id} value={r.id}>
              {r.report.analyzed_package.name} {r.report.analyzed_package.version}
            </option>
          ))}
        </select>
        <span style={{ color: W.faint, fontSize: fs.meta, fontFamily: wMono }}>
          {base.report.analyzed_package.version} → {current.report.analyzed_package.version}
        </span>
      </div>

      <div style={{ display: "flex", gap: 24, marginBottom: 14 }}>
        <Stat label="Added" value={`+${added.length}`} />
        <Stat label="Removed" value={`-${removed.length}`} />
        <Stat label="Unchanged" value={String(kept)} />
      </div>

      {added.length === 0 && removed.length === 0 ? (
        <p style={{ color: W.dim, fontSize: fs.body }}>No cross-package interaction changes.</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: fs.data }}>
            <tbody>
              {added.map((it, i) => (
                <DiffRow key={`a${i}`} it={it} sign="+" tone={W.ok} />
              ))}
              {removed.map((it, i) => (
                <DiffRow key={`r${i}`} it={it} sign="−" tone={W.err} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function DiffRow({ it, sign, tone }: { it: AnalyzerInteraction; sign: string; tone: string }) {
  return (
    <tr style={{ borderTop: `1px solid ${W.border}` }}>
      <td style={{ ...td, width: 18, color: tone, fontFamily: wMono, fontWeight: 600 }}>{sign}</td>
      <td style={{ ...td, fontFamily: wMono, color: W.brandText }}>{it.type}</td>
      <td style={{ ...td, fontFamily: wMono }}>{endpointLabel(it.caller)}</td>
      <td style={{ ...td, fontFamily: wMono }}>
        {it.target.package}·{endpointLabel(it.target)}
      </td>
    </tr>
  );
}

// --- Graph ---------------------------------------------------------------

type TargetAgg = { pkg: string; version: string; total: number; types: string[] };

function aggregateTargets(report: AnalyzerReport): TargetAgg[] {
  const m = new Map<string, { version: string; total: number; types: Set<string> }>();
  for (const it of report.interactions) {
    const e = m.get(it.target.package) ?? { version: it.target.version, total: 0, types: new Set<string>() };
    e.total++;
    e.types.add(it.type);
    m.set(it.target.package, e);
  }
  return [...m.entries()]
    .map(([pkg, e]) => ({ pkg, version: e.version, total: e.total, types: [...e.types] }))
    .sort((a, b) => b.total - a.total);
}

function GraphView({ report }: { report: AnalyzerReport }) {
  const src = report.analyzed_package;
  const targets = aggregateTargets(report);
  if (!targets.length) {
    return <p style={{ color: W.dim, fontSize: fs.body }}>No cross-package interactions to graph.</p>;
  }
  const NW = 176;
  const NH = 48;
  const GAP = 20;
  const LX = 6;
  const RX = 384;
  const PADY = 6;
  const VBW = 574;
  const H = targets.length * NH + Math.max(0, targets.length - 1) * GAP + PADY * 2;
  const srcCY = H / 2;
  return (
    <div style={{ overflowX: "auto", border: `1px solid ${W.border}`, borderRadius: R.card, padding: "14px 12px" }}>
      <svg viewBox={`0 0 ${VBW} ${H}`} width="100%" style={{ maxWidth: VBW, display: "block" }}>
        {targets.map((t, i) => {
          const cy = PADY + i * (NH + GAP) + NH / 2;
          const x1 = LX + NW;
          const mx = (x1 + RX) / 2;
          return (
            <g key={t.pkg}>
              <path
                d={`M${x1} ${srcCY} C${mx} ${srcCY} ${mx} ${cy} ${RX} ${cy}`}
                fill="none"
                style={{ stroke: W.borderHi }}
                strokeWidth={1.5}
              />
              <text
                x={mx}
                y={(srcCY + cy) / 2 - 5}
                textAnchor="middle"
                style={{ fill: W.dim, fontFamily: wMono, fontSize: 11 }}
              >
                {t.total}×
              </text>
              <GraphNode x={RX} cy={cy} w={NW} h={NH} name={t.pkg} version={t.version} sub={t.types.join(" · ")} />
            </g>
          );
        })}
        <GraphNode x={LX} cy={srcCY} w={NW} h={NH} name={src.name} version={src.version} accent />
      </svg>
    </div>
  );
}

function GraphNode({
  x,
  cy,
  w,
  h,
  name,
  version,
  sub,
  accent,
}: {
  x: number;
  cy: number;
  w: number;
  h: number;
  name: string;
  version: string;
  sub?: string;
  accent?: boolean;
}) {
  const y = cy - h / 2;
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={h}
        rx={6}
        style={{ fill: accent ? tint(W.brand, 14) : W.surface, stroke: accent ? W.brand : W.border }}
        strokeWidth={1}
      />
      <text x={x + 12} y={y + (sub ? 20 : 28)} style={{ fill: W.text, fontFamily: wMono, fontSize: 13, fontWeight: 600 }}>
        {name} <tspan style={{ fill: W.dim, fontWeight: 400 }}>{version}</tspan>
      </text>
      {sub && (
        <text x={x + 12} y={y + 36} style={{ fill: W.faint, fontSize: 10 }}>
          {sub.length > 30 ? sub.slice(0, 29) + "…" : sub}
        </text>
      )}
    </g>
  );
}

// --- helpers -------------------------------------------------------------

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ ...tableCaps, color: W.faint, fontSize: fs.label }}>{label}</div>
      <div style={{ fontFamily: wMono, fontSize: fs.stat, color: W.text, lineHeight: 1.1 }}>{value}</div>
    </div>
  );
}

// caller reads as module.choice/template; target as module.interface/template.
function endpointLabel(e: AnalyzerEndpoint): string {
  const leaf = e.choice || e.interface || e.template;
  return leaf ? `${e.module}.${leaf}` : e.module;
}

// file:line when the package carries source info, else a muted dash.
function sourceLabel(it: AnalyzerInteraction): string {
  const s = it.source;
  if (!s?.file) return "·";
  return s.start_line ? `${s.file}:${s.start_line}` : s.file;
}

// Stable identity for diffing: same call from the same caller to the same
// target is the "same" interaction across versions.
function ixKey(it: AnalyzerInteraction): string {
  return [it.type, endpointLabel(it.caller), it.target.package, endpointLabel(it.target)].join("|");
}

function downloadReport(loaded: Loaded) {
  const blob = new Blob([JSON.stringify(loaded.report, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${loaded.report.analyzed_package.name}-${loaded.report.analyzed_package.version}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

const th: React.CSSProperties = { ...tableCaps, fontSize: fs.label, padding: "6px 10px 6px 0" };
const td: React.CSSProperties = { padding: "6px 10px 6px 0", color: W.text2, verticalAlign: "top" };

function uploadStyle(disabled: boolean): React.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    background: W.brand,
    color: W.onAccent,
    borderRadius: R.control,
    padding: "6px 14px",
    fontSize: fs.data,
    fontWeight: 600,
    cursor: disabled ? "default" : "pointer",
    opacity: disabled ? 0.6 : 1,
  };
}
