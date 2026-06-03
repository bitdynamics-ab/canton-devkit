---
name: canton-dar-upload
description: Upload and inspect Daml DAR packages on a Canton LocalNet. Use when the user wants to deploy a .dar to local participants or list/inspect deployed packages.
---

# DAR upload & inspection

Deploy and inspect Daml packages on a running LocalNet via
`dpm localnet dar` (or `canton-devkit localnet dar`).

## When to use
The user asks to "upload my DAR", "deploy the package to LocalNet",
"see what packages are installed", or "diff two package versions".

## Safe workflow

1. **Confirm the instance is up**:
   ```
   dpm localnet status --name dev
   ```

2. **Upload a DAR** (vets it so it's usable):
   ```
   dpm localnet dar upload ./dist/my-app.dar --instance dev
   ```
   Compilation is NOT this tool's job — build with `dpm build` /
   `daml build` first, then upload the resulting `.dar`.

3. **List deployed packages**:
   ```
   dpm localnet dar list --instance dev
   ```
   Shows package id, name, version, and vetting status.

4. **Inspect / compare**:
   ```
   dpm localnet dar info ./dist/my-app.dar       # modules, templates, deps
   dpm localnet dar diff ./v1.dar ./v2.dar       # SCU-aware structural diff
   dpm localnet dar download <package-id> --instance dev
   ```

## Guardrails
- Build artefacts with Daml tooling, never hand-edit a `.dar`.
- `dar diff` SCU signals are best-effort — authoritative upgrade
  validation is the Ledger API's job.
- `dar remove` only unvets/removes where the participant admin API
  supports it.
