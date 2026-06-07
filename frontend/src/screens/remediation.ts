// wire-stable error codes → localized remediation copy.
//
// Pure function: each known ErrorCode maps to a remediation
// block (title + step list); unknown / undefined / explicit
// PREFLIGHT_FAILED returns null so the renderer falls back to the
// generic cause-text without an empty panel.
//
// Extracted from CreateLocalNetModal.tsx so it can be unit-tested.
// The default-null case is most at risk of drifting silently — a
// future contributor adds a new code to the Go side, frontend tests
// would still pass against the unknown→null fallback. Hence
// Remediation.test.ts pins the 9 known codes individually.

import type { ErrorCode } from "../api";

export interface Remediation {
  title: string;
  steps: string[];
}

export function remediationForCode(code?: ErrorCode): Remediation | null {
  switch (code) {
    case "PORTS_IN_USE":
      return {
        title: "A previously allocated host port is in use",
        steps: [
          "Find what's using the port: `lsof -i :<port>` (macOS/Linux) or `Get-NetTCPConnection -LocalPort <port>` (Windows).",
          "Either stop the conflicting process, or run `dpm localnet down --name <other-instance>` to free its ports.",
          "Then click Create again — the instance will reuse the same ports.",
        ],
      };
    case "DOCKER_DOWN":
      return {
        title: "Docker daemon isn't reachable",
        steps: [
          "Start Docker Desktop (macOS/Windows) or `sudo systemctl start docker` (Linux).",
          "Wait for the whale icon (Docker Desktop) to stop animating, then click Create again.",
        ],
      };
    case "DOCKER_NOT_INSTALLED":
      return {
        title: "Docker CLI not found on this host",
        steps: [
          "Install Docker Desktop from https://www.docker.com/products/docker-desktop/ (macOS/Windows).",
          "Or install the Docker Engine + Compose plugin from https://docs.docker.com/engine/install/ (Linux).",
          "Reopen this page after Docker is on PATH.",
        ],
      };
    case "COMPOSE_V1_OR_MISSING":
      return {
        title: "Docker Compose v2 plugin missing or v1 detected",
        steps: [
          "Install the Compose v2 plugin: https://docs.docker.com/compose/install/.",
          "Verify with `docker compose version` — should report v2.x.",
        ],
      };
    case "DOCKER_MEMORY_LOW":
      return {
        title: "Docker memory below this version's floor",
        steps: [
          "Docker Desktop → Settings → Resources → Memory → raise to the recommended size.",
          "Splice 0.6.x packs ~17 GB of JVM heap into a single container — 4 GB triggers OOM-loops, 8 GB is the minimum, 12 GB has headroom.",
          "Apply & Restart Docker, then click Create again.",
        ],
      };
    case "DISK_LOW":
      return {
        title: "Not enough free disk space at the registry root",
        steps: [
          "Free up space (Splice images alone are ~5 GB; the data dir grows ~1 GB/day under load).",
          "Or relocate the registry: `export CANTON_DEVKIT_REGISTRY=/path/to/roomier/volume` and re-launch the Web UI.",
        ],
      };
    case "CANTON_OOM":
      // Thresholds are NOT hard-coded in copy.
      // The per-version minimum + recommended bytes live
      // in `internal/splice/versions.json` (and surface in the
      // pre-flight report's Docker-memory check detail string).
      // Pointing the user at "what the pre-flight panel said"
      // keeps the displayed numbers and the catalogue in sync.
      return {
        title: "Canton was OOM-killed by the kernel",
        steps: [
          "The kernel killed the canton container (exit 137/143) in a restart loop — this is a Docker-memory-limit issue, not a Splice config issue.",
          "Open Docker Desktop → Settings → Resources → Memory, and raise to at least the per-version minimum shown in the pre-flight panel (re-open the create modal to see it).",
          "Apply & Restart Docker, then retry. Or pick a lighter Splice version in the picker — the catalogue lists per-version requirements.",
        ],
      };
    case "CONTAINER_UNHEALTHY":
      return {
        title: "One or more containers reported unhealthy",
        steps: [
          "Inspect the container health panel (it shows live state per container).",
          "Tail the failing container's logs: `dpm localnet container logs <inst> <service>`.",
          "Common causes: postgres connection limit, splice waiting on canton, ledger API not ready.",
        ],
      };
    case "PREFLIGHT_FAILED":
    case undefined:
      return null;
    default:
      return null;
  }
}
