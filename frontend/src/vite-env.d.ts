/// <reference types="vite/client" />

// Injected by Vite's `define` (vite.config.ts) with the short git
// commit the UI bundle was built from. Undefined under Vitest, which
// does not apply Vite `define` — read defensively.
declare const __UI_COMMIT__: string;
