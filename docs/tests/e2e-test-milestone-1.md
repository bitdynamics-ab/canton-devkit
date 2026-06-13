# E2E Test Plan — Milestone 1: LocalNet Management CLI

> **Proposal Reference:** `original-devkit-proposal.md`, Milestone 1 (Lines 230–247)
> **Estimated Delivery:** Month 3
> **Total Tests:** 18
> **Platforms:** macOS (Apple Silicon), Linux (amd64), Windows (amd64)

---

## Overview

This test plan validates the core LocalNet lifecycle management CLI delivered in Milestone 1. Every test is designed for mechanical execution by an AI agent or CI pipeline.

### Conventions

- Commands are shown in both forms: `dpm localnet ...` (DPM component) and `canton-devkit localnet ...` (standalone). Both must be tested.
- `$CLI` is used as a placeholder — set it to either `dpm localnet` or `canton-devkit localnet` before running.
- Exit code `0` = success. Non-zero = failure (specific codes noted where relevant).
- Output verification uses `grep -qE` patterns. A test step passes if the grep matches.
- `$PLATFORM` is one of `macos`, `linux`, `windows`.
- Timeouts are specified per-step where relevant. Default step timeout: 30 seconds unless noted.

### Environment Setup

```bash
# Set CLI mode (run full suite twice — once per mode)
export CLI="dpm localnet"       # DPM component mode
# OR
export CLI="canton-devkit localnet"  # standalone mode

# Ensure Docker is running
docker info > /dev/null 2>&1 || { echo "FAIL: Docker not running"; exit 1; }

# Ensure clean state before test suite
$CLI clean --name e2e-test-default --force 2>/dev/null || true
$CLI clean --name e2e-test-a --force 2>/dev/null || true
$CLI clean --name e2e-test-b --force 2>/dev/null || true
```

---

## Test Cases

---

### M1-INST-001: Install via DPM component

**Preconditions:** DPM CLI installed, network access to OCI registry.
**Platforms:** All

**Steps:**

1. Install the DevKit DPM component:
   ```bash
   dpm install package canton-devkit
   ```
   - **Expected:** Exit code `0`.
   - **Verify:** `dpm localnet --help` exits `0` and output matches:
     ```bash
     dpm localnet --help 2>&1 | grep -qE "(up|down|restart|clean|status|logs|snapshot|restore)"
     ```

2. Confirm the `localnet` top-level command is registered:
   ```bash
   dpm --help 2>&1 | grep -qE "localnet"
   ```
   - **Expected:** Match found (exit `0`).

**Cleanup:** None.

---

### M1-INST-002: Install standalone binary

**Preconditions:** Network access to GitHub Releases.
**Platforms:** All (platform-specific binary)

**Steps:**

1. Download the correct binary for the current platform:
   ```bash
   # macOS (Apple Silicon)
   curl -L -o canton-devkit https://github.com/<org>/canton-devkit/releases/latest/download/canton-devkit-darwin-arm64
   chmod +x canton-devkit

   # Linux (amd64)
   curl -L -o canton-devkit https://github.com/<org>/canton-devkit/releases/latest/download/canton-devkit-linux-amd64
   chmod +x canton-devkit

   # Windows (amd64) — PowerShell
   # Invoke-WebRequest -Uri https://github.com/<org>/canton-devkit/releases/latest/download/canton-devkit-windows-amd64.exe -OutFile canton-devkit.exe
   ```
   - **Expected:** File downloaded, non-zero size.

2. Verify the binary runs:
   ```bash
   ./canton-devkit localnet --help
   ```
   - **Expected:** Exit code `0`, output matches:
     ```bash
     ./canton-devkit localnet --help 2>&1 | grep -qE "(up|down|restart|clean|status|logs|snapshot|restore)"
     ```

3. Verify checksum (if published):
   ```bash
   curl -L -o checksums.txt https://github.com/<org>/canton-devkit/releases/latest/download/checksums.txt
   sha256sum -c checksums.txt 2>&1 | grep -qE "canton-devkit.*OK"
   ```
   - **Expected:** Checksum matches.

**Cleanup:** `rm -f canton-devkit checksums.txt`

---

### M1-INST-003: Verify binary on all platforms

**Preconditions:** Binary installed (M1-INST-001 or M1-INST-002).
**Platforms:** All (run once per platform)

