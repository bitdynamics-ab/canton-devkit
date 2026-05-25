# Design mockups

React/JSX prototypes of every `dpm localnet …` CLI command and the M2 Web UI.
Open `index.html` in a browser to render — it's a self-contained design-canvas
with a Figma-like pan/zoom viewer.

These files are **acceptance specs**, not shipping code:

- CLI mockups (`screens-*.jsx`, `terminal.jsx`) define the visual contract that
  the Go commands in [`internal/cli/localnet/`](../../../internal/cli/localnet/)
  reproduce via [`internal/ui/term/`](../../../internal/ui/term/) primitives.
- Web UI mockups (`webui-*.jsx`, `webui-shell.jsx`) are lifted into the real
  Vite + React frontend under `frontend/` (M2 / Phase 2 of the integration
  plan), then refactored to consume live data from `internal/ui/handlers`.

Editing notes:

- `terminal.jsx` token palette (`TERM.*`) is mirrored in
  `internal/ui/term/color.go`. Keep them in sync.
- The `design-canvas.jsx` viewer is in-house tooling; it is not part of the
  shipping Web UI bundle and stays under this directory.

See [`docs/PROJECT.md`](../../PROJECT.md) and the `reflective-forging-blanket`
plan for the milestone roadmap that ties these mockups to delivery.

## Traceability

One row per `screens-*.jsx` / `webui-*.jsx` file → the ticket that owns its
implementation → the Go (CLI) or `frontend/` (Web UI) file that delivers it.
Reviewer pin on PR #31 #10: keeps the design-spec ↔ implementation link
explicit so a mockup edit and a missing implementation update show up as a
visible row gap.

| Mockup | Ticket | Implementation |
|---|---|---|
| `terminal.jsx` | BIT-121 | `internal/ui/term/{color,style,box,section,step,table,spinner}.go` |
| `screens-lifecycle.jsx` (ScreenUp) | BIT-122 | `internal/localnet/up.go` |
| `screens-lifecycle.jsx` (ScreenStatus) | BIT-144 | `internal/cli/localnet/status.go` |
| `screens-lifecycle.jsx` (ScreenDoctor) | BIT-123 | `internal/cli/localnet/doctor.go` |
| `screens-lifecycle.jsx` (ScreenLogs) | BIT-145 | `internal/cli/localnet/logs.go` |
| `screens-lifecycle.jsx` (ScreenDown) | BIT-124 | `internal/cli/localnet/down.go` |
| `screens-tokens-help.jsx` (ScreenList) | BIT-146 | `internal/cli/localnet/list.go` |
| `screens-tokens-help.jsx` (ScreenEnv) | BIT-125 | `internal/cli/localnet/env.go` |
| `screens-tokens-help.jsx` (ScreenHelp) | BIT-148 | `internal/cli/help.go` |
| `screens-tokens-help.jsx` (ScreenError) | BIT-126 | `internal/localnet/friendly_errors.go` |
| `screens-tokens-help.jsx` (snapshot — derived) | BIT-147 | `internal/cli/localnet/snapshot.go` |
| `screens-dar.jsx` | BIT-127 | `internal/cli/localnet/dar/` (PR #17) |
| `screens-contracts.jsx` | BIT-136 | `internal/cli/localnet/contracts/` *(not landed)* |
| `screens-tokens-help.jsx` (token wizard/actions) | BIT-138 / BIT-139 | `internal/cli/localnet/token/` *(not landed)* |
| `webui-shell.jsx` | BIT-133 | `frontend/src/shell/` *(not landed)* |
| `webui-dashboard.jsx` | BIT-129 / BIT-131 | `frontend/src/dashboard/` *(not landed)* |
| `webui-explorer.jsx` | BIT-131 / BIT-132 | `frontend/src/explorer/` *(not landed)* |
| `webui-dar.jsx` | BIT-131 | `frontend/src/dar/` *(not landed)* |
| `webui-metrics-agent.jsx` | BIT-134 / BIT-135 | `frontend/src/metrics/`, `frontend/src/agent/` *(not landed)* |
| `webui-extras.jsx` | BIT-140 + ⌘K + multi-instance | `frontend/src/tokens/`, `frontend/src/palette/` *(not landed)* |
| `design-canvas.jsx`, `browser-window.jsx`, `index.html`, `index-print.html` | n/a | in-house design tooling only — not shipped |
| `assets/{bitdynamics-lockup,mark,favicon}.svg` + `bitdynamics-mark-256.png` | BIT-133 | `frontend/public/` *(not landed)* — Bit Dynamics brand assets bundled into the Vite build |

## Brand & branding

The `assets/` directory carries the canonical Bit Dynamics brand glyphs used
by both the design-canvas preview pages and the M2 Web UI:

- `bitdynamics-mark.svg` / `bitdynamics-mark-black.svg` — the chip+candle
  mark (44×44 framed square with a 20×20 lime fill and a top "candle" lead).
- `bitdynamics-lockup.svg` — mark + "BITDYNAMICS" wordmark, horizontal.
- `favicon.svg` — favicon variant of the mark.
- `bitdynamics-mark-256.png` — raster fallback for environments that don't
  render SVG well (CI screenshot diffs, slack previews).

The same chip+candle motif is reproduced inline in `terminal.jsx` (CLI
preview header) and `webui-shell.jsx` (`LogoMark` + `LogoLockup` React
components) so the design canvas renders correctly even when the assets
folder isn't served. M2 frontend (BIT-133) should `import` from
`frontend/public/assets/` so the bundled binary serves the same files.

## What's new in this drop (2026-05-25)

- `terminal.jsx`: status bar now renders the Bit Dynamics mark as an
  inline SVG instead of plain "BitDynamics" text. CLI implementation is
  unaffected (no SVG in a terminal); this is design-canvas chrome.
- `webui-shell.jsx`: real `LogoMark` (chip+candle) replaces the previous
  placeholder; new `LogoLockup` (mark + wordmark) component.
- `webui-dashboard.jsx`: major new "Developer setup" card with JWT
  generator (party/ttl/aud chips, monospace token preview, regenerate),
  app config exporter (.env/daml.yaml/package.json/application.conf/json/
  yaml tabs), and credentials/endpoints panel. Drives new M2 REST
  endpoints (BIT-131) and frontend UI (BIT-133).
- `webui-metrics-agent.jsx`: new "What this does" explainer card on the
  Agent skills page — 3-step walkthrough (install once, ask in plain
  English, agent runs DevKit safely). Drives the copy for BIT-135 skill
  docs and the Agent screen layout.
- `index-print.html`: print variant of `index.html` for offline review.
- `assets/`: real Bit Dynamics brand files.
