# E2E Test Plan — Milestone 2: Web UI, Observability, DAR & Contract Tooling

**Total Tests:** 26
**Platforms:** macOS (Apple Silicon), Linux (amd64), Windows (amd64)
**Prerequisite:** All Milestone 1 tests passing.

---

## Overview

This test plan validates the Web UI, observability/monitoring stack, DAR package management, live contract/transaction exploration, automation conveniences, and optional AI agent skill documents delivered in Milestone 2.

### Conventions

- `$CLI` = `dpm localnet` or `canton-devkit localnet` (run full suite twice — once per mode).
- The test DAR is built from the `daml-intro-contracts` project (`Token` template, Daml SDK 3.5.1).
- `$DAR_PATH` = path to the built `.dar` file from `daml-intro-contracts`.
- `$WEB_UI_URL` = URL of the Web UI. Extract it from status output with shell parsing.
- Web UI tests use `curl` for HTTP-level validation. Visual/interactive tests note what to verify manually or via browser automation.
- Default step timeout: 30 seconds unless noted.

### Environment Setup

1. Set the CLI mode (`dpm localnet` or `canton-devkit localnet`).
2. Build the test DAR:
   ```bash
   cd daml-intro-contracts
   daml build
   # DAR is at .daml/dist/daml-intro-contracts-1.0.0.dar
   cd ..
   ```
3. Clean any prior state and start the LocalNet:
   ```bash
   $CLI clean --name e2e-m2-test --force 2>/dev/null || true
   $CLI up --name e2e-m2-test
   ```
4. Capture the Web UI URL for later steps:
   ```bash
   export WEB_UI_URL=$($CLI status --name e2e-m2-test 2>&1 | grep -oiE "https?://[^ ]*ui[^ ]*" | head -1)
   ```

---

## Test Cases

---

## Web UI

---

### M2-WEB-001: Web UI launches and is accessible

**Preconditions:** LocalNet `e2e-m2-test` running.
**Platforms:** All

**Steps:**

1. Verify the Web UI URL is printed in status output:
   ```bash
   $CLI status --name e2e-m2-test
   ```
   - **Expected:** Output contains a URL referencing the Web UI or dashboard.

2. Verify the Web UI is reachable via HTTP:
   ```bash
   curl -sf -o /dev/null -w "%{http_code}" "$WEB_UI_URL"
   ```
   - **Expected:** HTTP `200`.

3. Verify the Web UI serves HTML:
   ```bash
   curl -sf "$WEB_UI_URL"
   ```
   - **Expected:** Response body contains `<html` or `<!DOCTYPE`.

**Cleanup:** None (LocalNet stays running for subsequent tests).

---

### M2-WEB-002: Web UI lifecycle actions (start/stop/restart)

**Preconditions:** Web UI accessible (M2-WEB-001).
**Platforms:** All

**Steps:**

1. Verify the Web UI exposes lifecycle action elements:
   ```bash
   curl -sf "$WEB_UI_URL"
   ```
   - **Expected:** HTML contains references to `start`, `stop`, `restart`, `status`, or `clean`.

2. Test the status view via Web UI:
   ```bash
   curl -sf "$WEB_UI_URL/api/status" 2>/dev/null || curl -sf "$WEB_UI_URL/status" 2>/dev/null
   ```
   - **Expected:** JSON or HTML response showing LocalNet health.

**Note:** Full interactive testing of start/stop/restart via the Web UI requires browser automation. The above steps validate endpoint availability. Verify that clicking "Restart" in the UI triggers restart behavior and the UI updates to reflect the new state.

**Cleanup:** None.

---

### M2-WEB-003: Web UI LocalNet dashboard content

**Preconditions:** Web UI accessible, LocalNet running.
**Platforms:** All

**Steps:**

1. Fetch the dashboard:
   ```bash
   curl -sf "$WEB_UI_URL"
   ```
   Verify the response contains each of the following:
   - The instance name `e2e-m2-test`.
   - Service health indicators: `healthy`, `running`, or `ready`.
   - Endpoint or port information: `endpoint`, `port`, or a 4–5 digit number.
   - Participant information: `participant` or `party`.
   - Splice version information: `version` or `splice`.

