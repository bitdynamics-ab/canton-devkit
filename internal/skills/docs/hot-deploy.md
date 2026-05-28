---
name: canton-hot-deploy
description: Rebuild-and-reupload a DAR on source change for a fast local iteration loop. Use when the user wants hot-reload / watch-mode deployment to LocalNet.
---

# Hot-deploy (watch mode)

Tighten the edit→build→deploy loop against a running LocalNet using
`dpm localnet dar watch` and `dar build-upload`.

## When to use
The user asks for "hot reload", "auto-redeploy on change", or "rebuild
and upload in one step" while iterating on Daml code.

## Safe workflow

1. **One-shot build + upload** (delegates compilation to dpm/daml):
   ```
   dpm localnet dar build-upload --project . --name dev
   ```
   Skipped with a clear message if `dpm`/`daml` isn't available.

2. **Continuous watch** — rebuild + re-upload on every source change:
   ```
   dpm localnet dar watch --project . --name dev
   ```
   Leave it running in a terminal; Ctrl-C to stop. Each change triggers
   a `dpm build` and re-upload to the selected participant(s).

3. **Verify the new package landed**:
   ```
   dpm localnet dar list --name dev
   ```

## Guardrails
- Watch mode shells out to `dpm build` — keep the project compiling, or
  each cycle just reports the build error and skips the upload.
- For Smart Contract Upgrade (SCU) compatibility, bump the package
  version in `daml.yaml`; `dar diff` shows whether the change is
  upgrade-compatible.