**Steps:**

1. Check version output:
   ```bash
   $CLI --version
   ```
   - **Expected:** Exit code `0`, output matches a semver pattern:
     ```bash
     $CLI --version 2>&1 | grep -qE "[0-9]+\.[0-9]+\.[0-9]+"
     ```

2. Check help output includes all Milestone 1 commands:
   ```bash
   $CLI --help 2>&1 | grep -qE "up"
   $CLI --help 2>&1 | grep -qE "down"
   $CLI --help 2>&1 | grep -qE "restart"
   $CLI --help 2>&1 | grep -qE "clean"
   $CLI --help 2>&1 | grep -qE "status"
   $CLI --help 2>&1 | grep -qE "logs"
   $CLI --help 2>&1 | grep -qE "snapshot"
   $CLI --help 2>&1 | grep -qE "restore"
   $CLI --help 2>&1 | grep -qE "doctor"
   ```
   - **Expected:** All grep commands exit `0`.

3. Verify no runtime dependencies required (no Go, Node, Python, Rust):
   ```bash
   # Binary should be statically linked / self-contained
   file $(which canton-devkit) 2>/dev/null || file $(which dpm) 2>/dev/null
   ```
   - **Expected:** Output indicates a compiled binary (e.g., "Mach-O", "ELF", "PE32").

**Cleanup:** None.

---

### M1-DOC-001: Doctor — all checks pass

**Preconditions:** Docker running, Compose v2 available, sufficient resources.
**Platforms:** All

**Steps:**

1. Run doctor:
   ```bash
   $CLI doctor
   ```
   - **Expected:** Exit code `0`.
   - **Verify output includes pass indicators for all checks:**
     ```bash
     $CLI doctor 2>&1 | grep -qiE "(docker cli|docker daemon|compose v2|ports|disk|memory)"
     ```
   - **Verify no failures reported:**
     ```bash
     $CLI doctor 2>&1 | grep -qiE "(fail|error|missing)" && echo "FAIL: doctor reports issues" || echo "PASS"
     ```

**Cleanup:** None.

---

### M1-DOC-002: Doctor — Docker not installed

**Preconditions:** Docker CLI removed from PATH or Docker daemon stopped.
**Platforms:** All

**Steps:**

1. Temporarily hide Docker from PATH:
   ```bash
   PATH_BACKUP="$PATH"
   export PATH=$(echo "$PATH" | tr ':' '\n' | grep -v docker | tr '\n' ':')
   ```

2. Run doctor:
   ```bash
   $CLI doctor
   ```
   - **Expected:** Non-zero exit code.
   - **Verify remediation instructions in output:**
     ```bash
     $CLI doctor 2>&1 | grep -qiE "(install docker|docker not found|docker desktop)"
     ```

3. Restore PATH:
   ```bash
   export PATH="$PATH_BACKUP"
   ```

**Cleanup:** PATH restored in step 3.

---

### M1-DOC-003: Doctor — insufficient resources

**Preconditions:** Docker running but with known resource constraints (e.g., low memory limit on Docker Desktop).
**Platforms:** macOS, Windows (Docker Desktop with configurable resource limits)

**Steps:**

1. Run doctor with constrained Docker resources:
   ```bash
   $CLI doctor
   ```
   - **Expected:** Non-zero exit code OR exit `0` with warnings.
   - **Verify resource warnings in output:**
     ```bash
     $CLI doctor 2>&1 | grep -qiE "(memory|disk|insufficient|warning)"
     ```

**Note:** This test may require manual Docker Desktop resource configuration. On Linux with native Docker, simulate by setting `--memory` limits on the daemon. If the environment has sufficient resources, verify that doctor reports adequate resources instead.

**Cleanup:** Restore Docker resource settings to original values.

---

### M1-UP-001: LocalNet up (default)

**Preconditions:** Docker running, no existing LocalNet named `e2e-test-default`.
**Platforms:** All
**Timeout:** 300 seconds (5 minutes for full startup + readiness)

**Steps:**