**Cleanup:** None.

---

## DAR Management

---

### M2-DAR-001: DAR upload to single participant

**Preconditions:** LocalNet running, `$DAR_PATH` exists.
**Platforms:** All

**Steps:**

1. Upload DAR to a single participant:
   ```bash
   $CLI dar upload "$DAR_PATH" --participant participant1 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains `uploaded`, `success`, or `package`.

2. Verify the package appears in the list:
   ```bash
   $CLI dar list --participant participant1 --name e2e-m2-test
   ```
   - **Expected:** Output contains `daml-intro-contracts`.

**Cleanup:** None (package remains for subsequent tests).

---

### M2-DAR-002: DAR upload to all participants

**Preconditions:** LocalNet running, `$DAR_PATH` exists.
**Platforms:** All

**Steps:**

1. Upload DAR to all participants:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.

2. Verify package is listed on multiple participants:
   ```bash
   $CLI dar list --name e2e-m2-test
   ```
   - **Expected:** `daml-intro-contracts` appears at least twice — once per participant.

**Cleanup:** None.

---

### M2-DAR-003: DAR upload with --vet and --dry-run

**Preconditions:** LocalNet running, `$DAR_PATH` exists.
**Platforms:** All

**Steps:**

1. Dry-run upload (should not actually upload):
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --dry-run --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output indicates what would happen without executing (e.g., contains `dry-run`, `would`, or `simulate`).

2. Upload with vetting for SCU:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --vet --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. The `daml-intro-contracts` entry in `$CLI dar list` shows `vetted` or `vet`.

**Cleanup:** None.

---

### M2-DAR-004: DAR list packages

**Preconditions:** DAR uploaded (M2-DAR-001 or M2-DAR-002).
**Platforms:** All

**Steps:**

1. List packages:
   ```bash
   $CLI dar list --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains:
     - Package identifiers: `package-id`, `name`, or `version`.
     - Metadata: `daml-lf` or `module`.
     - The uploaded package: `daml-intro-contracts`.

2. List packages filtered by participant:
   ```bash
   $CLI dar list --participant participant1 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. List is scoped to that participant.

**Cleanup:** None.

---

### M2-DAR-005: DAR info (modules, templates, choices)

**Preconditions:** DAR uploaded.
**Platforms:** All

**Steps:**

1. Get package info by name:
   ```bash
   $CLI dar info daml-intro-contracts --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains the template name `Token`, the field name `owner`, module listing, and dependency or hash metadata.

2. Get package info by package ID — obtain the package ID from `$CLI dar list`, then run:
   ```bash
   $CLI dar info "<package-id>" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Same information as by name.

**Cleanup:** None.

---

### M2-DAR-006: DAR download

**Preconditions:** DAR uploaded.
**Platforms:** All

**Steps:**

1. Obtain the package ID from `$CLI dar list --name e2e-m2-test` (64-character hex string next to `daml-intro-contracts`).

2. Download the DAR:
   ```bash
   $CLI dar download "<package-id>" --out /tmp/downloaded.dar --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. File `/tmp/downloaded.dar` exists and is non-empty.

3. Verify the downloaded file is a valid archive:
   ```bash
   file /tmp/downloaded.dar
   ```
   - **Expected:** Output contains `zip`, `archive`, or `data`.

**Cleanup:** `rm -f /tmp/downloaded.dar`

---

### M2-DAR-007: DAR diff between two versions

**Preconditions:** Two different DAR versions uploaded (or same DAR can be diffed against itself).
**Platforms:** All

**Steps:**

1. Build a second version of the DAR by editing `version: 1.0.0` to `version: 2.0.0` in `daml-intro-contracts/daml.yaml`, rebuilding with `daml build`, then uploading the new DAR:
   ```bash
   $CLI dar upload .daml/dist/daml-intro-contracts-2.0.0.dar --all-participants --name e2e-m2-test
   ```

