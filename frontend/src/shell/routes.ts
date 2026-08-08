// Shared route table: single source of truth for the left-nav order.
// Both Shell.tsx (sidebar NavLinks) and CommandPalette.tsx (⌘K
// navigation) read from this list so they can't drift on routes or labels.

export interface NavEntry {
  to: string;
  label: string;
}

export const NAV: readonly NavEntry[] = [
  { to: "/", label: "Overview" },
  { to: "/doctor", label: "Doctor" },
  { to: "/wallet", label: "Wallet" },
  { to: "/explorer", label: "Explorer" },
  { to: "/dar", label: "DAR Manager" },
  { to: "/metrics", label: "Metrics" },
  { to: "/tokens", label: "Tokens" },
  { to: "/agent", label: "Agent Skills" },
];

// The URL owns the current instance selection, so every in-app navigation
// carries it, including host-level screens such as Doctor that do not read
// it themselves. Dropping it there would make the next instance operation
// silently auto-pick another LocalNet. The encodeURIComponent guard is cheap
// insurance if the instance-name rules ever widen.
export function linkTo(to: string, instance: string | null): string {
  return instance
    ? `${to}?instance=${encodeURIComponent(instance)}`
    : to;
}
