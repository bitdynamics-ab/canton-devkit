// Web UI design tokens — the Canton Infrastructure Design System's
// dark console theme (consoles default dark). Single source of truth
// so every screen pulls from the same palette and a design-system
// change is a one-line update here.
//
// The system uses cool ink neutrals with ONE interactive accent
// (cobalt); teal is a data accent only — series, parties, throughput —
// never buttons or links. Status hues are desaturated. Structure comes
// from 1px hairlines, not shadows.
export const W = {
  bg: "#0B0F1A", // bg-page
  surface: "#10151F", // bg-surface — cards, sidebars, inputs
  surface2: "#161C29", // bg-raised — menus, hovered rows in menus
  border: "#232B3D", // border-default
  borderHi: "#313B52", // border-strong — hover borders
  text: "#E9ECF4", // text-primary
  text2: "#A9B2C6", // text-secondary
  dim: "#7C8598", // text-muted
  faint: "#5A6375", // text-faint
  brand: "#6480E6", // accent (cobalt) — buttons, tabs, active nav
  brandSoft: "#141C36", // accent-subtle — active-nav fill, selection
  brandText: "#93A7F0", // accent-text — accent-colored text
  ok: "#7CC89A", // ok-text
  warn: "#DDB25E", // warn-text
  err: "#E08D7D", // danger-text
  info: "#8FA3EE", // info-text / links
  mag: "#93A7F0", // series accent (cobalt-light)
  rose: "#7BD2C6", // series accent (teal — data only)
  amber: "#C8971F", // series accent (deep amber)
  card: "#10151F", // bg-surface
  rowHover: "#171E2C", // hover-tint
  selRow: "#1E2637", // active-tint

  // CDS-specific roles beyond the original palette.
  sunken: "#080C15", // bg-sunken — nav rail, card footers
  inset: "#0D1220", // bg-inset — wells, disabled fields
  onAccent: "#0B0F1A", // text on accent-filled controls
  accentHover: "#7B93EC",
  accentActive: "#93A7F0",
  teal: "#7BD2C6", // data accent — throughput, parties
  tealDeep: "#189E8C", // dense data accent — log sources
  okBg: "#0E1F16",
  okBorder: "#1E3A2A",
  okIcon: "#4FAE76",
  warnBg: "#211A0B",
  warnBorder: "#3E3115",
  warnIcon: "#C8971F",
  errBg: "#24120E",
  errBorder: "#45201A",
  errIcon: "#D2604B",
  infoBg: "#121A33",
  infoBorder: "#223059",
  focus: "#3D5BDC", // 2px focus outline — identical in both themes
} as const;

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

// Wide structural caps — the brand's label voice for STRUCTURE:
// the wordmark, section headers, stat-card labels. Spread where a
// style object is built.
export const wideCaps = {
  fontWeight: 600,
  fontStretch: "118%",
  letterSpacing: "0.08em",
  textTransform: "uppercase",
} as const;

// Quiet caps for data-table column headers. Uppercase is the table
// convention, but headers repeat on every table — at 118% width and
// heavy tracking they read as brand moments instead of chrome, so
// tables get the toned-down cut.
export const tableCaps = {
  fontWeight: 500,
  letterSpacing: "0.05em",
  textTransform: "uppercase",
} as const;

// Role-to-color map shared by every screen so a role addition or
// palette swap is a one-line change. Parties are data → cobalt /
// teal / amber triad from the dataviz ramp.
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