2. Diff the two versions:
   ```bash
   $CLI dar diff daml-intro-contracts:1.0.0 daml-intro-contracts:2.0.0 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output describes changes or confirms identical content — contains one of `template`, `choice`, `field`, `change`, `diff`, `identical`, `scu`, or `compatible`.

**Cleanup:** Remove the v2 DAR file built in step 1.

---

### M2-DAR-008: DAR remove / unvet

**Preconditions:** DAR uploaded.
**Platforms:** All

**Steps:**

1. Obtain the package ID from `$CLI dar list --name e2e-m2-test`.

2. Remove / unvet the package:
   ```bash
   $CLI dar remove "<package-id>" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Running `$CLI dar list` afterward shows the package is either absent or marked `unvetted` or `removed`.

3. Re-upload for subsequent tests:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-m2-test
   ```

**Cleanup:** None.

---

### M2-DAR-009: DAR build-upload (dpm build integration)

**Preconditions:** `dpm` available (skip if standalone mode and `dpm` not installed), `daml-intro-contracts` project.
**Platforms:** All

**Steps:**

1. Run build-upload from the project directory:
   ```bash
   $CLI dar build-upload --project ./daml-intro-contracts --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output mentions both build/compile and upload/deploy steps.

2. If `dpm` is not available (standalone mode), verify graceful skip:
   ```bash
   which dpm > /dev/null 2>&1 || $CLI dar build-upload --project ./daml-intro-contracts --name e2e-m2-test
   ```
   - **Expected:** Output indicates the command was skipped or that `dpm` is not available.

**Cleanup:** None.

---

### M2-DAR-010: DAR watch mode (hot-deploy)

**Preconditions:** LocalNet running, `daml-intro-contracts` project.
**Platforms:** All
**Timeout:** 60 seconds

**Steps:**

1. Start watch mode in the background:
   ```bash
   $CLI dar watch ./daml-intro-contracts --name e2e-m2-test &
   WATCH_PID=$!
   sleep 5
   ```

2. Trigger a rebuild by touching a source file:
   ```bash
   touch daml-intro-contracts/daml/Token.daml
   sleep 15
   ```

3. Verify re-upload occurred:
   ```bash
   $CLI dar list --name e2e-m2-test
   ```
   - **Expected:** `daml-intro-contracts` is listed (re-uploaded after file change).

4. Stop watch mode:
   ```bash
   kill $WATCH_PID 2>/dev/null || true
   wait $WATCH_PID 2>/dev/null || true
   ```

**Cleanup:** Watch process killed in step 4.

---

### M2-DAR-011: Web UI DAR drag-and-drop + package explorer

**Preconditions:** Web UI accessible, DAR uploaded.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - A DAR upload section: `upload`, `drag`, `drop`, or `dar`.
   - A package explorer tree: `package`, `module`, `template`, or `explorer`.
   - The uploaded package: `daml-intro-contracts` or `Token`.

**Note:** Drag-and-drop upload and package tree navigation require browser automation for full interactive testing. The above steps validate that the UI elements are rendered.

**Cleanup:** None.

---

## Contract Tracking & Exploration

---

### M2-CTR-001: Contracts watch (live streaming)

**Preconditions:** LocalNet running, DAR uploaded with `Token` template.
**Platforms:** All
**Timeout:** 60 seconds

**Steps:**

1. Start contracts watch in the background, redirecting output to a file:
   ```bash
   timeout 30 $CLI contracts watch --name e2e-m2-test > /tmp/watch-output.txt 2>&1 &
   WATCH_PID=$!
   sleep 3
   ```

2. Create a contract via the Ledger API or Daml Script to trigger a create event. The party to use is available from `$CLI env --name e2e-m2-test` — look for a `PARTY` or `ALICE` variable.

