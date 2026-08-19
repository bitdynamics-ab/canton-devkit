# E2E Test Plan — Milestone 3: Token Faucets & Token Standard Tooling (CIP-0112)

**Total Tests:** 11
**Platforms:** macOS (Apple Silicon), Linux (amd64), Windows (amd64)
**Prerequisites:** All Milestone 1 and Milestone 2 tests passing.

---

## Overview

This test plan validates the CIP-0112 token standard tooling delivered in Milestone 3: the token creation wizard, minting, transfer, burn, and balance commands, the full token lifecycle E2E flow, edge cases, Web UI token toolkit, and cross-platform regression.

### Conventions

- `$CLI` = `dpm localnet` or `canton-devkit localnet` (run full suite twice — once per mode).
- Token commands target the CIP-0112 (V2) path as the default.
- Non-interactive flags are used for wizard-style commands to enable unattended execution.
- `$WEB_UI_URL` = URL of the Web UI. Obtain it from `$CLI status --name e2e-m3-test` — the URL is printed in the output.
- Default step timeout: 30 seconds unless noted.

### Environment Setup

1. Set the CLI mode (`dpm localnet` or `canton-devkit localnet`).
2. Clean any prior state and start the LocalNet:
   ```bash
   $CLI clean --name e2e-m3-test --force 2>/dev/null || true
   $CLI up --name e2e-m3-test
   ```
3. Note the Web UI URL from the `$CLI status --name e2e-m3-test` output and set it as `$WEB_UI_URL` for the steps below.

### Teardown

```bash
$CLI down --name e2e-m3-test 2>/dev/null || true
$CLI clean --name e2e-m3-test --force 2>/dev/null || true
```

Teardown is not a test case and is not owned by one. An automated run must place these commands in a step that runs whether the suite passed or failed — `if: always()` in GitHub Actions, or a shell `trap ... EXIT` — otherwise a failing test leaks the instance, its containers, and its volumes onto the runner. A manual run ends with M3-TOK-999, whose cleanup step points back here.

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
   - **Expected:** Exit code `0`. Output contains `created`, `TST`, or `tst-issuer`.

2. Verify the token exists by checking balance:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument TST
   ```
   - **Expected:** Output contains `1000000` and `TST`.

3. Verify CIP-0112 alignment — output from the create command should reference `cip-0112`, `v2`, or `token-standard`, and should not contain V1 warnings.

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
   - **Expected:** Exit code `0`. Output contains `mint: accepted`.

2. Verify balance on the holder:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Output contains `500000` (or the cumulative sum if multiple mints have run).

3. Mint a second batch to confirm idempotent re-mint:
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument TST --to tst-holder --amount 100000
   ```
   - **Expected:** Exit code `0`. Output contains `mint: accepted`.

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
   - **Expected:** Exit code `0`. Output contains `transfer`, `accepted`, or `250000`.

2. Verify sender balance decreased — `$CLI token balance --instance e2e-m3-test --instrument TST --party tst-issuer` should show a value reduced by 250000 from before the transfer.

3. Verify receiver balance increased:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Output contains at least `250000`.

4. Attempt transfer with insufficient balance:
   ```bash
   $CLI token transfer --instance e2e-m3-test \
     --instrument TST --from tst-issuer --to tst-holder \
     --amount 999999999999
   ```
   - **Expected:** Non-zero exit code. Error message mentions insufficient balance.

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
   - **Expected:** Exit code `0`. Output contains `burned`, `burnt`, or `100000`.

2. Verify balance decreased after burn — `$CLI token balance --instance e2e-m3-test --instrument TST --party tst-holder` should show a value reduced by 100000 from before the burn.

3. Attempt to burn more than available balance:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount 999999999999 --yes
   ```
   - **Expected:** Non-zero exit code. Error message mentions insufficient balance.

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
   - **Expected:** Exit code `0`. Output contains `TST` and a numeric amount.

2. Query balance for a second party:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument TST --party tst-holder
   ```
   - **Expected:** Exit code `0`. Output shows the balance for `tst-holder`.

