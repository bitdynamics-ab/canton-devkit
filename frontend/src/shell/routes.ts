// Shared sidebar / palette route table. Single source of truth for
// (a) the order of the left nav and (b) which routes read
// `?instance=` from the URL.
//
// Both Shell.tsx (sidebar NavLinks) and CommandPalette.tsx (⌘K
// navigation actions) read from this list so they stay in lockstep.
// BIT-223 fixed the sidebar dropping `?instance=`; a follow-up
// surfaced that the palette had the same bug because it carried its
// own copy of the route list. Centralising here means a future tab
// addition can't reintroduce the divergence.

export interface NavEntry {
  to: string;
  label: string;
  // Routes flagged instanceScoped read `?instance=` from the URL.
  // Links to these routes must thread the currently-selected
  // instance, or the user lands on "No instance selected" even
  // though the header picker still shows one.
  instanceScoped: boolean;
}

export const NAV: readonly NavEntry[] = [
  { to: "/", label: "Overview", instanceScoped: false },
  { to: "/wallet", label: "Wallet", instanceScoped: true },
  { to: "/explorer", label: "Explorer", instanceScoped: true },
  { to: "/dar", label: "DAR Manager", instanceScoped: true },
  { to: "/metrics", label: "Metrics", instanceScoped: true },
  { to: "/tokens", label: "Tokens", instanceScoped: true },
  { to: "/agent", label: "Agent Skills", instanceScoped: false },
];

// linkTo appends `?instance=<name>` for instance-scoped routes when
// an instance is selected. The encodeURIComponent guards instance
// names with URL-significant characters (the CLI permits `-` and
// `_` today, but the rule is cheap insurance).
export function linkTo(
  to: string,
  instanceScoped: boolean,
  instance: string | null,
): string {
  return instanceScoped && instance
    ? `${to}?instance=${encodeURIComponent(instance)}`
    : to;
}

// isInstanceScoped looks up a path in NAV and returns its scoping
// flag. Used by callers (the palette) that know a path but not its
// NavEntry. Unknown paths default to false — safer than silently
// dropping the param onto an unrelated route.
export function isInstanceScoped(path: string): boolean {
  return NAV.find((n) => n.to === path)?.instanceScoped ?? false;
}
