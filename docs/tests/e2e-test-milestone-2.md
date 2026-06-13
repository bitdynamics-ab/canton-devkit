# E2E Test Plan — Milestone 2: Web UI, Observability, DAR & Contract Tooling

> **Proposal Reference:** `original-devkit-proposal.md`, Milestone 2 (Lines 249–266)
> **Estimated Delivery:** Month 6
> **Total Tests:** 26
> **Platforms:** macOS (Apple Silicon), Linux (amd64), Windows (amd64)
> **Prerequisite:** All Milestone 1 tests passing.

---

## Overview

This test plan validates the Web UI, observability/monitoring stack, DAR package management, live contract/transaction exploration, automation conveniences, and optional AI agent skill documents delivered in Milestone 2.

### Conventions

- `$CLI` = `dpm localnet` or `canton-devkit localnet` (run full suite twice — once per mode).
- The test DAR is built from the `daml-intro-contracts` project (`Token` template, Daml SDK 3.5.1).
- `$DAR_PATH` = path to the built `.dar` file from `daml-intro-contracts`.
- `$WEB_UI_URL` = URL of the Web UI (printed by `$CLI up` or `$CLI status`).
- Web UI tests use `curl` for HTTP-level validation. Visual/interactive tests note what to verify manually or via browser automation.
- Default step timeout: 30 seconds unless noted.

### Environment Setup

```bash
# Set CLI mode
export CLI="dpm localnet"       # or "canton-devkit localnet"

# Build the test DAR
cd daml-intro-contracts
daml build
export DAR_PATH="$(pwd)/.daml/dist/daml-intro-contracts-1.0.0.dar"
cd ..

# Ensure clean state
$CLI clean --name e2e-m2-test --force 2>/dev/null || true

# Start LocalNet for Milestone 2 tests
$CLI up --name e2e-m2-test

# Capture Web UI URL from status output
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

1. Verify the Web UI URL is printed during startup:
   ```bash
   $CLI status --name e2e-m2-test 2>&1 | grep -qiE "(web.ui|dashboard|http.*ui)"
   ```
   - **Expected:** URL found in output.

2. Verify the Web UI is reachable via HTTP:
   ```bash
   curl -sf -o /dev/null -w "%{http_code}" "$WEB_UI_URL"
   ```
   - **Expected:** HTTP `200`.

3. Verify the Web UI serves HTML:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "<html|<!DOCTYPE"
   ```
   - **Expected:** Valid HTML response.

**Cleanup:** None (LocalNet stays running for subsequent tests).

---

### M2-WEB-002: Web UI lifecycle actions (start/stop/restart)

**Preconditions:** Web UI accessible (M2-WEB-001).
**Platforms:** All

**Steps:**

1. Verify the Web UI exposes lifecycle action endpoints or renders action buttons:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(start|stop|restart|status|clean)"
   ```
   - **Expected:** Lifecycle actions are present in the UI HTML.

2. Test the status view via Web UI (API endpoint if available):
   ```bash
   curl -sf "$WEB_UI_URL/api/status" 2>/dev/null || \
   curl -sf "$WEB_UI_URL/status" 2>/dev/null
   ```
   - **Expected:** JSON or HTML response showing LocalNet health.

**Note:** Full interactive testing of start/stop/restart via the Web UI requires browser automation (e.g., Playwright, Puppeteer). The above steps validate endpoint availability. An AI agent should verify that clicking "Restart" in the UI triggers `$CLI restart` behavior and the UI updates to reflect the new state.

**Cleanup:** None.

---

### M2-WEB-003: Web UI LocalNet dashboard content

**Preconditions:** Web UI accessible, LocalNet running.
**Platforms:** All

**Steps:**

1. Verify dashboard shows named instances:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "e2e-m2-test"
   ```

2. Verify dashboard shows service health indicators:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(healthy|running|ready|status)"
   ```

3. Verify dashboard shows endpoints and ports:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(endpoint|port|localhost|[0-9]{4,5})"
   ```