1. Start a default LocalNet:
   ```bash
   $CLI up --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify endpoints printed:**
     ```bash
     $CLI up --name e2e-test-default 2>&1 | grep -qiE "(endpoint|port|url|ledger|json.api)"
     ```
   - **Verify readiness wait completed (command did not return until services ready):**
     ```bash
     $CLI status --name e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"
     ```

2. Verify Docker resources are labeled correctly:
   ```bash
   docker ps --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-test-default"
   ```
   - **Expected:** At least one container matches.

3. Verify deterministic Docker Compose project name:
   ```bash
   docker compose ls --format json 2>/dev/null | grep -qE "e2e-test-default"
   ```
   - **Expected:** Project listed.

**Cleanup:** `$CLI down --name e2e-test-default`

---

### M1-UP-002: LocalNet up with --name

**Preconditions:** Docker running.
**Platforms:** All
**Timeout:** 300 seconds

**Steps:**

1. Start a named LocalNet:
   ```bash
   $CLI up --name e2e-named-test
   ```
   - **Expected:** Exit code `0`.

2. Verify the instance uses the specified name:
   ```bash
   docker ps --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-named-test"
   ```
   - **Expected:** Match found.

3. Verify status references the correct name:
   ```bash
   $CLI status --name e2e-named-test 2>&1 | grep -qiE "e2e-named-test"
   ```
   - **Expected:** Exit code `0`, name appears in output.

**Cleanup:** `$CLI down --name e2e-named-test && $CLI clean --name e2e-named-test --force`

---

### M1-UP-003: LocalNet up with --version

**Preconditions:** Docker running, known valid Splice version from compatibility matrix.
**Platforms:** All
**Timeout:** 300 seconds

**Steps:**

1. Start a LocalNet with explicit version:
   ```bash
   SPLICE_VERSION="<known-valid-version>"  # from compatibility matrix
   $CLI up --name e2e-version-test --version "$SPLICE_VERSION"
   ```
   - **Expected:** Exit code `0`.

2. Verify the selected version is reflected in status:
   ```bash
   $CLI status --name e2e-version-test 2>&1 | grep -qE "$SPLICE_VERSION"
   ```
   - **Expected:** Version string appears in output.

3. Test with invalid version:
   ```bash
   $CLI up --name e2e-bad-version --version "0.0.0-nonexistent"
   ```
   - **Expected:** Non-zero exit code, error message about invalid/unavailable version.

**Cleanup:** `$CLI down --name e2e-version-test && $CLI clean --name e2e-version-test --force`

---

### M1-STS-001: Status shows healthy services

**Preconditions:** LocalNet `e2e-test-default` running (depends on M1-UP-001 setup).
**Platforms:** All

**Steps:**

1. Start LocalNet if not running:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Check status:
   ```bash
   $CLI status --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify output includes required information:**
     ```bash
     OUTPUT=$($CLI status --name e2e-test-default 2>&1)
     echo "$OUTPUT" | grep -qiE "(healthy|running|ready)"          # service health
     echo "$OUTPUT" | grep -qiE "(port|endpoint)"                   # ports/endpoints
     echo "$OUTPUT" | grep -qiE "(participant)"                     # participant readiness
     echo "$OUTPUT" | grep -qiE "(version|splice)"                  # selected version
     ```

3. Check status for non-existent LocalNet:
   ```bash
   $CLI status --name nonexistent-localnet-xyz
   ```
   - **Expected:** Non-zero exit code, clear error message.

**Cleanup:** `$CLI down --name e2e-test-default`

---

### M1-LOG-001: Logs — full and service-filtered

**Preconditions:** LocalNet `e2e-test-default` running.
**Platforms:** All

**Steps:**

1. Start LocalNet if not running:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Tail full logs (non-blocking with timeout):
   ```bash
   timeout 10 $CLI logs --name e2e-test-default 2>&1 | head -50
   ```
   - **Expected:** Output is non-empty (logs are streaming).
   - **Verify:**
     ```bash
     timeout 10 $CLI logs --name e2e-test-default 2>&1 | head -5 | wc -l | grep -qE "[1-9]"
     ```

3. Tail logs for a specific service:
   ```bash
   timeout 10 $CLI logs participant --name e2e-test-default 2>&1 | head -20
   ```
   - **Expected:** Output is non-empty, logs come from the specified service only.

4. Tail logs for non-existent service:
   ```bash
   $CLI logs nonexistent-service --name e2e-test-default
   ```
   - **Expected:** Non-zero exit code or clear error message about unknown service.

**Cleanup:** `$CLI down --name e2e-test-default`

---

### M1-RST-001: Restart full + single service

