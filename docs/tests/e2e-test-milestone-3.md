# E2E Test Plan — Milestone 3: Token Faucets & Token Standard Tooling (CIP-0112)

> **Proposal Reference:** `original-devkit-proposal.md`, Milestone 3 (Lines 268–277)
> **Estimated Delivery:** Month 9
> **Total Tests:** 11
> **Platforms:** macOS (Apple Silicon), Linux (amd64), Windows (amd64)
> **Prerequisites:** All Milestone 1 and Milestone 2 tests passing.

---

## Overview

This test plan validates the CIP-0112 token standard tooling delivered in Milestone 3: the token creation wizard, minting, transfer, burn, and balance commands, the full token lifecycle E2E flow, edge cases, Web UI token toolkit, and cross-platform regression.

### Conventions

- `$CLI` = `dpm localnet` or `canton-devkit localnet` (run full suite twice — once per mode).
- Token commands target the CIP-0112 (V2) path as the default.
- Non-interactive flags are used for wizard-style commands to enable AI agent execution.
- `$WEB_UI_URL` = URL of the Web UI (from `$CLI status`).
- Default step timeout: 30 seconds unless noted.

### Environment Setup

```bash
# Set CLI mode
export CLI="dpm localnet"       # or "canton-devkit localnet"

# Ensure clean state
$CLI clean --name e2e-m3-test --force 2>/dev/null || true

# Start LocalNet for Milestone 3 tests
$CLI up --name e2e-m3-test

# Capture Web UI URL
export WEB_UI_URL=$($CLI status --name e2e-m3-test 2>&1 | grep -oiE "https?://[^ ]*ui[^ ]*" | head -1)
```

---

## Test Cases

---

### M3-TOK-001: Token create wizard (non-interactive)

**Preconditions:** LocalNet `e2e-m3-test` running. Party alias `tst-issuer` registered on `app-provider`:
```bash
$CLI token party new tst-issuer --instance e2e-m3-test
```
**Platforms:** All

**Steps:**

1. Create a new token using non-interactive flags (CIP-0112 path):
   ```bash
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "TestCoin" --symbol TST --decimals 8 \
     --initial-supply 1000000 --issuer tst-issuer
   ```
   - **Expected:** Exit code `0`.
   - **Verify creation confirmation:**
     ```bash
     $CLI token create --instance e2e-m3-test --non-interactive \
       --name "TestCoin" --symbol TST --decimals 8 \
       --initial-supply 1000000 --issuer tst-issuer 2>&1 \
       | grep -qiE "(created|TST|tst-issuer)"
     ```

2. Verify the token exists by checking balance:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument TST 2>&1 \
     | grep -qiE "(1000000|TST)"
   ```
   - **Expected:** Balance shows the initial supply.

3. Verify CIP-0112 alignment:
   ```bash
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "TestCoin" --symbol TST --decimals 8 \
     --initial-supply 1000000 --issuer tst-issuer 2>&1 \
     | grep -qiE "(cip.0112|v2|token.standard)"
   ```
   - **Expected:** Output references CIP-0112 / V2 path (or no V1 warnings).

**Cleanup:** None (token persists for subsequent tests).

---

### M3-TOK-002: Token mint

**Preconditions:** Token `TST` created (M3-TOK-001). Party alias `tst-holder` registered on `app-provider` (`$CLI token party new tst-holder --instance e2e-m3-test`).
**Platforms:** All

**Steps:**

1. Mint to `tst-holder`:
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument TST --to tst-holder --amount 500000
   ```
   - **Expected:** Exit code `0`, output includes `mint: accepted`.
   - **Verify:**
     ```bash
     $CLI token mint --instance e2e-m3-test \
       --instrument TST --to tst-holder --amount 500000 2>&1 \
       | grep -q "mint: accepted"
     ```

2. Verify balance on the holder:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Amount shows `500000.000000` (or the sum of all mints so far).
   - **Verify:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument TST --party tst-holder 2>&1 \
       | grep -qiE "500000"
     ```

3. Mint a second batch to confirm idempotent re-mint:
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument TST --to tst-holder --amount 100000
   ```
   - **Expected:** Exit code `0`, output includes `mint: accepted`.