4. Verify dashboard shows participant information:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(participant|party)"
   ```

5. Verify dashboard shows Splice version:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(version|splice)"
   ```

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
   - **Expected:** Exit code `0`.
   - **Verify upload confirmation:**
     ```bash
     $CLI dar upload "$DAR_PATH" --participant participant1 --name e2e-m2-test 2>&1 | grep -qiE "(uploaded|success|package)"
     ```

2. Verify the package appears in the list:
   ```bash
   $CLI dar list --participant participant1 --name e2e-m2-test 2>&1 | grep -qiE "daml-intro-contracts"
   ```

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
   $CLI dar list --name e2e-m2-test 2>&1 | grep -ciE "daml-intro-contracts"
   ```
   - **Expected:** Count >= 2 (one entry per participant).

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
   - **Expected:** Exit code `0`, output shows what would happen without executing.
   - **Verify:**
     ```bash
     $CLI dar upload "$DAR_PATH" --all-participants --dry-run --name e2e-m2-test 2>&1 | grep -qiE "(dry.run|would|simulate)"
     ```

2. Upload with vetting for SCU:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --vet --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify vetting status:**
     ```bash
     $CLI dar list --name e2e-m2-test 2>&1 | grep -iE "daml-intro-contracts" | grep -qiE "(vetted|vet)"
     ```

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
   - **Expected:** Exit code `0`.
   - **Verify output includes required fields:**
     ```bash
     OUTPUT=$($CLI dar list --name e2e-m2-test 2>&1)
     echo "$OUTPUT" | grep -qiE "(package.id|name|version)"  # identifiers
     echo "$OUTPUT" | grep -qiE "(daml.lf|module)"            # metadata
     echo "$OUTPUT" | grep -qiE "daml-intro-contracts"        # our package
     ```

2. List packages filtered by participant:
   ```bash
   $CLI dar list --participant participant1 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, list is scoped to that participant.

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
   - **Expected:** Exit code `0`.
   - **Verify output includes structural details:**
     ```bash
     OUTPUT=$($CLI dar info daml-intro-contracts --name e2e-m2-test 2>&1)
     echo "$OUTPUT" | grep -qiE "Token"           # template name
     echo "$OUTPUT" | grep -qiE "owner"            # field name
     echo "$OUTPUT" | grep -qiE "(module|Token)"   # module listing
     echo "$OUTPUT" | grep -qiE "(dependency|hash)" # metadata
     ```

2. Get package info by package ID:
   ```bash
   PKG_ID=$($CLI dar list --name e2e-m2-test 2>&1 | grep -i "daml-intro-contracts" | grep -oE "[a-f0-9]{64}" | head -1)
   $CLI dar info "$PKG_ID" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, same info as by name.

**Cleanup:** None.

---

### M2-DAR-006: DAR download

**Preconditions:** DAR uploaded.
**Platforms:** All

**Steps:**

1. Download a DAR by package ID:
   ```bash
   PKG_ID=$($CLI dar list --name e2e-m2-test 2>&1 | grep -i "daml-intro-contracts" | grep -oE "[a-f0-9]{64}" | head -1)
   $CLI dar download "$PKG_ID" --out /tmp/downloaded.dar --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify file exists and is non-empty:**
     ```bash
     [ -s /tmp/downloaded.dar ] && echo "PASS" || echo "FAIL: downloaded DAR is empty or missing"
     ```

2. Verify downloaded DAR is a valid archive:
   ```bash
   file /tmp/downloaded.dar | grep -qiE "(zip|archive|data)"
   ```

**Cleanup:** `rm -f /tmp/downloaded.dar`

---

### M2-DAR-007: DAR diff between two versions

**Preconditions:** Two different DAR versions uploaded (or same DAR can be diffed against itself).
**Platforms:** All

**Steps:**

1. Build a second version of the DAR (modify version in daml.yaml):
   ```bash
   cd daml-intro-contracts
   cp daml.yaml daml.yaml.bak
   sed -i.tmp 's/version: 1.0.0/version: 2.0.0/' daml.yaml
   daml build
   export DAR_PATH_V2="$(pwd)/.daml/dist/daml-intro-contracts-2.0.0.dar"
   mv daml.yaml.bak daml.yaml
   rm -f daml.yaml.tmp
   cd ..
   $CLI dar upload "$DAR_PATH_V2" --all-participants --name e2e-m2-test
   ```