3. Wait and check watch output:
   ```bash
   sleep 10
   kill $WATCH_PID 2>/dev/null || true
   wait $WATCH_PID 2>/dev/null || true
   ```
   - **Expected:** `/tmp/watch-output.txt` contains `create`, `archive`, `contract`, or `event`.

**Cleanup:** `rm -f /tmp/watch-output.txt`

---

### M2-CTR-002: TX ls with multi-dimensional filters

**Preconditions:** LocalNet running, at least one transaction exists.
**Platforms:** All

**Steps:**

1. List all transactions:
   ```bash
   $CLI tx ls --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains `transaction`, `tx`, or `offset`.

2. Filter by party — obtain a party ID from `$CLI env --name e2e-m2-test`:
   ```bash
   $CLI tx ls --party "<party-id>" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Only transactions visible to that party.

3. Filter by template:
   ```bash
   $CLI tx ls --template "Token:Token" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Only Token-related transactions.

4. Filter by offset range:
   ```bash
   $CLI tx ls --from 0 --to 100 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Transactions within the offset range.

5. Combined multi-dimensional filter:
   ```bash
   $CLI tx ls --party "<party-id>" --template "Token:Token" --from 0 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Results satisfy all filters simultaneously.

**Cleanup:** None.

---

### M2-CTR-003: TX replay per-party projection

**Preconditions:** LocalNet running, at least one transaction exists.
**Platforms:** All

**Steps:**

1. Obtain a transaction ID from `$CLI tx ls --name e2e-m2-test` (UUID-style string).

2. Replay the transaction:
   ```bash
   $CLI tx replay "<tx-id>" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains `party`, `visible`, `projection`, `signatory`, or `observer`.

3. Obtain two party IDs from `$CLI env --name e2e-m2-test`. Replay with each party explicitly:
   ```bash
   $CLI tx replay "<tx-id>" --party "<party-a>" --name e2e-m2-test
   $CLI tx replay "<tx-id>" --party "<party-b>" --name e2e-m2-test
   ```
   - **Expected:** Both commands exit `0`. Each output shows the party's visibility projection.

**Cleanup:** None.

---

### M2-CTR-004: Web UI ACS explorer table

**Preconditions:** Web UI accessible, contracts exist.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - An explorer section: `explorer`, `active contract`, or `acs`.
   - Contract data: `contract`, `template`, `Token`, `signatory`, or `observer`.
   - Filter controls: `filter`, `party`, `template`, or `participant`.

**Note:** Full interactive filtering requires browser automation. The above validates the UI structure is present.

**Cleanup:** None.

---

### M2-CTR-005: Web UI transaction timeline

**Preconditions:** Web UI accessible, transactions exist.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - A transaction timeline section: `transaction`, `timeline`, `history`, or `tx`.
   - Transaction entries: `create`, `exercise`, `archive`, or `offset`.
   - Party visibility badges: `party`, `visibility`, or `badge`.

**Cleanup:** None.

---

### M2-CTR-006: Web UI contract detail view

**Preconditions:** Web UI accessible, contracts exist.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - A detail view or drawer: `detail`, `drawer`, `payload`, or `lifecycle`.
   - Payload and lifecycle information: `payload`, `json`, `lifecycle`, `created`, `signatory`, or `observer`.

**Cleanup:** None.

---

## Observability and Monitoring

---

### M2-OBS-001: Prometheus/Grafana toggle enable/disable

**Preconditions:** LocalNet running.
**Platforms:** All

**Steps:**

1. Restart LocalNet with observability enabled:
   ```bash
   $CLI down --name e2e-m2-test
   $CLI up --name e2e-m2-test --enable prometheus --enable grafana
   ```
   - **Expected:** Exit code `0`.

2. Obtain the Prometheus URL from `$CLI status --name e2e-m2-test` (or use the default `http://localhost:9090`). Verify Prometheus responds:
   ```bash
   curl -sf "<prometheus-url>/-/healthy"
   ```
   - **Expected:** HTTP `200`.