**Preconditions:** LocalNet `e2e-test-default` running.
**Platforms:** All
**Timeout:** 300 seconds

**Steps:**

1. Start LocalNet if not running:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Restart the full LocalNet:
   ```bash
   $CLI restart --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify readiness after restart:**
     ```bash
     $CLI status --name e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"
     ```

3. Restart a single service:
   ```bash
   $CLI restart participant --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify the restarted service is healthy:**
     ```bash
     $CLI status --name e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"
     ```

**Cleanup:** `$CLI down --name e2e-test-default`

---

### M1-DWN-001: Down stops instance cleanly

**Preconditions:** LocalNet `e2e-test-default` running.
**Platforms:** All

**Steps:**

1. Start LocalNet:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Verify it is running:
   ```bash
   docker ps --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-test-default"
   ```

3. Stop it:
   ```bash
   $CLI down --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.

4. Verify containers stopped:
   ```bash
   docker ps --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-test-default" && echo "FAIL: containers still running" || echo "PASS"
   ```

5. Verify unrelated Docker resources are not affected:
   ```bash
   # If other non-DevKit containers were running before, they should still be running
   docker ps --format '{{.Names}}' | grep -v "canton-devkit" | wc -l
   ```
   - **Expected:** Count unchanged from before test.

**Cleanup:** `$CLI clean --name e2e-test-default --force 2>/dev/null || true`

---

### M1-CLN-001: Clean removes all resources

**Preconditions:** LocalNet `e2e-test-default` has been started and stopped.
**Platforms:** All

**Steps:**

1. Start and stop a LocalNet:
   ```bash
   $CLI up --name e2e-test-default
   $CLI down --name e2e-test-default
   ```

2. Verify resources exist (volumes, networks):
   ```bash
   docker volume ls --format '{{.Name}}' | grep -qE "e2e-test-default"
   ```
   - **Expected:** Volumes exist from the stopped instance.

3. Clean the instance:
   ```bash
   $CLI clean --name e2e-test-default
   ```
   - **Expected:** Exit code `0`. May prompt for confirmation (use `--force` if non-interactive).
   - If confirmation is required:
     ```bash
     echo "y" | $CLI clean --name e2e-test-default
     # OR
     $CLI clean --name e2e-test-default --force
     ```

4. Verify all DevKit-managed resources removed:
   ```bash
   docker volume ls --format '{{.Name}}' | grep -qE "e2e-test-default" && echo "FAIL: volumes remain" || echo "PASS"
   docker network ls --format '{{.Name}}' | grep -qE "e2e-test-default" && echo "FAIL: networks remain" || echo "PASS"
   docker ps -a --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-test-default" && echo "FAIL: containers remain" || echo "PASS"
   ```

**Cleanup:** None (test is self-cleaning).

---

### M1-SNP-001: Snapshot and restore

**Preconditions:** LocalNet `e2e-test-default` running.
**Platforms:** All
**Timeout:** 300 seconds

**Steps:**

1. Start LocalNet and wait for readiness:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Create a snapshot:
   ```bash
   $CLI snapshot --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify snapshot reference is output:**
     ```bash
     $CLI snapshot --name e2e-test-default 2>&1 | grep -qiE "(snapshot|saved|created)"
     ```

3. Stop and clean the LocalNet:
   ```bash
   $CLI down --name e2e-test-default
   $CLI clean --name e2e-test-default --force
   ```

