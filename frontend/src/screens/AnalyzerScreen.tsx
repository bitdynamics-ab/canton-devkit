import { useEffect, useState } from "react";
import {
  fetchAnalyzerStatus,
  fetchDARList,
  analyzeDeployedDar,
  analyzeUploadedDar,
  ApiError,
  type Role,
  type AnalyzerStatusResponse,
  type AnalyzerReport,
  type AnalyzerEndpoint,
  type DARRow,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono, tableCaps, R, tint, fs } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";

// Cross-package interaction analysis (Certora daml-analyzer). Analyze a
// DAR already deployed to the selected instance, or a .dar file uploaded
// ad hoc. The whole surface is gated on the analyzer being available
// (Docker + image), so a missing runtime reads as a clean notice.

const ROLES: Role[] = ["app-user", "app-provider", "sv"];

type Loaded = { darName: string; report: AnalyzerReport };

export function AnalyzerScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [status, setStatus] = useState<AnalyzerStatusResponse | null>(null);
  const [dars, setDars] = useState<DARRow[]>([]);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // package id / "upload" in flight
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

  async function run(what: string, p: Promise<{ dar_name?: string; report: AnalyzerReport | null }>) {
    setBusy(what);
    setErr(null);
    try {
      const resp = await p;
      if (resp.report) setLoaded({ darName: resp.dar_name ?? "", report: resp.report });
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

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

      {gated && <NotConfigured status={status} />}

      {!gated && (
        <>
          <div style={{ display: "flex", alignItems: "center", gap: 12, margin: "18px 0", flexWrap: "wrap" }}>
            <label style={{ ...uploadStyle(!!busy) }}>
              {busy === "upload" ? "Analyzing…" : "Analyze a .dar file"}
              <input
                type="file"
                accept=".dar"
                disabled={!!busy}
                style={{ display: "none" }}
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) run("upload", analyzeUploadedDar(f));
                  e.target.value = "";
                }}
              />
            </label>
            {status?.source && (
              <span style={{ color: W.dim, fontSize: fs.meta, fontFamily: wMono }}>
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
                    onClick={() => run(d.main, analyzeDeployedDar(name, d.main, role))}
                  >
                    {busy === d.main ? "Analyzing…" : `${d.name} ${d.version}`}
                  </Button>
                ))}
              </div>
            </div>
          )}

          {err && (
            <div style={{ color: W.err, fontSize: fs.body, margin: "12px 0" }}>{err}</div>
          )}

          {loaded && <ReportView darName={loaded.darName} report={loaded.report} />}

          {!loaded && !err && !busy && (
            <p style={{ color: W.dim, fontSize: fs.body }}>
              Pick a deployed DAR above{name ? "" : " (select an instance)"} or upload a .dar to see its
              cross-package interactions.
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

function ReportView({ darName, report }: { darName: string; report: AnalyzerReport }) {
  const p = report.analyzed_package;
  const s = report.summary;
  const [view, setView] = useState<"table" | "graph">("table");
  return (
    <div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10, flexWrap: "wrap", marginBottom: 4 }}>
        <h3 style={{ margin: 0, color: W.text }}>{p.name}</h3>
        <span style={{ color: W.dim, fontFamily: wMono, fontSize: fs.meta }}>
          {p.version} · LF {p.lf_version ?? "?"}
        </span>
        {darName && <span style={{ color: W.faint, fontSize: fs.meta }}>{darName}</span>}
      </div>

      <div style={{ display: "flex", gap: 20, flexWrap: "wrap", margin: "12px 0", alignItems: "baseline" }}>
        <Stat label="Interactions" value={String(s.total_interactions)} />
        <Stat label="Dependencies" value={String(report.dependencies.length)} />
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
          {Object.entries(s.by_type).map(([k, n]) => (
            <span
              key={k}
              style={{
                background: tint(W.brand, 12),
                color: W.brandText,
                borderRadius: R.control,
                padding: "2px 8px",
                fontSize: fs.label,
                fontFamily: wMono,
              }}
            >
              {k} {n}
            </span>
          ))}
        </div>
      </div>

      {s.total_interactions > 0 && (
        <div style={{ display: "flex", gap: 4, margin: "6px 0 14px" }}>
          {(["table", "graph"] as const).map((v) => (
            <button
              key={v}
              onClick={() => setView(v)}
              style={{
                background: view === v ? W.brand : "transparent",
                color: view === v ? W.onAccent : W.dim,
                border: `1px solid ${view === v ? W.brand : W.border}`,
                borderRadius: R.control,
                padding: "3px 12px",
                fontSize: fs.label,
                textTransform: "capitalize",
                cursor: "pointer",
              }}
            >
              {v}
            </button>
          ))}
        </div>
      )}

      {s.total_interactions === 0 ? (
        <p style={{ color: W.dim, fontSize: fs.body }}>No cross-package interactions.</p>
      ) : view === "graph" ? (
        <GraphView report={report} />
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: fs.data }}>
          <thead>
            <tr style={{ color: W.dim, textAlign: "left" }}>
              <th style={th}>TYPE</th>
              <th style={th}>CALLER</th>
              <th style={th}>TARGET</th>
              <th style={th}>PACKAGE</th>
            </tr>
          </thead>
          <tbody>
            {report.interactions.map((it, i) => (
              <tr key={i} style={{ borderTop: `1px solid ${W.border}` }}>
                <td style={td}>
                  <span style={{ fontFamily: wMono, color: W.brandText }}>{it.type}</span>
                </td>
                <td style={{ ...td, fontFamily: wMono }}>{endpointLabel(it.caller)}</td>
                <td style={{ ...td, fontFamily: wMono }}>
                  {endpointLabel(it.target)}
                  {it.target.consuming ? <span style={{ color: W.warn }}> (consuming)</span> : null}
                </td>
                <td style={{ ...td, color: W.dim }}>
                  <MonoId value={it.target.package_id} size={12} color={W.dim} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ ...tableCaps, color: W.faint, fontSize: fs.label }}>{label}</div>
      <div style={{ fontFamily: wMono, fontSize: fs.stat, color: W.text, lineHeight: 1.1 }}>{value}</div>
    </div>
  );
}

// GraphView is a native package-interaction graph: the analyzed package on
// the left, each dependency it reaches on the right, edges labeled with the
// interaction count — the same shape the analyzer's DOT output draws, built
// from the JSON so it needs no graphviz/cytoscape.
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

// caller reads as module.choice/template; target as module.interface/template.
function endpointLabel(e: AnalyzerEndpoint): string {
  const leaf = e.choice || e.interface || e.template;
  return leaf ? `${e.module}.${leaf}` : e.module;
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