**Cleanup:** None.

---

### M3-TOK-003: Token transfer

**Preconditions:** Token `TST` minted (M3-TOK-002). Party aliases `tst-issuer` (sender, app-provider) and `tst-holder` (receiver, app-provider) exist.
**Platforms:** All

**Steps:**

1. Transfer tokens from `tst-issuer` to `tst-holder`:
   ```bash
   $CLI token transfer --instance e2e-m3-test \
     --instrument TST --from tst-issuer --to tst-holder \
     --amount 250000 --auto-accept
   ```
   - **Expected:** Exit code `0`.
   - **Verify transfer confirmation:**
     ```bash
     $CLI token transfer --instance e2e-m3-test \
       --instrument TST --from tst-issuer --to tst-holder \
       --amount 250000 --auto-accept 2>&1 \
       | grep -qiE "(transfer|accepted|250000)"
     ```

2. Verify sender balance decreased:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-issuer 2>&1 | grep -oE "[0-9]+"
   ```
   - **Expected:** Sender balance reduced by 250000.

3. Verify receiver balance increased:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder 2>&1 | grep -qiE "250000"
   ```
   - **Expected:** Receiver shows at least 250000.

4. Attempt transfer with insufficient balance:
   ```bash
   $CLI token transfer --instance e2e-m3-test \
     --instrument TST --from tst-issuer --to tst-holder \
     --amount 999999999999
   ```
   - **Expected:** Non-zero exit code, error message about insufficient balance.

**Cleanup:** None.

---

### M3-TOK-004: Token burn

**Preconditions:** Token `TST` exists with `tst-holder` holding balance > 0 (M3-TOK-002 or M3-TOK-003).
**Platforms:** All

**Steps:**

1. Burn tokens from `tst-holder`:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount 100000 --yes
   ```
   - **Expected:** Exit code `0`.
   - **Verify burn confirmation:**
     ```bash
     $CLI token burn --instance e2e-m3-test \
       --instrument TST --from tst-holder --amount 100000 --yes 2>&1 \
       | grep -qiE "(burned|burnt|100000)"
     ```

2. Verify balance decreased after burn:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Balance reduced by 100000 from pre-burn value.

3. Attempt to burn more than available balance:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount 999999999999 --yes
   ```
   - **Expected:** Non-zero exit code, error message about insufficient balance.

**Cleanup:** None.

---

### M3-TOK-005: Token balance query

**Preconditions:** Token `TST` exists (M3-TOK-001). Party aliases `tst-issuer` and `tst-holder` exist.
**Platforms:** All

**Steps:**

1. Query balance for a specific party:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument TST --party tst-issuer
   ```
   - **Expected:** Exit code `0`.
   - **Verify output format:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument TST --party tst-issuer 2>&1 \
       | grep -qiE "(TST|[0-9]+)"
     ```

2. Query balance for a second party:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Exit code `0`, shows balance for `tst-holder`.

3. Query all balances for the instance (no instrument filter):
   ```bash
   $CLI token balance --instance e2e-m3-test
   ```
   - **Expected:** Exit code `0`, lists all instruments and their balances.