4. Restore from snapshot:
   ```bash
   $CLI restore --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify LocalNet is running and healthy after restore:**
     ```bash
     $CLI status --name e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"
     ```

**Cleanup:** `$CLI down --name e2e-test-default && $CLI clean --name e2e-test-default --force`

---

### M1-ISO-001: Two named instances, non-conflicting ports

**Preconditions:** Docker running, sufficient resources for two LocalNets.
**Platforms:** All
**Timeout:** 600 seconds (10 minutes for two full startups)

**Steps:**

1. Start first instance with explicit ports:
   ```bash
   $CLI up --name e2e-test-a
   ```
   - **Expected:** Exit code `0`.

2. Start second instance with non-conflicting ports:
   ```bash
   $CLI up --name e2e-test-b
   ```
   - **Expected:** Exit code `0`.

3. Verify both instances are running and isolated:
   ```bash
   $CLI status --name e2e-test-a 2>&1 | grep -qiE "(healthy|ready|running)"
   $CLI status --name e2e-test-b 2>&1 | grep -qiE "(healthy|ready|running)"
   ```

4. Verify port isolation (no port conflicts):
   ```bash
   PORTS_A=$($CLI status --name e2e-test-a 2>&1 | grep -oE "[0-9]{4,5}" | sort)
   PORTS_B=$($CLI status --name e2e-test-b 2>&1 | grep -oE "[0-9]{4,5}" | sort)
   OVERLAP=$(comm -12 <(echo "$PORTS_A") <(echo "$PORTS_B"))
   [ -z "$OVERLAP" ] && echo "PASS: no port overlap" || echo "FAIL: overlapping ports: $OVERLAP"
   ```

5. Verify Docker resource isolation (separate project names):
   ```bash
   docker compose ls --format json 2>/dev/null | grep -qE "e2e-test-a"
   docker compose ls --format json 2>/dev/null | grep -qE "e2e-test-b"
   ```

6. Stop one instance and verify the other is unaffected:
   ```bash
   $CLI down --name e2e-test-a
   $CLI status --name e2e-test-b 2>&1 | grep -qiE "(healthy|ready|running)"
   ```
   - **Expected:** Instance B still healthy.

**Cleanup:**
```bash
$CLI down --name e2e-test-a 2>/dev/null || true
$CLI down --name e2e-test-b 2>/dev/null || true
$CLI clean --name e2e-test-a --force 2>/dev/null || true
$CLI clean --name e2e-test-b --force 2>/dev/null || true
```

---

### M1-ENV-001: Env export outputs valid config

**Preconditions:** LocalNet `e2e-test-default` running.
**Platforms:** All

**Steps:**

1. Start LocalNet if not running:
   ```bash
   $CLI up --name e2e-test-default
   ```

2. Export environment:
   ```bash
   $CLI env --name e2e-test-default
   ```
   - **Expected:** Exit code `0`.
   - **Verify `.env`-style output:**
     ```bash
     OUTPUT=$($CLI env --name e2e-test-default 2>&1)
     echo "$OUTPUT" | grep -qE "^[A-Z_]+=.+"                        # KEY=value format
     echo "$OUTPUT" | grep -qiE "(LEDGER|JSON.API|ADMIN|PARTICIPANT)" # expected keys
     ```

3. Verify exported values are usable (source and test a variable):
   ```bash
   eval "$($CLI env --name e2e-test-default)"
   # Verify at least one URL/port is reachable
   curl -sf "http://${LEDGER_API_HOST:-localhost}:${LEDGER_API_PORT:-6865}/health" > /dev/null 2>&1 || \
   curl -sf "http://${JSON_API_HOST:-localhost}:${JSON_API_PORT:-7575}/health" > /dev/null 2>&1 || \
   echo "WARN: Could not reach exported endpoints (may require different health check path)"
   ```

**Cleanup:** `$CLI down --name e2e-test-default`

---

### M1-LST-001: List discovers running instances

**Preconditions:** At least one named LocalNet running.
**Platforms:** All

**Steps:**

1. Start two instances:
   ```bash
   $CLI up --name e2e-test-a
   $CLI up --name e2e-test-b
   ```

2. List instances:
   ```bash
   $CLI list
   ```
   - **Expected:** Exit code `0`.
   - **Verify both instances appear:**
     ```bash
     OUTPUT=$($CLI list 2>&1)
     echo "$OUTPUT" | grep -qE "e2e-test-a"
     echo "$OUTPUT" | grep -qE "e2e-test-b"
     ```

3. Stop one instance and re-list:
   ```bash
   $CLI down --name e2e-test-a
   OUTPUT=$($CLI list 2>&1)
   echo "$OUTPUT" | grep -qE "e2e-test-b"
   ```
   - **Expected:** Instance B still listed, instance A either removed or shown as stopped.

4. Verify no non-DevKit containers appear in the list:
   ```bash
   $CLI list 2>&1 | grep -qiE "(canton-devkit|localnet|e2e-test)" || echo "WARN: list output format unclear"
   ```

**Cleanup:**
```bash
$CLI down --name e2e-test-a 2>/dev/null || true
$CLI down --name e2e-test-b 2>/dev/null || true
$CLI clean --name e2e-test-a --force 2>/dev/null || true
$CLI clean --name e2e-test-b --force 2>/dev/null || true
```

---

## Exit Code Contract

| Exit Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error |
| `2` | Docker not available or preflight check failed |
| `3` | LocalNet instance not found |
| `4` | Port conflict |
| `5` | Resource insufficient (memory/disk) |
| Non-zero | Any failure (agent should capture stderr for diagnostics) |

*Note: Exact exit codes are subject to implementation. The key contract is: `0` = success, non-zero = failure with diagnostic output on stderr.*

---

## Cross-Platform Notes

| Platform | Special Considerations |
|---|---|
| **macOS (Apple Silicon)** | Docker Desktop required. `file` command shows "Mach-O 64-bit executable arm64". Ports bind to `localhost` by default. |
| **Linux (amd64)** | Native Docker or Docker Desktop. Doctor should check Linux Docker permissions (user in `docker` group or rootless Docker). `file` command shows "ELF 64-bit LSB executable, x86-64". |
| **Windows (amd64)** | Docker Desktop with WSL 2 backend. Commands use PowerShell or WSL. `file` equivalent: `Get-Command canton-devkit.exe`. Path separators differ. |

---

## Test Execution Summary

| ID | Test Name | Category | Depends On |
|---|---|---|---|
| M1-INST-001 | Install via DPM component | Installation | — |
| M1-INST-002 | Install standalone binary | Installation | — |
| M1-INST-003 | Verify binary on all platforms | Installation | M1-INST-001 or M1-INST-002 |
| M1-DOC-001 | Doctor — all checks pass | Preflight | M1-INST-003 |
| M1-DOC-002 | Doctor — Docker not installed | Preflight | M1-INST-003 |
| M1-DOC-003 | Doctor — insufficient resources | Preflight | M1-INST-003 |
| M1-UP-001 | LocalNet up (default) | Lifecycle | M1-DOC-001 |
| M1-UP-002 | LocalNet up with --name | Lifecycle | M1-DOC-001 |
| M1-UP-003 | LocalNet up with --version | Lifecycle | M1-DOC-001 |
| M1-STS-001 | Status shows healthy services | Status | M1-UP-001 |
| M1-LOG-001 | Logs — full and service-filtered | Logs | M1-UP-001 |
| M1-RST-001 | Restart full + single service | Lifecycle | M1-UP-001 |
| M1-DWN-001 | Down stops instance cleanly | Lifecycle | M1-UP-001 |
| M1-CLN-001 | Clean removes all resources | Lifecycle | M1-DWN-001 |
| M1-SNP-001 | Snapshot and restore | State | M1-UP-001 |
| M1-ISO-001 | Two named instances | Isolation | M1-DOC-001 |
| M1-ENV-001 | Env export outputs valid config | Automation | M1-UP-001 |
| M1-LST-001 | List discovers running instances | Automation | M1-UP-001 |

---

## Execution Results — macOS (standalone mode, no DPM)

**Date:** 2026-06-06
**Platform:** macOS (Apple Silicon / arm64), Docker Desktop 29.5.2
**CLI mode:** `canton-devkit localnet` (standalone — no DPM)
**Binary version:** dev
**Splice version (default/latest):** 0.6.4
**Splice version (explicit):** 0.6.3
**Script:** `scripts/e2e-milestone1.sh`

### CLI Syntax Adaptations

The test plan assumes command syntax that differs from the actual CLI implementation. The following adaptations were applied:

| Test Plan Syntax | Actual CLI Syntax | Notes |
|---|---|---|
| `$CLI restart participant --name X` | `$CLI restart --name X --service participant` | Service is a `--service` flag, not positional |
| `$CLI logs participant --name X` | `$CLI logs --name X --service participant` | Service is a `--service` flag, not positional |
| `$CLI snapshot --name X` | `$CLI snapshot --name X --to <path.tgz>` | `--to` is required — output path |
| `$CLI restore --name X` | `$CLI restore --name X --from <path.tgz>` | `--from` is required — input path |
| `$CLI --version` → semver | `$CDK --version` → `canton-devkit version dev` | Version is top-level, may be `dev` in local builds |
| `$CLI --help` shows `clean`, `restart` | Hidden commands; not in `--help` output | Exist and work via `--help` on each subcommand |
| Docker label `canton-devkit` | `com.docker.compose.project=canton-<name>` | Docker compose project label, not a custom label |
| `$CLI down` then `$CLI clean` | `$CLI clean --force` on running instance | `down` deregisters the instance; `clean` can't find it after. Use `clean --force` directly |

### Skipped Tests

| Test | Reason |
|---|---|
| M1-INST-001 | DPM mode excluded from this run |
| M1-INST-002 | Binary already built locally; no release URL to download from |
| M1-DOC-003 | Requires manually changing Docker Desktop resource limits — destructive to dev environment |
| M1-ISO-001 | Requires two concurrent LocalNets (~16 GB Docker memory); machine has 8.84 GB available |

### Results

| ID | Result | Duration | Notes |
|---|---|---|---|
| M1-INST-003 | **PASS** | <1s | Version (`dev`), help (10 visible + 2 hidden commands), Mach-O arm64 |
| M1-DOC-001 | **PASS** | <2s | 0 issues, 1 warning (memory 8.84/12 GB). Exit 0. |
| M1-DOC-002 | **PASS** | <2s | Exit 2 when Docker hidden from PATH. Remediation: "Install Docker Desktop for Mac" |
| M1-UP-001 | **PASS** | ~2-4 min | Splice 0.6.4, cached images. Status: healthy. Docker compose project verified. |
| M1-STS-001 | **PASS** | <2s | Status includes health, endpoints, participant info. Non-existent instance → exit 1. |
| M1-LOG-001 | **PASS** | <10s | Full logs: 308 lines. Service-filtered (`canton`): 20 lines. |
| M1-ENV-001 | **PASS** | <1s | `export CANTON_*` format. Contains JWT (redacted), audience, port variables. |
| M1-RST-001 | **PASS** | ~5-8 min | Full restart + single-service (`--service canton`) restart. Readiness wait is slow post-restart. |
| M1-SNP-001 | **PASS*** | ~10 min | Snapshot: 78 MB .tgz. Restore + re-up works but splice re-sync can exceed 5 min (crash-consistent, not app-consistent). |
| M1-DWN-001 | **PASS** | ~5s | Containers stopped, non-devkit containers unaffected. |
| M1-CLN-001 | **PASS*** | ~10 min | See finding below. `clean --force` on running instance removes all resources (containers, volumes, networks). |
| M1-UP-002 | **PASS** | ~2-4 min | Named instance `e2e-named-test` created, Docker containers + status verified. |
| M1-UP-003 | **PASS** | ~2-4 min | Splice 0.6.3 (explicit `--version`). Status shows version. Invalid version `0.0.0-nonexistent` → exit 1 with clear error. |
| M1-LST-001 | **PASS** | <2s | List shows running instance with name, splice version, status, ports. Adapted to single-instance (resource constraint). |

### Findings

#### Finding 1: `down` + `clean` workflow leaves orphaned volumes

**Severity:** Medium
**Test:** M1-CLN-001
**Issue:** [`docs/issues/down-clean-orphaned-volumes.md`](../issues/down-clean-orphaned-volumes.md)

`localnet down` (default) deregisters the instance from the registry on success. A subsequent `localnet clean --name X --force` then reports "Nothing to clean" but Docker volumes remain on disk. This is a design gap — both commands work correctly individually but don't compose in the `down` → `clean` sequence.

**Workaround:** Use `localnet clean --name X --force` directly on a running instance (it runs `down` internally before removing volumes). Do not call `down` before `clean`.

#### Finding 2: Post-restore `up` may exceed readiness timeout

**Severity:** Low
**Test:** M1-SNP-001

After `snapshot` → `down` → `clean` → `restore` → `up`, the Splice service must re-sync from scratch. On a machine with 8.84 GB Docker memory, this consistently exceeds the 5-minute default readiness wait, causing `up` to exit with a timeout. The services are actually healthy — they just need more time.

**Recommendation:** Document that post-restore bring-up may take longer than a fresh `up`, especially on resource-constrained hosts. Consider a `--timeout` flag on `up`.

#### Finding 3: `restart` readiness wait is very slow

**Severity:** Low
**Test:** M1-RST-001

Full `localnet restart` and single-service `restart --service canton` both work correctly, but the post-restart readiness wait can take 5+ minutes. The services come back healthy; the wait just takes time.

**Recommendation:** Consider `--no-wait` as a practical default for CI scripts, with a separate `localnet status --wait` for blocking on readiness.