3. Obtain the Grafana URL from `$CLI status --name e2e-m2-test` (or use the default `http://localhost:3000`). Verify Grafana responds:
   ```bash
   curl -sf "<grafana-url>/api/health"
   ```
   - **Expected:** HTTP `200`.

4. Verify selective disable:
   ```bash
   $CLI down --name e2e-m2-test
   $CLI up --name e2e-m2-test --disable prometheus
   $CLI status --name e2e-m2-test
   ```
   - **Expected:** Status output does not reference Prometheus as running.

**Cleanup:** `$CLI down --name e2e-m2-test && $CLI up --name e2e-m2-test`

---

### M2-OBS-002: Grafana dashboards accessible with presets

**Preconditions:** LocalNet running with Grafana enabled.
**Platforms:** All

**Steps:**

1. Verify Grafana is accessible (use the URL from `$CLI status`):
   ```bash
   curl -sf "<grafana-url>/api/health"
   ```
   - **Expected:** Response body contains `ok`.

2. Verify Canton-specific dashboard presets exist:
   ```bash
   curl -sf "<grafana-url>/api/search?type=dash-db"
   ```
   - **Expected:** Response contains at least one dashboard name related to `canton`, `transaction`, `latency`, `throughput`, or `contract`.

3. Inspect a dashboard — obtain the `uid` from the search response and fetch the dashboard definition:
   ```bash
   curl -sf "<grafana-url>/api/dashboards/uid/<dashboard-uid>"
   ```
   - **Expected:** Response contains developer-focused panels: `transactions/sec`, `latency`, `active contract`, or `throughput`.

**Cleanup:** None.

---

### M2-OBS-003: Metrics CLI summary output

**Preconditions:** LocalNet running with Grafana enabled.
**Platforms:** All

**Steps:**

1. Run metrics command:
   ```bash
   $CLI metrics --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`. Output contains:
     - Throughput information: `throughput` or `transactions`.
     - Latency information: `latency`, `p50`, or `p99`.
     - Resource usage: `resource`, `cpu`, or `memory`.
     - At least one Grafana URL.

**Cleanup:** None.

---

## Automation Conveniences

---

### M2-AUT-001: Machine-readable --json output

**Preconditions:** LocalNet running.
**Platforms:** All

**Steps:**

1. Status with JSON output:
   ```bash
   $CLI status --name e2e-m2-test --json
   ```
   - **Expected:** Exit code `0`. Output is valid JSON containing at least one of `name`, `status`, or `services` as top-level keys.

2. DAR list with JSON output:
   ```bash
   $CLI dar list --name e2e-m2-test --json
   ```
   - **Expected:** Valid JSON output.

3. List with JSON output:
   ```bash
   $CLI list --json
   ```
   - **Expected:** Valid JSON output.

**Cleanup:** None.

---

### M2-AUT-002: CI workflow: up → DAR upload → test → down

**Preconditions:** Docker running, `daml-intro-contracts` project available.
**Platforms:** All
**Timeout:** 600 seconds

This test simulates a complete CI pipeline.

**Steps:**

1. Start LocalNet:
   ```bash
   $CLI up --name e2e-ci-test
   ```
   - **Expected:** Exit code `0`.

2. Verify readiness:
   ```bash
   $CLI status --name e2e-ci-test
   ```
   - **Expected:** Output contains `healthy`, `ready`, or `running`.