4. Query balance in JSON format:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --format json 2>&1 | grep -q '"amount"'
   ```
   - **Expected:** Exit code `0`, JSON output contains `"amount"` field.

**Cleanup:** None.

---

### M3-TOK-006: Full flow — create, mint, transfer, burn, balance

**Preconditions:** LocalNet `e2e-m3-test` running. Party aliases `e2e-sender` and `e2e-receiver` registered on `app-provider`:
```bash
$CLI token party new e2e-sender   --instance e2e-m3-test
$CLI token party new e2e-receiver --instance e2e-m3-test
```
**Platforms:** All

This test executes the complete token lifecycle in a single sequential flow, validating state after each step.

**Steps:**

1. **Create** a new token:
   ```bash
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "E2ECoin" --symbol E2E --decimals 6 \
     --initial-supply 0 --issuer e2e-sender
   ```
   - **Verify:** Exit code `0`, creation confirmed.
   - **Assert:**
     ```bash
     $CLI token balance --instance e2e-m3-test --instrument E2E 2>&1 | grep -qiE "^$|0"
     ```

2. **Mint** initial supply to sender:
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument E2E --to e2e-sender --amount 1000000
   ```
   - **Verify:** Exit code `0`, output includes `mint: accepted`.
   - **Assert:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument E2E --party e2e-sender 2>&1 | grep -q "1000000"
     ```

3. **Transfer** to receiver:
   ```bash
   $CLI token transfer --instance e2e-m3-test \
     --instrument E2E --from e2e-sender --to e2e-receiver \
     --amount 400000 --auto-accept
   ```
   - **Verify:** Exit code `0`.
   - **Assert sender balance = 600000:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument E2E --party e2e-sender 2>&1 | grep -q "600000"
     ```
   - **Assert receiver balance = 400000:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument E2E --party e2e-receiver 2>&1 | grep -q "400000"
     ```

4. **Burn** from sender:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument E2E --from e2e-sender --amount 100000 --yes
   ```
   - **Verify:** Exit code `0`.
   - **Assert sender balance = 500000:**
     ```bash
     $CLI token balance --instance e2e-m3-test \
       --instrument E2E --party e2e-sender 2>&1 | grep -q "500000"
     ```

5. **Final balance** check:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument E2E
   ```
   - **Assert sender:** `500000`.
   - **Assert receiver:** `400000`.
   - **Assert total supply:** `900000` (1000000 minted - 100000 burned).

6. **Ledger verification** — verify token operations created transactions:
   ```bash
   $CLI tx ls --template "E2E" --instance e2e-m3-test 2>&1 | wc -l
   ```
   - **Expected:** At least 4 transactions (create, mint, transfer, burn).

**Cleanup:** None (E2E instrument persists for reference).

---

### M3-TOK-007: Token balance after partial burn

**Preconditions:** Token `TST` exists with `tst-holder` holding balance > 0 (M3-TOK-002).
**Platforms:** All

**Steps:**

1. Record current balance:
   ```bash
   BEFORE=$($CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder 2>&1 | grep -oE "[0-9]+(\.[0-9]+)?" | head -1)
   echo "Balance before: $BEFORE"
   ```

2. Burn a small amount:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount 1 --yes
   ```
   - **Expected:** Exit code `0`.

3. Verify exact balance after partial burn:
   ```bash
   AFTER=$($CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder 2>&1 | grep -oE "[0-9]+(\.[0-9]+)?" | head -1)
   echo "Balance after: $AFTER (expected $BEFORE - 1)"
   ```

4. Burn all remaining balance:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount "$AFTER" --yes
   ```
   - **Expected:** Exit code `0`.

5. Verify zero balance:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder 2>&1 | grep -qiE "^0\.0+$|0\.000000"
   ```
   - **Expected:** Balance is exactly `0`.

**Cleanup:** None.

---

### M3-TOK-008: Web UI token toolkit — create + mint

**Preconditions:** Web UI accessible, LocalNet running.
**Platforms:** All

**Steps:**

1. Verify token toolkit section exists in Web UI:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(token|faucet|mint|create.*token)"
   ```
   - **Expected:** Token section found.

2. Verify token cards are rendered:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(token.*card|TestCoin|E2ECoin|TST|E2E)"
   ```
   - **Expected:** Previously created tokens appear as cards.

3. Verify mint action UI elements:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(mint|amount|supply)"
   ```
   - **Expected:** Mint controls present.

4. Verify create token form/wizard UI elements:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(create|wizard|name|symbol|decimals)"
   ```
   - **Expected:** Token creation form present.

**Note:** Full interactive token creation and minting via the Web UI requires browser automation. The above validates UI structure and that existing tokens are reflected.

**Cleanup:** None.

---

### M3-TOK-009: Web UI token transfer + activity feed

**Preconditions:** Web UI accessible, tokens with balance exist.
**Platforms:** All

**Steps:**

1. Verify transfer action UI elements:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(transfer|send|recipient|to.*wallet)"
   ```
   - **Expected:** Transfer controls present.

2. Verify recent token activity feed:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(activity|recent|history|transaction|event)"
   ```
   - **Expected:** Activity feed section present.

