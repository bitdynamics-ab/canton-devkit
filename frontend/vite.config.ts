/// <reference types="vitest/config" />
import { execSync } from "node:child_process";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Short git commit the UI bundle was built from. Surfaced in the side
// nav footer so screenshots reveal which UI commit a user is on.
// Falls back to "dev" outside a git checkout (e.g. release tarballs).
const uiCommit = (() => {
  try {
    return execSync("git rev-parse --short HEAD", { encoding: "utf8" }).trim();
  } catch {
    return "dev";
  }
})();

// Vite config for the canton-devkit Web UI.
//
// build.outDir is set to ../internal/ui/dist so `npm run build`
// drops its output directly where the Go binary's go:embed
// picks it up. The Makefile target `make frontend` runs `npm
// ci && npm run build` in this directory — no separate copy
// step needed.
//
// server.proxy forwards /api and /events to the Go backend
// during `npm run dev` so the dev server (port 5173) can talk
// to a running `dpm localnet ui --port 7777`. Both must be up
// for the dev loop to work.
export default defineConfig({
  plugins: [react()],
  define: {
    __UI_COMMIT__: JSON.stringify(uiCommit),
  },
  build: {
    outDir: "../internal/ui/dist",
    emptyOutDir: true,
    sourcemap: true,
    // Produce a manifest so the future asset-fingerprint test
    // can verify hashed filenames land
    // where index.html references them.
    manifest: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:7777",
      "/events": {
        target: "http://127.0.0.1:7777",
        ws: false, // SSE is plain HTTP, not WebSocket
      },
    },
  },
  test: {
    // jsdom — needed because the components touch document,
    // navigator.clipboard, and react-router-dom expects a DOM.
    environment: "jsdom",
    // Auto-imports @testing-library/jest-dom matchers and runs
    // afterEach cleanup so tests don't leak DOM state between
    // each other.
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    // Co-located convention: src/**/*.test.ts(x). Keeps each test
    // next to the code it exercises rather than mirroring the
    // tree under a separate __tests__/ root.
    include: ["src/**/*.test.{ts,tsx}"],
    css: false,
  },
});
