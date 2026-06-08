// BIT-230 #1 — DAR package-tree explorer.
//
// Renders a /api/instances/:name/dar/:id/inspect response as an
// expandable tree: package → module → (template | interface | data
// type). Choices and methods are leaf-level nodes shown as inline
// chips.
//
// The component is deliberately self-contained — it fetches its own
// data, owns its expand/collapse state, and renders without any
// shared layout primitive. Embedded as a drawer inside DARScreen.
import { useEffect, useState } from "react";
import {
  fetchDARInspect,
  type DARInspectResponse,
  type DARModuleContents,
  type DARPackageInspect,
  type Role,
} from "../api";
import { W, wMono } from "../tokens";

interface Props {
  instance: string;
  mainID: string;
  role: Role;
}

export function DARPackageTree({ instance, mainID, role }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: DARInspectResponse }
    | { kind: "err"; msg: string }
  >({ kind: "loading" });
  // Expanded package ids — start with the main package expanded so
  // the most useful tree is visible on first render.
  const [expandedPkgs, setExpandedPkgs] = useState<Set<string>>(new Set());
  const [expandedModules, setExpandedModules] = useState<Set<string>>(
    new Set(),
  );

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchDARInspect(instance, mainID, role)
      .then((data) => {
        if (cancelled) return;
        setState({ kind: "ok", data });
        // Auto-expand the main package.
        const main = data.packages.find((p) => p.is_main);
        if (main) setExpandedPkgs(new Set([main.package_id]));
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setState({
          kind: "err",
          msg: e instanceof Error ? e.message : "inspect failed",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [instance, mainID, role]);

  if (state.kind === "loading") {
    return <div style={paneStyle}>Loading package tree…</div>;
  }
  if (state.kind === "err") {
    return (
      <div style={{ ...paneStyle, color: W.err }}>
        Inspect failed: {state.msg}
      </div>
    );
  }

  const togglePkg = (id: string) => {
    setExpandedPkgs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const toggleMod = (key: string) => {
    setExpandedModules((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  return (
    <div style={paneStyle} data-testid="dar-package-tree">
      <div style={{ color: W.dim, fontSize: 11.5, marginBottom: 8 }}>
        {state.data.packages.length} package
        {state.data.packages.length === 1 ? "" : "s"} · sha256{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>
          {state.data.sha256.slice(0, 12)}…
        </code>
      </div>
      {state.data.packages.map((pkg) => (
        <PackageNode
          key={pkg.package_id}
          pkg={pkg}
          expanded={expandedPkgs.has(pkg.package_id)}
          onToggle={() => togglePkg(pkg.package_id)}
          moduleExpanded={expandedModules}
          onToggleModule={toggleMod}
        />
      ))}
    </div>
  );
}

const paneStyle: React.CSSProperties = {
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: 8,
  padding: 12,
  fontSize: 12,
  maxHeight: "60vh",
  overflowY: "auto",
};

function PackageNode({
  pkg,
  expanded,
  onToggle,
  moduleExpanded,
  onToggleModule,
}: {
  pkg: DARPackageInspect;
  expanded: boolean;
  onToggle: () => void;
  moduleExpanded: Set<string>;
  onToggleModule: (key: string) => void;
}) {
  const modules = pkg.contents?.modules ?? [];
  return (
    <div style={{ marginBottom: 6 }}>
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        style={treeRowStyle(pkg.is_main)}
      >
        <span style={{ width: 14, display: "inline-block" }}>
          {modules.length === 0 ? "·" : expanded ? "▾" : "▸"}
        </span>
        <span style={{ fontFamily: wMono, color: pkg.is_main ? W.brand : W.text }}>
          {pkg.name || pkg.package_id.slice(0, 12)}
        </span>
        {pkg.version && (
          <span style={{ fontFamily: wMono, color: W.dim, marginLeft: 6 }}>
            {pkg.version}
          </span>
        )}
        <span style={{ fontFamily: wMono, color: W.dim, marginLeft: 8, fontSize: 10.5 }}>
          {pkg.lf_version} · {pkg.package_id.slice(0, 10)}…
        </span>
      </button>
      {expanded &&
        modules.map((mod) => {
          const key = pkg.package_id + ":" + mod.name;
          return (
            <ModuleNode
              key={key}
              mod={mod}
              expanded={moduleExpanded.has(key)}
              onToggle={() => onToggleModule(key)}
            />
          );
        })}
    </div>
  );
}

function ModuleNode({
  mod,
  expanded,
  onToggle,
}: {
  mod: DARModuleContents;
  expanded: boolean;
  onToggle: () => void;
}) {
  const tplCount = mod.templates?.length ?? 0;
  const ifCount = mod.interfaces?.length ?? 0;
  const dtCount = mod.data_types?.length ?? 0;
  const total = tplCount + ifCount + dtCount;
  return (
    <div style={{ marginLeft: 18, marginTop: 3 }}>
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        style={treeRowStyle(false)}
      >
        <span style={{ width: 14, display: "inline-block" }}>
          {total === 0 ? "·" : expanded ? "▾" : "▸"}
        </span>
        <span style={{ fontFamily: wMono, color: W.text }}>{mod.name}</span>
        <span style={{ marginLeft: 8, color: W.dim, fontSize: 10.5 }}>
          {tplCount}T · {ifCount}I · {dtCount}D
        </span>
      </button>
      {expanded && (
        <div style={{ marginLeft: 28, marginTop: 3 }}>
          {(mod.templates ?? []).map((t) => (
            <div key={"t-" + t.name} style={leafRow}>
              <span style={{ color: "#7BB7FF", fontFamily: wMono }}>template</span>{" "}
              <span style={{ color: W.text, fontFamily: wMono }}>{t.name}</span>
              {t.choices && t.choices.length > 0 && (
                <span style={{ marginLeft: 8 }}>
                  {t.choices.map((c) => (
                    <Chip key={c} label={c} kind="choice" />
                  ))}
                </span>
              )}
            </div>
          ))}
          {(mod.interfaces ?? []).map((i) => (
            <div key={"i-" + i.name} style={leafRow}>
              <span style={{ color: "#C4A8F5", fontFamily: wMono }}>interface</span>{" "}
              <span style={{ color: W.text, fontFamily: wMono }}>{i.name}</span>
              {i.choices && i.choices.length > 0 && (
                <span style={{ marginLeft: 8 }}>
                  {i.choices.map((c) => (
                    <Chip key={c} label={c} kind="choice" />
                  ))}
                </span>
              )}
              {i.methods && i.methods.length > 0 && (
                <span style={{ marginLeft: 6 }}>
                  {i.methods.map((m) => (
                    <Chip key={m} label={m} kind="method" />
                  ))}
                </span>
              )}
            </div>
          ))}
          {(mod.data_types ?? []).map((dt) => (
            <div key={"d-" + dt} style={leafRow}>
              <span style={{ color: "#62E2A0", fontFamily: wMono }}>data</span>{" "}
              <span style={{ color: W.text2, fontFamily: wMono }}>{dt}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function treeRowStyle(highlight: boolean): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    background: "transparent",
    border: "none",
    padding: "3px 6px",
    fontSize: 12,
    fontFamily: "inherit",
    color: highlight ? W.brand : W.text,
    cursor: "pointer",
    textAlign: "left",
    width: "100%",
  };
}

const leafRow: React.CSSProperties = {
  padding: "2px 6px",
  fontSize: 11.5,
  color: W.text2,
};

function Chip({
  label,
  kind,
}: {
  label: string;
  kind: "choice" | "method";
}) {
  const tone =
    kind === "choice" ? { bg: `${W.brand}1A`, fg: W.brand } : { bg: "#7BB7FF22", fg: "#7BB7FF" };
  return (
    <span
      style={{
        display: "inline-block",
        padding: "0 6px",
        marginRight: 4,
        marginTop: 2,
        borderRadius: 4,
        background: tone.bg,
        color: tone.fg,
        fontSize: 10.5,
        fontFamily: wMono,
      }}
    >
      {label}
    </span>
  );
}
