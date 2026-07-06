// Single source of truth: the repository's docs/*.md files.
//
// scripts/sync-docs.mjs regenerates the mapped site pages from docs/ on
// every `npm run dev` / `npm run build` (predev/prebuild hooks). Edit the
// repo docs, not the generated pages — generated pages are gitignored.
//
// `dest` is the site path (no extension) under src/content/docs/.
// `description` is site-only metadata (Starlight frontmatter + SEO);
// it lives here so the repo docs stay plain markdown.
//
// A docs/*.md file that is NOT listed here is auto-published under
// reference/<name> with a build warning — new docs never silently
// disappear from the site. Add an entry to place (and describe) it
// properly.

export const docsMap = [
  { src: 'getting-started.md', dest: 'getting-started', description: 'Install Canton DevKit as a DPM component or standalone binary on macOS, Linux, and Windows, and verify your host is ready for LocalNet.' },
  { src: 'localnet-lifecycle.md', dest: 'guides/localnet-lifecycle', description: 'Zero to a running Canton LocalNet — start, inspect, run multiple instances, pin ports, tear down, and answers to common questions.' },
  { src: 'explorer.md', dest: 'guides/explorer', description: 'Browse the Active Contract Set, recent transactions, and a ledger timeline of a running LocalNet from the Web UI — with CLI equivalents for scripting.' },
  { src: 'observability.md', dest: 'guides/observability', description: 'Enable the observability profile for Prometheus + Grafana, understand the live Splice metric naming convention, and toggle the sidecars at runtime.' },
  { src: 'dashboard-customization.md', dest: 'guides/dashboard-customization', description: 'Extend, replace, or restore the bundled Grafana dashboard for a running LocalNet — panels, template variables, and persistence across down/up cycles.' },
  { src: 'tokens.md', dest: 'guides/tokens', description: 'Create, mint, transfer, and burn CIP-0112 (Token Standard V2) instruments against a live LocalNet from the CLI or the Web UI.' },
  { src: 'homebrew.md', dest: 'guides/homebrew', description: 'Install canton-devkit via the Homebrew tap or direct formula, and how the formula is kept in sync on every release.' },
  { src: 'faq.md', dest: 'reference/faq', description: 'Common questions about canton-devkit: what it is, how the CLI and Web UI relate, versions, and day-to-day usage.' },
  { src: 'versions.md', dest: 'reference/versions', description: 'How DevKit pins tested Splice LocalNet versions by commit SHA and content hash, discovers upstream tags, and resolves uncurated versions on opt-in.' },
  { src: 'packaging.md', dest: 'reference/packaging', description: 'How canton-devkit ships — standalone binaries, the DPM component, the Debian/APT package — and the current supply-chain integrity story.' },
  { src: 'telemetry.md', dest: 'reference/telemetry', description: "The complete reference for canton-devkit's anonymous, aggregate usage counters — what is collected, what is never collected, and how to inspect or disable it." },
  { src: 'limitations.md', dest: 'reference/limitations', description: 'Things DevKit does not (yet) do well, with the rationale and workarounds where applicable.' },
  { src: 'troubleshooting.md', dest: 'reference/troubleshooting', description: 'Failure modes and fixes for LocalNet bring-up, ports, V2 token instances, credentials, and snapshots.' },
];

// Hand-authored pages the sync must never touch or prune.
export const handAuthored = ['index.mdx', '404.md', 'operations/telemetry-collector.md'];

// Internal / process docs that live in docs/ but must NOT be published to
// the grant-facing site (see AGENTS.md "Grant-facing documentation" —
// out-of-scope files). Listed by docs/ filename; the sync skips them
// instead of auto-publishing them under reference/.
export const doNotPublish = ['changes-from-proposal.md', 'original-devkit-proposal.md'];

// Repo root on GitHub, for links that point outside docs/ (e.g. the
// telemetry collector runbook) so they resolve from the published site.
export const repoBlobBase = 'https://github.com/bitdynamics-ab/canton-devkit/blob/main/';

// GitHub "edit this page" base for the canonical docs/ sources. Generated
// pages point their editUrl here (at docs/<src>), not at the generated
// mirror under website/, so the edit link lands on the source of truth.
export const docsEditBase = 'https://github.com/bitdynamics-ab/canton-devkit/edit/main/docs/';
