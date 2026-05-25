---
name: canton-devkit-dar-upload
description: Upload a compiled Daml package (a .dar file) to a running
  Canton LocalNet instance. Use when the user asks to "upload a DAR",
  "deploy my Daml code", or "vet a package."
mirrors: docs/design/mockups/screens-dar.jsx
---

# DAR upload

## What this does

Uploads a `.dar` (Daml Archive) file to the participant of a running
LocalNet instance, then verifies the package is visible to the ledger.
Equivalent to `daml ledger upload-dar` but goes through the DevKit
DAR backend which handles JWT auth and version-skew detection.

## Prerequisites

The instance must be `Status: running`. Bring it up with the
`canton-devkit-lifecycle` skill first if it isn't.

## Upload

```sh
dpm localnet dar upload --name <INSTANCE> --file path/to/your.dar
```

Multiple files in one call are allowed:

```sh
dpm localnet dar upload --name demo --file pkg-a.dar --file pkg-b.dar
```

**Verification (always run this next):**

```sh
dpm localnet dar list --name <INSTANCE> --format=json
```

Look for the package-id of the uploaded DAR in the output. Package IDs
are SHA-256 hashes; they appear as a 64-char hex string. The agent
can compute the expected ID with `daml damlc inspect path/to/your.dar
| head -1` if it needs to assert a specific package landed.

## Version skew

If the user's DAR depends on a package version the participant doesn't
have, upload will fail with `ExitRuntimeFailure (4)` and a message
naming the missing dependency. The fix is either:

- Upload the dependency DAR first, OR
- Recompile the user's package against the participant's package set
  (use `daml damlc build --dar-dep <id>` to pin).

`dpm localnet dar diff --name <INSTANCE> --file your.dar` shows what
would be uploaded vs what's already vetted, without performing the
upload. Useful when the agent isn't sure if the package is new.

## Hot deploy / re-upload

DARs are content-addressed by hash. Re-uploading a byte-identical DAR
is a no-op (server returns 200, hash already vetted). Changing one
line in the Daml source produces a new hash; re-upload installs the
new version alongside the old. See the `canton-devkit-hot-deploy`
skill for the smart-diff flow that avoids redundant work.

## Failure handling

- Exit `2` (`ExitPreflightFail`): the instance isn't running. Run
  `dpm localnet status --name <NAME>` then bring it up.
- Exit `1` (`ExitUserError`): malformed `--file` (not a valid DAR),
  or `--name` doesn't exist. The error line names which.
- Exit `4` (`ExitRuntimeFailure`): upload reached the participant but
  the participant rejected it. The error includes the participant's
  message verbatim — usually a missing dependency or a package-name
  conflict. Run `dpm localnet doctor --name <NAME>` to confirm the
  participant is healthy.

## What to NOT do

- **Don't curl the JSON API directly.** The DevKit CLI handles JWT
  signing for the dev shared secret; a hand-rolled `curl` would have
  to mint its own JWT and would expire silently.
- **Don't pre-strip the DAR.** Upload the file the Daml SDK produced;
  the backend does its own deduplication.
