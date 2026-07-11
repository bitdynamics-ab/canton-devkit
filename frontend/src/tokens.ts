// Design tokens. Each semantic color resolves through a CSS variable in
// index.css (:root dark, :root[data-theme="light"] light), so one W.*
// reference renders correctly in both themes.
export const W = {
  bg: "var(--bg-page)",
  surface: "var(--bg-surface)", // cards, sidebars, inputs
  surface2: "var(--bg-raised)", // menus, raised rows
  border: "var(--border-default)",
  borderHi: "var(--border-strong)", // hover borders
  text: "var(--text-primary)",
  text2: "var(--text-secondary)",
  dim: "var(--text-muted)",
  faint: "var(--text-faint)",
  brand: "var(--accent)", // buttons, tabs, active nav
  brandSoft: "var(--accent-subtle)", // active-nav fill, selection
  brandText: "var(--accent-text)",
  ok: "var(--ok-text)",
  warn: "var(--warn-text)",
  err: "var(--danger-text)",
  info: "var(--info-text)", // status/info + links
  mag: "#93A7F0", // series accent (data)
  rose: "#7BD2C6", // series accent (teal — data only)
  amber: "#C8971F", // series accent (deep amber — data)
  card: "var(--bg-surface)",
  rowHover: "var(--hover-tint)",
  selRow: "var(--active-tint)",

  sunken: "var(--bg-sunken)", // nav rail, card footers
  inset: "var(--bg-inset)", // wells, disabled fields
  onAccent: "var(--on-accent)", // text on accent-filled controls
  onAccentSolid: "var(--on-accent-solid)", // text on the solid CTA fill
  accentSolid: "var(--accent-solid)", // the primary-button fill (both themes)
  accentSolidHover: "var(--accent-solid-hover)",
  accentHover: "var(--accent-hover)",
  accentActive: "var(--accent-active)",
  teal: "#7BD2C6", // data accent — throughput, parties
  tealDeep: "#189E8C", // dense data accent — log sources
  okBg: "var(--ok-bg)",
  okBorder: "var(--ok-border)",
  okIcon: "var(--ok-text)",
  warnBg: "var(--warn-bg)",
  warnBorder: "var(--warn-border)",
  warnIcon: "var(--warn-text)",
  errBg: "var(--danger-bg)",
  errBorder: "var(--danger-border)",
  errIcon: "var(--danger-text)",
  infoBg: "var(--info-bg)",
  infoBorder: "var(--info-border)",
  focus: "var(--blue-500)", // 2px focus outline — identical in both themes
} as const;

// W.x is a CSS var, so `${W.x}1A` hex-alpha concat is invalid; color-mix
// over transparent is the equivalent.
export function tint(color: string, pct: number): string {
  return `color-mix(in srgb, ${color} ${pct}%, transparent)`;
}

export const wMono =
  "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace";
export const wSans =
  "'Archivo', -apple-system, 'Segoe UI', 'Helvetica Neue', Arial, sans-serif";

export const R = { control: 2, card: 4, dialog: 8 } as const;

export const EASE = "cubic-bezier(0.2, 0.6, 0.2, 1)";
export const FAST = "120ms";

export const wideCaps = {
  fontWeight: 600,
  fontStretch: "118%",
  letterSpacing: "0.08em",
  textTransform: "uppercase",
} as const;

// Quieter caps for data-table column headers.
export const tableCaps = {
  fontWeight: 500,
  letterSpacing: "0.05em",
  textTransform: "uppercase",
} as const;

export const ROLE_COLOR: Record<"app-user" | "app-provider" | "sv", string> = {
  "app-user": W.brand,
  "app-provider": W.teal,
  sv: W.warn,
};

// Shared by the Explorer Timeline + table so they never disagree.
export const TX_KIND_COLOR: Record<
  "transaction" | "reassignment" | "topology" | "checkpoint",
  string
> = {
  transaction: W.brand,
  reassignment: W.teal,
  topology: W.warn,
  checkpoint: W.dim,
};

// Type scale — the Canton Design System ramp, shared with the docs site
// (website/src/styles/custom.css). Values are rem so the Web UI respects the
// reader's browser / OS font-size preference, exactly like the docs site does.
// At the default 16px root these render pixel-identical to the raw ramp
// (11 · 13 · 16 · 18 · 22 · 28).
export const fs = {
  caption: "0.6875rem", // 11px — meta, timestamps, chart micro-labels
  small: "0.8125rem",   // 13px — labels, nav, code, dense cells, mono id chips
  body: "1rem",         // 16px — default reading size
  h3: "1.125rem",       // 18px — subsection
  h2: "1.375rem",       // 22px — section
  h1: "1.75rem",        // 28px — page title / display
} as const;