3. Upload DAR:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-ci-test
   ```
   - **Expected:** Exit code `0`.

4. Run application tests (simulate with a DAR list check):
   ```bash
   $CLI dar list --name e2e-ci-test
   ```
   - **Expected:** `daml-intro-contracts` is listed.

5. Teardown:
   ```bash
   $CLI down --name e2e-ci-test
   $CLI clean --name e2e-ci-test --force
   ```
   - **Expected:** Both commands exit `0`.

6. Verify full cleanup — `docker ps --filter "label=canton-devkit"` should show no containers named `e2e-ci-test`.

**Cleanup:** Handled in step 5.

---

## AI Agent Skill Documents

---

### M2-SKL-001: AI agent skill document validation

**Preconditions:** Skill documents exist in the DevKit distribution.
**Platforms:** All

**Steps:**

1. Verify skill documents are included in the distribution. Look for `.md` files under paths containing `skill` or `agent` in the installed package directory or alongside the binary.

2. Open a skill document and verify it contains:
   - At least one `dpm localnet` or `canton-devkit localnet` command.
   - Lifecycle commands: `up`, `down`, `status`, `dar upload`, or `logs`.

3. Execute the basic workflow described in a skill document:
   ```bash
   $CLI up --name e2e-skill-test
   $CLI status --name e2e-skill-test
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-skill-test
   $CLI dar list --name e2e-skill-test
   timeout 5 $CLI logs --name e2e-skill-test
   $CLI down --name e2e-skill-test
   ```
   - **Expected:** All commands exit `0`.

**Cleanup:** `$CLI clean --name e2e-skill-test --force 2>/dev/null || true`

---

## Cross-Platform Notes

| Platform | Special Considerations |
|---|---|
| **macOS (Apple Silicon)** | Docker Desktop required. Web UI accessible at `localhost`. Grafana/Prometheus default ports may conflict with local dev tools. |
| **Linux (amd64)** | Native Docker. Ensure firewall allows localhost port access for Web UI and observability stack. |
| **Windows (amd64)** | Docker Desktop with WSL 2. Web UI URL may differ (`localhost` vs WSL IP). `curl` available via WSL or PowerShell `Invoke-WebRequest`. `timeout` command replaced with PowerShell equivalent. |

---

## Test Execution Summary

| ID | Test Name | Category | Depends On |
|---|---|---|---|
| M2-WEB-001 | Web UI launches and is accessible | Web UI | M1 suite |
| M2-WEB-002 | Web UI lifecycle actions | Web UI | M2-WEB-001 |
| M2-WEB-003 | Web UI dashboard content | Web UI | M2-WEB-001 |
| M2-DAR-001 | DAR upload to single participant | DAR | M1 suite |
| M2-DAR-002 | DAR upload to all participants | DAR | M1 suite |
| M2-DAR-003 | DAR upload with --vet and --dry-run | DAR | M1 suite |
| M2-DAR-004 | DAR list packages | DAR | M2-DAR-001 |
| M2-DAR-005 | DAR info | DAR | M2-DAR-001 |
| M2-DAR-006 | DAR download | DAR | M2-DAR-001 |
| M2-DAR-007 | DAR diff between two versions | DAR | M2-DAR-001 |
| M2-DAR-008 | DAR remove / unvet | DAR | M2-DAR-001 |
| M2-DAR-009 | DAR build-upload | DAR | M1 suite |
| M2-DAR-010 | DAR watch mode | DAR | M1 suite |
| M2-DAR-011 | Web UI DAR + package explorer | DAR Web UI | M2-WEB-001, M2-DAR-001 |
| M2-CTR-001 | Contracts watch (live) | Contracts | M2-DAR-001 |
| M2-CTR-002 | TX ls multi-filter | Contracts | M2-DAR-001 |
| M2-CTR-003 | TX replay per-party | Contracts | M2-CTR-002 |
| M2-CTR-004 | Web UI ACS explorer | Contracts Web UI | M2-WEB-001 |
| M2-CTR-005 | Web UI transaction timeline | Contracts Web UI | M2-WEB-001 |
| M2-CTR-006 | Web UI contract detail | Contracts Web UI | M2-WEB-001 |
| M2-OBS-001 | Prometheus/Grafana toggle | Observability | M1 suite |
| M2-OBS-002 | Grafana dashboards with presets | Observability | M2-OBS-001 |
| M2-OBS-003 | Metrics CLI summary | Observability | M2-OBS-001 |
| M2-AUT-001 | Machine-readable --json output | Automation | M1 suite |
| M2-AUT-002 | CI workflow E2E | Automation | M1 suite |
| M2-SKL-001 | AI agent skill document validation | AI Skills | M1 suite |
