import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

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
  build: {
    outDir: "../internal/ui/dist",
    emptyOutDir: true,
    sourcemap: true,
    // Produce a manifest so the future asset-fingerprint test
    // (BIT-141 follow-up) can verify hashed filenames land
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
});