3. Query all balances for the instance (no instrument filter):
   ```bash
   $CLI token balance --instance e2e-m3-test
   ```
   - **Expected:** Exit code `0`. Output lists all instruments and their balances.

4. Query balance in JSON format:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument TST --format json
   ```
   - **Expected:** Exit code `0`. JSON output contains an `"amount"` field.

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
   - **Verify:** Exit code `0`, creation confirmed. Balance for `E2E` should be `0` or empty.

2. **Mint** initial supply to sender:
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument E2E --to e2e-sender --amount 1000000
   ```
   - **Verify:** Exit code `0`, output includes `mint: accepted`. Balance for `e2e-sender` is `1000000`.

3. **Transfer** to receiver:
   ```bash
   $CLI token transfer --instance e2e-m3-test \
     --instrument E2E --from e2e-sender --to e2e-receiver \
     --amount 400000 --auto-accept
   ```
   - **Verify:** Exit code `0`. Sender balance is `600000`. Receiver balance is `400000`.

4. **Burn** from sender:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument E2E --from e2e-sender --amount 100000 --yes
   ```
   - **Verify:** Exit code `0`. Sender balance is `500000`.

5. **Final balance** check:
   ```bash
   $CLI token balance --instance e2e-m3-test --instrument E2E
   ```
   - **Assert:** Sender shows `500000`. Receiver shows `400000`. Total supply is `900000` (1000000 minted - 100000 burned).

6. **Ledger verification** — verify token operations created transactions:
   ```bash
   $CLI tx ls --template "E2E" --instance e2e-m3-test
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
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   Note the numeric amount shown (call it `BEFORE`).

2. Burn a small amount:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount 1 --yes
   ```
   - **Expected:** Exit code `0`.

3. Check balance after partial burn:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Amount equals `BEFORE - 1`.

4. Burn all remaining balance — use the current balance from step 3 as the amount:
   ```bash
   $CLI token burn --instance e2e-m3-test \
     --instrument TST --from tst-holder --amount "<current-balance>" --yes
   ```
   - **Expected:** Exit code `0`.

5. Verify zero balance:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --instrument TST --party tst-holder
   ```
   - **Expected:** Balance is exactly `0` or `0.000000`.

**Cleanup:** None.

---

### M3-TOK-008: Web UI token toolkit — create + mint

**Preconditions:** Web UI accessible, LocalNet running.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - A token section: `token`, `faucet`, `mint`, or `create token`.
   - Token cards for previously created tokens: `TestCoin`, `E2ECoin`, `TST`, or `E2E`.
   - Mint action controls: `mint`, `amount`, or `supply`.
   - Token creation form elements: `create`, `wizard`, `name`, `symbol`, or `decimals`.

**Note:** Full interactive token creation and minting via the Web UI requires browser automation. The above validates UI structure and that existing tokens are reflected.

**Cleanup:** None.

---

### M3-TOK-009: Web UI token transfer + activity feed

**Preconditions:** Web UI accessible, tokens with balance exist.
**Platforms:** All

**Steps:**

1. Fetch the Web UI and confirm it contains:
   - Transfer controls: `transfer`, `send`, `recipient`, or `to wallet`.
   - A recent activity feed: `activity`, `recent`, `history`, `transaction`, or `event`.
   - Token operations from earlier CLI tests: `mint`, `transfer`, `burn`, or `create`.
   - Burn controls: `burn` or `destroy`.
   - Token balance display: `balance`, `supply`, or a numeric amount.

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
   - **Expected:** Both commands exit `0` and output contains `Registered party`.

2. Create instrument `XPAR` with `xpar-issuer` as issuer:
   ```bash
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "Cross-Participant Token" --symbol XPAR --decimals 6 \
     --initial-supply 0 --issuer xpar-issuer
   ```
   - **Expected:** Exit code `0`. Output contains `Vetted test-token DARs on sv, app-provider, app-user`.

   (On a second run this command returns `ErrSymbolInUse`. To check vetting independently, use the next assertion.)

   - **Assert vetting via `dar list`:**
     ```bash
     $CLI dar list --instance e2e-m3-test --vetting
     ```
     - **Expected:** The `splice-test-token-v2` row shows vetted on all three participants — `U:✓ P:✓ S:✓` (U = app-user, P = app-provider, S = sv).

