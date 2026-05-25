---
name: canton-devkit-hot-deploy
description: Rebuild a Daml package and upload only what changed. Use
  during an edit-compile-deploy loop when the user is iterating on
  Daml code and doesn't want to wait for a full upload every time.
mirrors: docs/design/mockups/screens-dar.jsx
---

# Hot deploy — smart re-upload

## What this does

Compiles the user's Daml project to a fresh `.dar`, computes the
package hash, and uploads only if the hash isn't already vetted on the
participant. Avoids the 30–60s round-trip of a no-op re-upload during
fast iteration.

## Trigger

The user says one of: "redeploy", "push the latest changes", "rebuild
and upload", "hot reload my daml code".

## Flow

```sh
# 1. Build (or rebuild) the DAR.
cd <DAML_PROJECT_DIR>
daml build

# 2. Check what would change without uploading.
dpm localnet dar diff \
  --name <INSTANCE> \
  --file .daml/dist/*.dar

# 3. Upload only if the diff reported changes.
dpm localnet dar upload \
  --name <INSTANCE> \
  --file .daml/dist/*.dar
```

The agent should branch on the exit code of `dar diff`:

- Exit `0`: no changes — DAR already vetted, skip upload.
- Exit `1`: differences exist, run the upload.
- Exit `2`+: error — see `canton-devkit-dar-upload` failure section.

## Watch mode (optional)

If the user wants continuous redeploy on file save:

```sh
dpm localnet dar watch --name <INSTANCE> --project <DAML_PROJECT_DIR>
```

`watch` runs `daml build` on filesystem changes, then upload-if-diff
loop. Ctrl+C stops it. Watch mode prints a one-line "uploaded vX" or
"unchanged" per event so the user sees progress.

## What to NOT do

- **Don't `daml build && dpm dar upload` unconditionally.** A no-op
  upload still serialises through the participant's package vetting
  and takes ~30s. Use `dar diff` to skip.
- **Don't watch files yourself.** The `dar watch` command already
  handles debouncing and the `.dar` filename pattern; an agent re-
  rolling its own watcher will miss edge cases (atomic-replace,
  symlinks).
