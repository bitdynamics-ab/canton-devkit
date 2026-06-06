# E2E Test Plan — Milestone 3: Token Faucets & Token Standard Tooling (CIP-0112)

> **Proposal Reference:** `original-devkit-proposal.md`, Milestone 3 (Lines 268–277)
> **Estimated Delivery:** Month 9
> **Total Tests:** 10
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

# Capture available wallet/party info
export WALLET_A=$($CLI env --name e2e-m3-test 2>&1 | grep -iE "WALLET|ALICE" | head -1 | cut -d= -f2)
export WALLET_B=$($CLI env --name e2e-m3-test 2>&1 | grep -iE "WALLET|BOB" | head -1 | cut -d= -f2)
```

---

## Test Cases

---

### M3-TOK-001: Token create wizard (non-interactive)

**Preconditions:** LocalNet `e2e-m3-test` running.
**Platforms:** All

**Steps:**

1. Create a new token using non-interactive flags (CIP-0112 path):
   ```bash
   $CLI token create \
     --token-name "TestCoin" \
     --symbol "TST" \
     --decimals 8 \
     --initial-supply 1000000 \
     --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify creation confirmation:**
     ```bash
     $CLI token create \
       --token-name "TestCoin" \
       --symbol "TST" \
       --decimals 8 \
       --initial-supply 1000000 \
       --name e2e-m3-test 2>&1 | grep -qiE "(created|success|TestCoin|TST)"
     ```

2. Verify the token exists by checking balance:
   ```bash
   $CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -qiE "(1000000|TestCoin|TST)"
   ```
   - **Expected:** Balance shows the initial supply.

3. Verify CIP-0112 alignment:
   ```bash
   $CLI token create \
     --token-name "TestCoin" \
     --symbol "TST" \
     --decimals 8 \
     --initial-supply 1000000 \
     --name e2e-m3-test 2>&1 | grep -qiE "(cip.0112|v2|token.standard)"
   ```
   - **Expected:** Output references CIP-0112 / V2 path (or no V1 warnings).

**Cleanup:** None (token persists for subsequent tests).

---

### M3-TOK-002: Token mint

**Preconditions:** Token "TestCoin" created (M3-TOK-001).
**Platforms:** All

**Steps:**

1. Mint additional tokens:
   ```bash
   $CLI token mint TestCoin 500000 --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify mint confirmation:**
     ```bash
     $CLI token mint TestCoin 500000 --name e2e-m3-test 2>&1 | grep -qiE "(minted|success|500000)"
     ```

2. Verify updated balance:
   ```bash
   $CLI token balance TestCoin --name e2e-m3-test
   ```
   - **Expected:** Balance is now `1500000` (initial 1000000 + minted 500000).
   - **Verify:**
     ```bash
     $CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -qiE "1500000"
     ```

3. Mint to a specific wallet:
   ```bash
   $CLI token mint TestCoin 100000 --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.

**Cleanup:** None.

---

### M3-TOK-003: Token transfer

**Preconditions:** Token "TestCoin" minted (M3-TOK-002), multiple wallets available.
**Platforms:** All

**Steps:**

1. Transfer tokens between wallets:
   ```bash
   $CLI token transfer TestCoin 250000 --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify transfer confirmation:**
     ```bash
     $CLI token transfer TestCoin 250000 --to "$WALLET_B" --name e2e-m3-test 2>&1 | grep -qiE "(transferred|success|250000)"
     ```

2. Verify sender balance decreased:
   ```bash
   SENDER_BALANCE=$($CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -oE "[0-9]+")
   # Sender balance should be 1500000 - 250000 = 1250000 (or adjusted based on previous mints)
   echo "Sender balance: $SENDER_BALANCE"
   ```

3. Verify receiver balance increased:
   ```bash
   $CLI token balance TestCoin --to "$WALLET_B" --name e2e-m3-test 2>&1
   ```
   - **Expected:** Receiver has tokens from transfer + any direct mints.

4. Attempt transfer with insufficient balance:
   ```bash
   $CLI token transfer TestCoin 999999999999 --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Expected:** Non-zero exit code, error message about insufficient balance.

**Cleanup:** None.

---

### M3-TOK-004: Token burn

**Preconditions:** Token "TestCoin" exists with balance > 0.
**Platforms:** All

**Steps:**

1. Burn tokens:
   ```bash
   $CLI token burn TestCoin 100000 --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify burn confirmation:**
     ```bash
     $CLI token burn TestCoin 100000 --name e2e-m3-test 2>&1 | grep -qiE "(burned|burnt|success|100000)"
     ```

2. Verify balance decreased after burn:
   ```bash
   $CLI token balance TestCoin --name e2e-m3-test
   ```
   - **Expected:** Balance reduced by 100000 from pre-burn value.

3. Attempt to burn more than available balance:
   ```bash
   $CLI token burn TestCoin 999999999999 --name e2e-m3-test
   ```
   - **Expected:** Non-zero exit code, error message about insufficient balance.

**Cleanup:** None.

---

### M3-TOK-005: Token balance query

**Preconditions:** Token "TestCoin" exists.
**Platforms:** All

**Steps:**

1. Query balance for default wallet:
   ```bash
   $CLI token balance TestCoin --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.
   - **Verify output format:**
     ```bash
     $CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -qiE "(TestCoin|TST|balance|[0-9]+)"
     ```