3. Verify token activity includes operations from CLI tests:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(mint|transfer|burn|create)" 
   ```
   - **Expected:** Token operations from earlier tests appear in the activity feed.

4. Verify burn action UI elements:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(burn|destroy)"
   ```
   - **Expected:** Burn controls present.

5. Verify balance display:
   ```bash
   curl -sf "$WEB_UI_URL" | grep -qiE "(balance|supply|[0-9]+)"
   ```
   - **Expected:** Token balances displayed.

**Note:** Full interactive transfer and activity feed verification requires browser automation.

**Cleanup:** None.

---

### M3-TOK-011: Cross-participant DAR vetting and mint (app-provider → app-user)

**Preconditions:** LocalNet `e2e-m3-test` running on Splice 0.6.11 or newer. No prior `XPAR` instrument registered.
**Platforms:** All

Verifies fix for [#318](https://github.com/bitdynamics-ab/canton-devkit/issues/318): `token create` must vet the bundled test-token DARs on every LocalNet participant so minting to a party hosted on a different participant succeeds.

**Steps:**

1. Allocate an issuer party on `app-provider` and a holder party on `app-user`:

   ```bash
   $CLI token party new xpar-issuer --instance e2e-m3-test --role app-provider
   $CLI token party new xpar-holder --instance e2e-m3-test --role app-user
   ```

   - **Expected:** Both commands exit `0` and print `Registered party`.

2. Create instrument `XPAR` with `xpar-issuer` as issuer:

   ```bash
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "Cross-Participant Token" --symbol XPAR --decimals 6 \
     --initial-supply 0 --issuer xpar-issuer
   ```

   - **Expected:** Exit code `0`.

   - **Assert all-participant vetting in output:**

     ```bash
     $CLI token create --instance e2e-m3-test --non-interactive \
       --name "Cross-Participant Token" --symbol XPAR --decimals 6 \
       --initial-supply 0 --issuer xpar-issuer 2>&1 \
       | grep -q "Vetted test-token DARs on sv, app-provider, app-user"
     ```

     (This will return `ErrSymbolInUse` on the second run; run the assertion against the output of step 2 directly, or check `dar list --vetting` in step 3.)

   - **Assert vetting via `dar list`:**

     ```bash
     $CLI dar list --instance e2e-m3-test --vetting 2>&1 \
       | grep "splice-test-token-v2" \
       | grep -q "U:✓ P:✓ S:✓"
     ```

     - **Expected:** `splice-test-token-v2` shows vetted on all three participants (`U` = app-user, `P` = app-provider, `S` = sv).

3. Mint to `xpar-holder` (the app-user party):

   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument XPAR --to xpar-holder --amount 1000
   ```

   - **Expected:** Exit code `0`, output includes `mint: accepted` (confirms the holding settled on-ledger, not just offered).

   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument XPAR --to xpar-holder --amount 1000 2>&1 \
     | grep -q "mint: accepted"
   ```

4. Verify `xpar-holder` balance on the app-user participant:

   ```bash
   $CLI token balance --instance e2e-m3-test \
     --party xpar-holder --instrument XPAR --role app-user \
     --format json
   ```

   - **Expected:** Exit code `0`, JSON output contains amount `1000`.

   ```bash
   $CLI token balance --instance e2e-m3-test \
     --party xpar-holder --instrument XPAR --role app-user \
     --format json 2>&1 | grep -q '"amount"'
   ```

5. Confirm `xpar-issuer` (app-provider) has zero balance (all supply minted to holder):

   ```bash
   $CLI token balance --instance e2e-m3-test \
     --party xpar-issuer --instrument XPAR 2>&1 \
     | grep -q "0\."
   ```

   - **Expected:** Balance is `0.000000` (no self-held supply).

**Cleanup:** None (XPAR instrument persists for reference).

---

### M3-TOK-010: Cross-platform regression (macOS/Linux/Windows)

**Preconditions:** This test is a meta-test — run the full M3-TOK-001 through M3-TOK-009 suite on each platform.
**Platforms:** All (run once per platform)

**Steps:**

1. **Per-platform execution:**
   ```bash
   echo "Running on platform: $(uname -s) $(uname -m)"
   ```

2. **Execute the full token test suite on the current platform:**
   ```bash
   PASS_COUNT=0
   FAIL_COUNT=0

   # prerequisite parties
   $CLI token party new plt-issuer --instance e2e-m3-test
   $CLI token party new plt-holder --instance e2e-m3-test

   # M3-TOK-001: Token create
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "PlatformCoin" --symbol PLT --decimals 6 \
     --initial-supply 0 --issuer plt-issuer \
     && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-002: Token mint
   $CLI token mint --instance e2e-m3-test \
     --instrument PLT --to plt-holder --amount 1000 \
     && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-003: Token transfer
   $CLI token transfer --instance e2e-m3-test \
     --instrument PLT --from plt-holder --to plt-issuer \
     --amount 200 --auto-accept \
     && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-004: Token burn
   $CLI token burn --instance e2e-m3-test \
     --instrument PLT --from plt-holder --amount 100 --yes \
     && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-005: Token balance
   $CLI token balance --instance e2e-m3-test --instrument PLT \
     && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   echo "Platform regression results: PASS=$PASS_COUNT FAIL=$FAIL_COUNT"
   [ "$FAIL_COUNT" -eq 0 ] && echo "PASS: all platform tests passed" || echo "FAIL: $FAIL_COUNT tests failed"
   ```

3. **Verify platform-specific binary integrity:**
   ```bash
   case "$(uname -s)" in
     Darwin) file $(which canton-devkit 2>/dev/null || echo ".") | grep -qiE "Mach-O" && echo "PASS: macOS binary" || echo "WARN" ;;
     Linux)  file $(which canton-devkit 2>/dev/null || echo ".") | grep -qiE "ELF" && echo "PASS: Linux binary" || echo "WARN" ;;
     *)      echo "Windows: verify .exe manually" ;;
   esac
   ```

4. **Record platform and Docker environment:**
   ```bash
   echo "=== Platform Info ==="
   uname -a
   docker version --format '{{.Server.Version}}'
   docker compose version
   $CLI --version
   echo "===================="
   ```

**Cleanup:**
```bash
$CLI down --name e2e-m3-test 2>/dev/null || true
$CLI clean --name e2e-m3-test --force 2>/dev/null || true
```

---

## Cross-Platform Notes

| Platform | Special Considerations |
|---|---|
| **macOS (Apple Silicon)** | Docker Desktop required. Token operations go through Ledger API on localhost. No known arm64-specific token issues expected. |
| **Linux (amd64)** | Native Docker. Token operations may be faster due to native container performance. Ensure user is in `docker` group. |
| **Windows (amd64)** | Docker Desktop with WSL 2. Token CLI commands work via PowerShell or WSL bash. `grep` and `cut` available in WSL; use PowerShell equivalents (`Select-String`, `ConvertFrom-Json`) for native Windows testing. |

---

## Test Execution Summary

| ID | Test Name | Category | Depends On |
|---|---|---|---|
| M3-TOK-001 | Token create wizard (non-interactive) | Token Create | M1 + M2 suites |
| M3-TOK-002 | Token mint | Token Ops | M3-TOK-001 |
| M3-TOK-003 | Token transfer | Token Ops | M3-TOK-002 |
| M3-TOK-004 | Token burn | Token Ops | M3-TOK-002 |
| M3-TOK-005 | Token balance query | Token Ops | M3-TOK-001 |
| M3-TOK-006 | Full flow: create, mint, transfer, burn, balance | Token E2E | M1 + M2 suites |
| M3-TOK-007 | Token balance after partial burn | Token Edge | M3-TOK-001 |
| M3-TOK-008 | Web UI token toolkit: create + mint | Token Web UI | M2-WEB-001 |
| M3-TOK-009 | Web UI token transfer + activity feed | Token Web UI | M2-WEB-001 |
| M3-TOK-010 | Cross-platform regression | Regression | All M3 tests |
| M3-TOK-011 | Cross-participant DAR vetting and mint | Token E2E | M1 + M2 suites |

---

## CIP-0112 Scope Note

All token tests in this plan target the **CIP-0112 (Token Standard V2)** path as the default, consistent with the proposal's committed scope. CIP-56 (V1) compatibility and V1-to-V2 migration helpers are explicitly out of scope for this test plan. If CIP-56 support is added later, a supplementary test plan should be created.