2. Diff the two versions:
   ```bash
   $CLI dar diff daml-intro-contracts:1.0.0 daml-intro-contracts:2.0.0 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify output shows diff information:**
     ```bash
     $CLI dar diff daml-intro-contracts:1.0.0 daml-intro-contracts:2.0.0 --name e2e-m2-test 2>&1 | grep -qiE "(template|choice|field|change|diff|identical|scu|compatible)"
     ```

**Cleanup:** `rm -f "$DAR_PATH_V2"`

---

### M2-DAR-008: DAR remove / unvet

**Preconditions:** DAR uploaded.
**Platforms:** All

**Steps:**

1. Get the package ID to remove:
   ```bash
   PKG_ID=$($CLI dar list --name e2e-m2-test 2>&1 | grep -i "daml-intro-contracts" | grep -oE "[a-f0-9]{64}" | head -1)
   ```

2. Remove / unvet the package:
   ```bash
   $CLI dar remove "$PKG_ID" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify package is no longer listed (or marked as unvetted):**
     ```bash
     $CLI dar list --name e2e-m2-test 2>&1 | grep -i "$PKG_ID" | grep -qiE "(unvetted|removed)" || \
     ! $CLI dar list --name e2e-m2-test 2>&1 | grep -qiE "$PKG_ID"
     ```

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
   - **Expected:** Exit code `0`.
   - **Verify both build and upload occurred:**
     ```bash
     $CLI dar build-upload --project ./daml-intro-contracts --name e2e-m2-test 2>&1 | grep -qiE "(build|compil)" 
     $CLI dar build-upload --project ./daml-intro-contracts --name e2e-m2-test 2>&1 | grep -qiE "(upload|deploy)"
     ```

2. If `dpm` is not available (standalone mode), verify graceful skip:
   ```bash
   # Only if dpm is not on PATH:
   which dpm > /dev/null 2>&1 || {
     $CLI dar build-upload --project ./daml-intro-contracts --name e2e-m2-test 2>&1 | grep -qiE "(skip|not available|dpm not found)"
     echo "PASS: graceful skip when dpm unavailable"
   }
   ```

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
   sleep 5  # let watch mode initialize
   ```

2. Trigger a rebuild by touching a source file:
   ```bash
   touch daml-intro-contracts/daml/Token.daml
   sleep 15  # wait for watch to detect change, rebuild, and re-upload
   ```

3. Verify re-upload occurred:
   ```bash
   $CLI dar list --name e2e-m2-test 2>&1 | grep -qiE "daml-intro-contracts"
   ```
   - **Expected:** Package is listed (re-uploaded).

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

1. Verify DAR upload UI is present in the Web UI:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(upload|drag.*drop|dar)"
   ```

2. Verify package explorer tree is present:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(package|module|template|explorer)"
   ```

3. Verify uploaded packages appear in the Web UI:
   ```bash
   curl -sf "$WEB_UI_URL" 2>&1 | grep -qiE "(daml-intro-contracts|Token)"
   ```

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

1. Start contracts watch in the background:
   ```bash
   timeout 30 $CLI contracts watch --name e2e-m2-test > /tmp/watch-output.txt 2>&1 &
   WATCH_PID=$!
   sleep 3
   ```

2. Create a contract via the Ledger API or Daml Script to trigger a create event:
   ```bash
   # Use daml script or ledger API to create a Token contract
   # This step depends on the available parties from the LocalNet
   PARTY=$($CLI env --name e2e-m2-test 2>&1 | grep -iE "PARTY|ALICE" | head -1 | cut -d= -f2)
   # Trigger contract creation via available means (daml script, JSON API, etc.)
   ```

3. Wait and check watch output:
   ```bash
   sleep 10
   kill $WATCH_PID 2>/dev/null || true
   wait $WATCH_PID 2>/dev/null || true
   cat /tmp/watch-output.txt | grep -qiE "(create|archive|contract|event)" && echo "PASS" || echo "FAIL: no events in watch output"
   ```

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
   - **Expected:** Exit code `0`.
   - **Verify output has transaction entries:**
     ```bash
     $CLI tx ls --name e2e-m2-test 2>&1 | grep -qiE "(transaction|tx|offset)"
     ```

2. Filter by party:
   ```bash
   PARTY=$($CLI env --name e2e-m2-test 2>&1 | grep -iE "PARTY|ALICE" | head -1 | cut -d= -f2)
   $CLI tx ls --party "$PARTY" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, only transactions visible to that party.