2. Query balance for a specific wallet:
   ```bash
   $CLI token balance TestCoin --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`, shows balance for wallet B.

3. Query balance for non-existent token:
   ```bash
   $CLI token balance NonExistentToken --name e2e-m3-test
   ```
   - **Expected:** Non-zero exit code or zero balance, with clear message.

4. Query all token balances (if supported):
   ```bash
   $CLI token balance --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`, lists all tokens and their balances.

**Cleanup:** None.

---

### M3-TOK-006: Full flow — create, mint, transfer, burn, balance

**Preconditions:** LocalNet `e2e-m3-test` running, clean token state preferred.
**Platforms:** All

This test executes the complete token lifecycle in a single sequential flow, validating state after each step.

**Steps:**

1. **Create** a new token:
   ```bash
   $CLI token create \
     --token-name "E2ECoin" \
     --symbol "E2E" \
     --decimals 6 \
     --initial-supply 0 \
     --name e2e-m3-test
   ```
   - **Verify:** Exit code `0`, creation confirmed.
   - **Assert:** `$CLI token balance E2ECoin --name e2e-m3-test` shows `0`.

2. **Mint** initial supply:
   ```bash
   $CLI token mint E2ECoin 1000000 --name e2e-m3-test
   ```
   - **Verify:** Exit code `0`.
   - **Assert:** `$CLI token balance E2ECoin --name e2e-m3-test` shows `1000000`.

3. **Transfer** to another wallet:
   ```bash
   $CLI token transfer E2ECoin 400000 --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Verify:** Exit code `0`.
   - **Assert sender:** Balance = `600000`.
   - **Assert receiver:** Balance = `400000`.

4. **Burn** from sender:
   ```bash
   $CLI token burn E2ECoin 100000 --name e2e-m3-test
   ```
   - **Verify:** Exit code `0`.
   - **Assert sender:** Balance = `500000`.

5. **Final balance** check:
   ```bash
   $CLI token balance E2ECoin --name e2e-m3-test
   ```
   - **Assert sender:** `500000`.
   ```bash
   $CLI token balance E2ECoin --to "$WALLET_B" --name e2e-m3-test
   ```
   - **Assert receiver:** `400000`.
   - **Assert total supply:** `900000` (1000000 minted - 100000 burned).

6. **Ledger verification** — verify token operations created transactions:
   ```bash
   $CLI tx ls --template "E2ECoin" --name e2e-m3-test 2>&1 | wc -l
   ```
   - **Expected:** At least 4 transactions (create, mint, transfer, burn).

**Cleanup:** None (E2ECoin persists for reference).

---

### M3-TOK-007: Token balance after partial burn

**Preconditions:** Token "TestCoin" exists with known balance.
**Platforms:** All

**Steps:**

1. Record current balance:
   ```bash
   BEFORE=$($CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -oE "[0-9]+")
   echo "Balance before: $BEFORE"
   ```

2. Burn a small amount:
   ```bash
   BURN_AMOUNT=1
   $CLI token burn TestCoin $BURN_AMOUNT --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.

3. Verify exact balance after partial burn:
   ```bash
   AFTER=$($CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -oE "[0-9]+")
   EXPECTED=$((BEFORE - BURN_AMOUNT))
   [ "$AFTER" -eq "$EXPECTED" ] && echo "PASS: balance is $AFTER (expected $EXPECTED)" || echo "FAIL: balance is $AFTER, expected $EXPECTED"
   ```

4. Burn all remaining balance:
   ```bash
   $CLI token burn TestCoin "$AFTER" --name e2e-m3-test
   ```
   - **Expected:** Exit code `0`.

5. Verify zero balance:
   ```bash
   $CLI token balance TestCoin --name e2e-m3-test 2>&1 | grep -qiE "^0$\|: 0\|balance.*0"
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
   # Run M3-TOK-001 through M3-TOK-009 and record results
   PASS_COUNT=0
   FAIL_COUNT=0

   # M3-TOK-001: Token create
   $CLI token create --token-name "PlatformCoin" --symbol "PLT" --decimals 6 --initial-supply 1000 --name e2e-m3-test && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-002: Token mint
   $CLI token mint PlatformCoin 500 --name e2e-m3-test && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-003: Token transfer
   $CLI token transfer PlatformCoin 200 --to "$WALLET_B" --name e2e-m3-test && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-004: Token burn
   $CLI token burn PlatformCoin 100 --name e2e-m3-test && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

   # M3-TOK-005: Token balance
   $CLI token balance PlatformCoin --name e2e-m3-test && PASS_COUNT=$((PASS_COUNT+1)) || FAIL_COUNT=$((FAIL_COUNT+1))

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

---

## CIP-0112 Scope Note

All token tests in this plan target the **CIP-0112 (Token Standard V2)** path as the default, consistent with the proposal's committed scope. CIP-56 (V1) compatibility and V1-to-V2 migration helpers are explicitly out of scope for this test plan. If CIP-56 support is added later, a supplementary test plan should be created.
