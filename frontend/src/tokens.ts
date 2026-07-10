// Web UI design tokens — the Canton Infrastructure Design System.
//
// Every semantic color resolves through a CSS variable defined in
// index.css under :root (dark) and :root[data-theme="light"], so the
// same W.* reference renders correctly in both themes. Structure comes
// from 1px hairlines, not shadows; one interactive accent; teal/amber
// are data-only accents (series, parties, throughput) held at fixed
// mid-tones legible on either background.
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

  // CDS roles.
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

// Translucent tint of a themed color. W.x is a CSS var, so a `${W.x}1A`
// hex-alpha concat is invalid; color-mix over transparent is the
// equivalent.
export function tint(color: string, pct: number): string {
  return `color-mix(in srgb, ${color} ${pct}%, transparent)`;
}

export const wMono =
  "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace";
export const wSans =
  "'Archivo', -apple-system, 'Segoe UI', 'Helvetica Neue', Arial, sans-serif";

// Radius language: 2 controls / 4 cards / 8 dialogs. Full radius is
// reserved for status dots.
export const R = { control: 2, card: 4, dialog: 8 } as const;

// Motion: quick and damped, no bounce.
export const EASE = "cubic-bezier(0.2, 0.6, 0.2, 1)";
export const FAST = "120ms";

// Wide structural caps for the wordmark, section headers, and stat-card
// labels.
export const wideCaps = {
  fontWeight: 600,
  fontStretch: "118%",
  letterSpacing: "0.08em",
  textTransform: "uppercase",
} as const;

// Quieter caps for data-table column headers — the wide structural cut
// repeats on every table and reads as chrome, so tables tone it down.
export const tableCaps = {
  fontWeight: 500,
  letterSpacing: "0.05em",
  textTransform: "uppercase",
} as const;

// Role-to-color map shared by every screen. Parties are data: the
// accent / teal / amber triad from the dataviz ramp.
export const ROLE_COLOR: Record<"app-user" | "app-provider" | "sv", string> = {
  "app-user": W.brand,
  "app-provider": W.teal,
  sv: W.warn,
};

// Transaction-kind palette used by the Explorer Timeline + table.
// Single source of truth so table and strip never disagree.
export const TX_KIND_COLOR: Record<
  "transaction" | "reassignment" | "topology" | "checkpoint",
  string
> = {
  transaction: W.brand,
  reassignment: W.teal,
  topology: W.warn,
  checkpoint: W.dim,
};