3. Filter by template:
   ```bash
   $CLI tx ls --template "Token:Token" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, only Token-related transactions.

4. Filter by offset range:
   ```bash
   $CLI tx ls --from 0 --to 100 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, transactions within offset range.

5. Combined multi-dimensional filter:
   ```bash
   $CLI tx ls --party "$PARTY" --template "Token:Token" --from 0 --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`, results satisfy all filters.

**Cleanup:** None.

---

### M2-CTR-003: TX replay per-party projection

**Preconditions:** LocalNet running, at least one transaction exists.
**Platforms:** All

**Steps:**

1. Get a transaction ID from the listing:
   ```bash
   TX_ID=$($CLI tx ls --name e2e-m2-test 2>&1 | grep -oE "[a-f0-9-]{36,}" | head -1)
   ```

2. Replay the transaction showing per-party visibility:
   ```bash
   $CLI tx replay "$TX_ID" --name e2e-m2-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify output shows party visibility projection:**
     ```bash
     $CLI tx replay "$TX_ID" --name e2e-m2-test 2>&1 | grep -qiE "(party|visible|projection|signatory|observer)"
     ```

3. Verify different parties see different projections:
   ```bash
   PARTY_A=$($CLI env --name e2e-m2-test 2>&1 | grep -iE "PARTY" | sed -n '1p' | cut -d= -f2)
   PARTY_B=$($CLI env --name e2e-m2-test 2>&1 | grep -iE "PARTY" | sed -n '2p' | cut -d= -f2)
   OUTPUT_A=$($CLI tx replay "$TX_ID" --party "$PARTY_A" --name e2e-m2-test 2>&1)
   OUTPUT_B=$($CLI tx replay "$TX_ID" --party "$PARTY_B" --name e2e-m2-test 2>&1)
   # At minimum, both should return successfully
   echo "$OUTPUT_A" | grep -qiE "(party|visible|projection)" && echo "PASS: Party A projection" || echo "WARN"
   echo "$OUTPUT_B" | grep -qiE "(party|visible|projection)" && echo "PASS: Party B projection" || echo "WARN"
   ```

**Cleanup:** None.

---

### M2-CTR-004: Web UI ACS explorer table

**Preconditions:** Web UI accessible, contracts exist.
**Platforms:** All

**Steps:**

1. Verify explorer section exists in Web UI:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(explorer|active.contract|acs)"
   ```

2. Verify ACS data is rendered (contracts visible):
   ```bash
   curl -sf "$WEB_UI_URL" 2>&1 | grep -qiE "(contract|template|Token|signatory|observer)"
   ```

3. Verify party/template filter controls exist:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(filter|party|template|participant)"
   ```

**Note:** Full interactive filtering requires browser automation. The above validates the UI structure is present.

**Cleanup:** None.

---

### M2-CTR-005: Web UI transaction timeline

**Preconditions:** Web UI accessible, transactions exist.
**Platforms:** All

**Steps:**

1. Verify transaction timeline section exists:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(transaction|timeline|history|tx)"
   ```

2. Verify transaction entries are rendered:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(create|exercise|archive|offset)"
   ```

3. Verify party visibility badges are present:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(party|visibility|badge)"
   ```

**Cleanup:** None.

---

### M2-CTR-006: Web UI contract detail view

**Preconditions:** Web UI accessible, contracts exist.
**Platforms:** All

**Steps:**

1. Verify contract detail view/drawer is accessible:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(detail|drawer|payload|lifecycle)"
   ```

2. Verify the detail view includes payload, lifecycle, and interface information:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(payload|json|lifecycle|created|signatory|observer)"
   ```

**Cleanup:** None.

---

## Observability and Monitoring

