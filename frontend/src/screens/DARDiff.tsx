// DAR structural diff viewer. Renders /api/instances/:name/dar/diff
// between two DARs as expandable sections: modules / templates /
// interfaces added/removed/changed. No third-party diff library — the
// JSON shape is small enough that a hand-rolled list-with-colour reads
// cleanly. Embedded as a drawer inside DARScreen when the user picks
// two DARs to compare.
import { useEffect, useState } from "react";
import {
  fetchDARDiff,
  type DARDiffResponse,
  type Role,
} from "../api";
import { W, wMono } from "../tokens";
import {
  IcArrowRight,
  IcChevronDown,
  IcChevronRight,
} from "../components/icons";

interface Props {
  instance: string;
  a: string;
  b: string;
  role: Role;
}

export function DARDiff({ instance, a, b, role }: Props) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: DARDiffResponse }
    | { kind: "err"; msg: string }
  >({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchDARDiff(instance, a, b, role)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setState({
            kind: "err",
            msg: e instanceof Error ? e.message : "diff failed",
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [instance, a, b, role]);

  if (state.kind === "loading") {
    return <div style={paneStyle}>Computing diff…</div>;
  }
  if (state.kind === "err") {
    return (
      <div style={{ ...paneStyle, color: W.err }}>
        Diff failed: {state.msg}
      </div>
    );
  }
  const d = state.data;
  const totalDelta =
    d.modules_added.length +
    d.modules_removed.length +
    d.templates_added.length +
    d.templates_removed.length +
    d.templates_changed.length +
    d.interfaces_added.length +
    d.interfaces_removed.length +
    d.interfaces_changed.length;
  return (
    <div style={paneStyle} data-testid="dar-diff">
      <header style={{ marginBottom: 10 }}>
        <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
          <Side label="a" side={d.a ?? null} />
          <span
            style={{
              color: W.dim,
              alignSelf: "center",
              display: "inline-flex",
              alignItems: "center",
            }}
          >
            <IcArrowRight size={12} />
          </span>
          <Side label="b" side={d.b ?? null} />
        </div>
        <div style={{ color: W.dim, fontSize: 11, marginTop: 6 }}>
          {totalDelta === 0
            ? "no structural changes"
            : `${totalDelta} structural change${totalDelta === 1 ? "" : "s"}`}
        </div>
      </header>
      <Section title="Modules added" tone="add" items={d.modules_added}>
        {(item) => <code style={mono}>{item}</code>}
      </Section>
      <Section title="Modules removed" tone="rm" items={d.modules_removed}>
        {(item) => <code style={mono}>{item}</code>}
      </Section>
      <Section title="Templates added" tone="add" items={d.templates_added}>
        {(t) => (
          <>
            <code style={mono}>
              {t.module}:{t.name}
            </code>
            {t.choices && t.choices.length > 0 && (
              <ChipGroup labels={t.choices} tone="add" />
            )}
          </>
        )}
      </Section>
      <Section title="Templates removed" tone="rm" items={d.templates_removed}>
        {(t) => (
          <>
            <code style={mono}>
              {t.module}:{t.name}
            </code>
            {t.choices && t.choices.length > 0 && (
              <ChipGroup labels={t.choices} tone="rm" />
            )}
          </>
        )}
      </Section>
      <Section title="Templates changed" tone="chg" items={d.templates_changed}>
        {(t) => (
          <>
            <code style={mono}>
              {t.module}:{t.name}
            </code>
            {t.choices_added.length > 0 && (
              <ChipGroup label="+choices" labels={t.choices_added} tone="add" />
            )}
            {t.choices_removed.length > 0 && (
              <ChipGroup label="−choices" labels={t.choices_removed} tone="rm" />
            )}
          </>
        )}
      </Section>
      <Section title="Interfaces added" tone="add" items={d.interfaces_added}>
        {(i) => (
          <>
            <code style={mono}>
              {i.module}:{i.name}
            </code>
            {i.choices && i.choices.length > 0 && (
              <ChipGroup labels={i.choices} tone="add" />
            )}
            {i.methods && i.methods.length > 0 && (
              <ChipGroup label="methods" labels={i.methods} tone="info" />
            )}
          </>
        )}
      </Section>
      <Section title="Interfaces removed" tone="rm" items={d.interfaces_removed}>
        {(i) => (
          <>
            <code style={mono}>
              {i.module}:{i.name}
            </code>
            {i.choices && i.choices.length > 0 && (
              <ChipGroup labels={i.choices} tone="rm" />
            )}
            {i.methods && i.methods.length > 0 && (
              <ChipGroup label="methods" labels={i.methods} tone="info" />
            )}
          </>
        )}
      </Section>
      <Section
        title="Interfaces changed"
        tone="chg"
        items={d.interfaces_changed}
      >
        {(i) => (
          <>
            <code style={mono}>
              {i.module}:{i.name}
            </code>
            {i.choices_added.length > 0 && (
              <ChipGroup label="+choices" labels={i.choices_added} tone="add" />
            )}
            {i.choices_removed.length > 0 && (
              <ChipGroup label="−choices" labels={i.choices_removed} tone="rm" />
            )}
            {i.methods_added.length > 0 && (
              <ChipGroup label="+methods" labels={i.methods_added} tone="add" />
            )}
            {i.methods_removed.length > 0 && (
              <ChipGroup label="−methods" labels={i.methods_removed} tone="rm" />
            )}
          </>
        )}
      </Section>
    </div>
  );
}

const paneStyle: React.CSSProperties = {
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: 4,
  padding: 12,
  fontSize: 12,
  maxHeight: "60vh",
  overflowY: "auto",
};

const mono: React.CSSProperties = {
  fontFamily: wMono,
  color: W.text,
  fontSize: 11.5,
};

function Side({
  label,
  side,
}: {
  label: string;
  side: { main: string; name: string; version: string } | null;
}) {
  if (!side) {
    return (
      <span style={{ color: W.dim, fontSize: 11 }}>
        {label}: <code style={mono}>unknown</code>
      </span>
    );
  }
  return (
    <span style={{ fontSize: 11 }}>
      <span style={{ color: W.dim }}>{label}: </span>
      <code style={mono}>
        {side.name}@{side.version}
      </code>
      <span style={{ color: W.dim, marginLeft: 6 }}>
        {side.main.slice(0, 8)}…
      </span>
    </span>
  );
}

type Tone = "add" | "rm" | "chg" | "info";

function toneColour(t: Tone): { bg: string; fg: string } {
  switch (t) {
    case "add":
      return { bg: "#7CC89A22", fg: "#7CC89A" };
    case "rm":
      return { bg: `${W.err}22`, fg: W.err };
    case "chg":
      return { bg: `${W.warn}22`, fg: W.warn };
    case "info":
      return { bg: `${W.brand}1A`, fg: W.brand };
  }
}

function Section<T>({
  title,
  items,
  tone,
  children,
}: {
  title: string;
  items: T[];
  tone: Tone;
  children: (item: T) => React.ReactNode;
}) {
  const [open, setOpen] = useState(items.length > 0 && items.length < 12);
  if (items.length === 0) return null;
  const c = toneColour(tone);
  return (
    <div style={{ marginBottom: 10 }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        style={{
          background: "transparent",
          border: "none",
          color: c.fg,
          fontSize: 11.5,
          fontWeight: 600,
          cursor: "pointer",
          padding: "2px 0",
          letterSpacing: 0.6,
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
        }}
      >
        {open ? <IcChevronDown size={12} /> : <IcChevronRight size={12} />}
        <span>
          {title} ({items.length})
        </span>
      </button>
      {open && (
        <div style={{ marginLeft: 14, marginTop: 4 }}>
          {items.map((item, idx) => (
            <div
              key={idx}
              style={{
                display: "flex",
                gap: 6,
                alignItems: "center",
                padding: "2px 0",
                flexWrap: "wrap",
              }}
            >
              {children(item)}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ChipGroup({
  label,
  labels,
  tone,
}: {
  label?: string;
  labels: string[];
  tone: Tone;
}) {
  const c = toneColour(tone);
  return (
    <span style={{ display: "inline-flex", flexWrap: "wrap", alignItems: "center", gap: 3 }}>
      {label && (
        <span style={{ color: W.dim, fontSize: 10.5, marginLeft: 6 }}>{label}:</span>
      )}
      {labels.map((l) => (
        <span
          key={l}
          style={{
            padding: "0 5px",
            borderRadius: 2,
            background: c.bg,
            color: c.fg,
            fontSize: 10.5,
            fontFamily: wMono,
          }}
        >
          {l}
        </span>
      ))}
    </span>
  );
}
