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
