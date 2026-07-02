// Web UI design tokens — mirrors the W object in
// docs/design/mockups/webui-shell.jsx. Single source of truth so
// every screen pulls from the same palette and a designer-driven
// change to the mockup is a one-line update here.
//
// Keep field names identical to the JSX so cross-referencing the
// mockups during reviews stays one-to-one.
export const W = {
  bg: "#0B0E13",
  surface: "#11151D",
  surface2: "#161C25",
  border: "#1F2731",
  borderHi: "#2A3441",
  text: "#E6E9EE",
  text2: "#B5BCC7",
  dim: "#7C8593",
  faint: "#4A5260",
  brand: "#5BD7C5",
  brandSoft: "#1F4A47",
  brandText: "#7FE9D9",
  ok: "#62E2A0",
  warn: "#F5BF55",
  err: "#F47C7C",
  info: "#7CB5F7",
  mag: "#C4A8F5",
  rose: "#F08FB5",
  amber: "#E8A14E",
  card: "#10151D",
  rowHover: "#161D26",
  selRow: "#162226",
} as const;

export const wMono =
  "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace";
export const wSans =
  "'IBM Plex Sans', 'Inter Tight', system-ui, sans-serif";

// Role-to-color map shared by every screen so a role addition or
// palette swap is a one-line change.
export const ROLE_COLOR: Record<"app-user" | "app-provider" | "sv", string> = {
  "app-user": W.info,
  "app-provider": W.brand,
  sv: W.mag,
};

// Transaction-kind palette used by the Explorer Timeline + table.
// Single source of truth so table and strip never disagree.
export const TX_KIND_COLOR: Record<
  "transaction" | "reassignment" | "topology" | "checkpoint",
  string
> = {
  transaction: W.brand,
  reassignment: W.info,
  topology: W.mag,
  checkpoint: W.dim,
};