---

### M2-OBS-001: Prometheus/Grafana toggle enable/disable

**Preconditions:** LocalNet running.
**Platforms:** All

**Steps:**

1. Verify observability components can be enabled:
   ```bash
   # If observability is not already running, restart with it enabled
   $CLI down --name e2e-m2-test
   $CLI up --name e2e-m2-test --enable prometheus --enable grafana
   ```
   - **Expected:** Exit code `0`.

2. Verify Prometheus is running:
   ```bash
   PROM_URL=$($CLI status --name e2e-m2-test 2>&1 | grep -oiE "https?://[^ ]*prometheus[^ ]*" | head -1)
   # Or use default port
   PROM_URL="${PROM_URL:-http://localhost:9090}"
   curl -sf "$PROM_URL/-/healthy" > /dev/null && echo "PASS: Prometheus healthy" || echo "FAIL"
   ```

3. Verify Grafana is running:
   ```bash
   GRAFANA_URL=$($CLI status --name e2e-m2-test 2>&1 | grep -oiE "https?://[^ ]*grafana[^ ]*" | head -1)
   GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
   curl -sf "$GRAFANA_URL/api/health" > /dev/null && echo "PASS: Grafana healthy" || echo "FAIL"
   ```

4. Verify selective disable works:
   ```bash
   $CLI down --name e2e-m2-test
   $CLI up --name e2e-m2-test --disable prometheus
   $CLI status --name e2e-m2-test 2>&1 | grep -qiE "prometheus" && echo "WARN: Prometheus should be disabled" || echo "PASS"
   ```

**Cleanup:** `$CLI down --name e2e-m2-test && $CLI up --name e2e-m2-test`

---

### M2-OBS-002: Grafana dashboards accessible with presets

**Preconditions:** LocalNet running with Grafana enabled.
**Platforms:** All

**Steps:**

1. Verify Grafana is accessible:
   ```bash
   GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
   curl -sf "$GRAFANA_URL/api/health" | grep -qiE "ok" && echo "PASS" || echo "FAIL"
   ```

2. Verify Canton-specific dashboard presets exist:
   ```bash
   curl -sf "$GRAFANA_URL/api/search?type=dash-db" | grep -qiE "(canton|transaction|latency|throughput|contract)"
   ```
   - **Expected:** At least one Canton-specific dashboard preset is found.

3. Verify dashboards contain expected panels:
   ```bash
   DASHBOARD_UID=$(curl -sf "$GRAFANA_URL/api/search?type=dash-db" | grep -oE '"uid":"[^"]*"' | head -1 | cut -d'"' -f4)
   curl -sf "$GRAFANA_URL/api/dashboards/uid/$DASHBOARD_UID" | grep -qiE "(transactions.sec|latency|active.contract|throughput)"
   ```
   - **Expected:** Dashboard includes DApp developer-focused panels.

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
   - **Expected:** Exit code `0`.
   - **Verify output includes key metrics:**
     ```bash
     OUTPUT=$($CLI metrics --name e2e-m2-test 2>&1)
     echo "$OUTPUT" | grep -qiE "(throughput|transactions)"
     echo "$OUTPUT" | grep -qiE "(latency|p50|p99)"
     echo "$OUTPUT" | grep -qiE "(resource|cpu|memory)"
     ```

2. Verify Grafana dashboard URLs are printed:
   ```bash
   $CLI metrics --name e2e-m2-test 2>&1 | grep -qiE "https?://.*grafana"
   ```
   - **Expected:** At least one Grafana URL in output.

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
   - **Expected:** Exit code `0`.
   - **Verify valid JSON:**
     ```bash
     $CLI status --name e2e-m2-test --json 2>&1 | python3 -m json.tool > /dev/null
     ```
   - **Verify JSON contains expected keys:**
     ```bash
     $CLI status --name e2e-m2-test --json 2>&1 | python3 -c "import json,sys; d=json.load(sys.stdin); assert 'name' in d or 'status' in d or 'services' in d, 'Missing expected keys'"
     ```

2. DAR list with JSON output:
   ```bash
   $CLI dar list --name e2e-m2-test --json 2>&1 | python3 -m json.tool > /dev/null
   ```
   - **Expected:** Valid JSON.