3. Mint to `xpar-holder` (the app-user party):
   ```bash
   $CLI token mint --instance e2e-m3-test \
     --instrument XPAR --to xpar-holder --amount 1000
   ```
   - **Expected:** Exit code `0`. Output contains `mint: accepted` (confirms the holding settled on-ledger, not just offered).

4. Verify `xpar-holder` balance on the app-user participant:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --party xpar-holder --instrument XPAR --role app-user \
     --format json
   ```
   - **Expected:** Exit code `0`. JSON output contains `"amount"` with value `1000`.

5. Confirm `xpar-issuer` (app-provider) has zero balance — all supply was minted to holder:
   ```bash
   $CLI token balance --instance e2e-m3-test \
     --party xpar-issuer --instrument XPAR
   ```
   - **Expected:** Balance is `0.000000`.

**Cleanup:** None (XPAR instrument persists for reference).

---

### M3-TOK-999: Cross-platform regression (macOS/Linux/Windows)

**Preconditions:** This test is a meta-test — run M3-TOK-001 through M3-TOK-009 and M3-TOK-011 on each platform first.
**Platforms:** All (run once per platform)

Numbered `999` so it always sorts last: its cleanup destroys the `e2e-m3-test` instance, so any case that runs after it has no LocalNet left to talk to.

**Steps:**

1. Print the current platform:
   ```bash
   echo "Running on platform: $(uname -s) $(uname -m)"
   ```

2. Execute the abbreviated token suite on the current platform using a separate instrument symbol (`PLT`) to avoid conflicts with earlier tests:

   ```bash
   # Register parties
   $CLI token party new plt-issuer --instance e2e-m3-test
   $CLI token party new plt-holder --instance e2e-m3-test

   # Create token
   $CLI token create --instance e2e-m3-test --non-interactive \
     --name "PlatformCoin" --symbol PLT --decimals 6 \
     --initial-supply 0 --issuer plt-issuer

   # Mint
   $CLI token mint --instance e2e-m3-test \
     --instrument PLT --to plt-holder --amount 1000

   # Transfer
   $CLI token transfer --instance e2e-m3-test \
     --instrument PLT --from plt-holder --to plt-issuer \
     --amount 200 --auto-accept

   # Burn
   $CLI token burn --instance e2e-m3-test \
     --instrument PLT --from plt-holder --amount 100 --yes

   # Balance
   $CLI token balance --instance e2e-m3-test --instrument PLT
   ```

   - **Expected:** All commands exit `0`. The final balance output shows `plt-holder` with `700` and `plt-issuer` with `200`.

3. Verify platform-specific binary integrity:
   - macOS: `file $(which canton-devkit)` should contain `Mach-O`.
   - Linux: `file $(which canton-devkit)` should contain `ELF`.
   - Windows: verify the `.exe` file exists and runs.

4. Record platform and Docker environment for the test report:
   ```bash
   uname -a
   docker version --format '{{.Server.Version}}'
   docker compose version
   $CLI --version
   ```

**Cleanup:** Run the [Teardown](#teardown) commands. In an automated run they belong in an always-run step rather than in this test case.

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
| M3-TOK-011 | Cross-participant DAR vetting and mint | Token E2E | M1 + M2 suites |
| M3-TOK-999 | Cross-platform regression (runs last) | Regression | All M3 tests |

---

## CIP-0112 Scope Note

All token tests in this plan target the **CIP-0112 (Token Standard V2)** path as the default, consistent with the proposal's committed scope. CIP-56 (V1) compatibility and V1-to-V2 migration helpers are explicitly out of scope for this test plan. If CIP-56 support is added later, a supplementary test plan should be created.