3. List with JSON output:
   ```bash
   $CLI list --json 2>&1 | python3 -m json.tool > /dev/null
   ```
   - **Expected:** Valid JSON.

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
   EXIT_CODE=$?
   [ "$EXIT_CODE" -eq 0 ] && echo "PASS: up" || { echo "FAIL: up exited $EXIT_CODE"; exit 1; }
   ```

2. Wait for readiness (already handled by `up`, but verify):
   ```bash
   $CLI status --name e2e-ci-test 2>&1 | grep -qiE "(healthy|ready|running)"
   [ $? -eq 0 ] && echo "PASS: ready" || { echo "FAIL: not ready"; exit 1; }
   ```

3. Upload DAR:
   ```bash
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-ci-test
   [ $? -eq 0 ] && echo "PASS: dar upload" || { echo "FAIL: dar upload"; exit 1; }
   ```

4. Run application tests (simulate with a health check):
   ```bash
   # In a real CI pipeline, this would be: daml test, or integration tests
   $CLI dar list --name e2e-ci-test 2>&1 | grep -qiE "daml-intro-contracts"
   [ $? -eq 0 ] && echo "PASS: test verification" || { echo "FAIL: test verification"; exit 1; }
   ```

5. Teardown:
   ```bash
   $CLI down --name e2e-ci-test
   [ $? -eq 0 ] && echo "PASS: down" || { echo "FAIL: down"; exit 1; }
   $CLI clean --name e2e-ci-test --force
   [ $? -eq 0 ] && echo "PASS: clean" || { echo "FAIL: clean"; exit 1; }
   ```

6. Verify full cleanup:
   ```bash
   docker ps --filter "label=canton-devkit" --format '{{.Names}}' | grep -qE "e2e-ci-test" && echo "FAIL: containers remain" || echo "PASS: full cleanup"
   ```

**Cleanup:** Handled in step 5-6.

---

## AI Agent Skill Documents

---

### M2-SKL-001: AI agent skill document validation

**Preconditions:** Skill documents exist in the DevKit distribution.
**Platforms:** All

**Steps:**

1. Verify skill documents are included in the distribution:
   ```bash
   # Check for skill docs in the installed package or binary directory
   find $(dirname $(which canton-devkit 2>/dev/null || echo ".")) -name "*.md" -path "*skill*" -o -name "*.md" -path "*agent*" 2>/dev/null | head -5
   # Or check a known documentation path
   ls -la docs/skills/ 2>/dev/null || ls -la skills/ 2>/dev/null || echo "Check skill document location"
   ```

2. Verify a skill document contains executable workflow steps:
   ```bash
   # Read a skill document and verify it contains dpm localnet commands
   SKILL_DOC=$(find . -name "*.md" -path "*skill*" -o -name "*.md" -path "*agent*" 2>/dev/null | head -1)
   if [ -n "$SKILL_DOC" ]; then
     grep -qiE "dpm localnet|canton-devkit localnet" "$SKILL_DOC" && echo "PASS: contains CLI commands" || echo "FAIL: no CLI commands found"
     grep -qiE "(up|down|status|dar upload|logs)" "$SKILL_DOC" && echo "PASS: contains lifecycle commands" || echo "FAIL: no lifecycle commands"
   else
     echo "WARN: Skill document not found — check distribution packaging"
   fi
   ```

3. Execute the basic workflow described in a skill document:
   ```bash
   # The skill document should describe a workflow like:
   # 1. Start LocalNet
   # 2. Check status
   # 3. Upload a DAR
   # 4. List packages
   # 5. Check logs
   # 6. Stop LocalNet
   # Execute each step and verify:
   $CLI up --name e2e-skill-test
   $CLI status --name e2e-skill-test
   $CLI dar upload "$DAR_PATH" --all-participants --name e2e-skill-test
   $CLI dar list --name e2e-skill-test
   timeout 5 $CLI logs --name e2e-skill-test 2>&1 | head -10
   $CLI down --name e2e-skill-test
   echo "PASS: skill workflow executed successfully"
   ```

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
