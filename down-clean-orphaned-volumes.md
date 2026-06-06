# Issue: `down` → `clean` sequence orphans Docker volumes

**Severity:** Medium
**Found in:** E2E test M1-CLN-001 (2026-06-06, macOS arm64, dev build)
**Component:** `internal/cli/localnet/down.go`, `internal/cli/localnet/clean.go`

---

## Summary

`localnet down` (default, without `--keep-data`) deletes the registry entry for the instance but preserves Docker volumes. A subsequent `localnet clean --name <name>` cannot find the instance because the registry entry is gone, so it reports "Nothing to clean" while Docker volumes remain orphaned on disk.

The `down` command's own output tells the user to run `localnet clean` to remove volumes — but that won't work.

## Reproduction

```bash
canton-devkit localnet up --name foo
canton-devkit localnet down --name foo
# Output says: "Run localnet clean to remove volumes."

canton-devkit localnet clean --name foo --force
# Output: No instance named "foo" is registered. Nothing to clean.

docker volume ls --format '{{.Name}}' | grep foo
# canton-foo_postgres
# canton-foo_domain-upgrade-dump
# ← orphaned
```

## Root Cause

In `down.go:206-213`, the default path (without `--keep-data`) calls `registry.Delete(state.Name)` after `docker compose down` succeeds. This removes `~/.canton-devkit/localnet/<name>/` (state.json, overlay.env) and the index entry.

In `clean.go:182-199`, the clean command looks up the instance via the registry index. Without a registry entry, it cannot discover the compose project name needed to run `docker compose down --volumes`.

The `--keep-data` path in `down` does the right thing — it preserves the registry entry with `status=stopped`, which lets `clean` find and remove everything afterward.

### Relevant code

**`down.go:206-213`** — default path deletes registry:
```go
if !opts.KeepData {
    if delErr := registry.Delete(state.Name); delErr != nil { ... }
} else {
    state.Status = registry.StatusStopped
    if werr := registry.Write(state); werr != nil { ... }
}
```

**`down.go:219-224`** — output message (misleading after registry deletion):
```
Run localnet up --name foo to resume, or localnet clean to remove volumes.
```

**`clean.go:182+`** — requires registry entry to find compose project:
```go
// Looks up instance in registry index
// Reads state.json for compose project name, files, env
// Calls runner.Down(ctx) with --volumes
```

## Current workarounds

1. Use `clean --force` directly on a running instance (skips `down` entirely):
   ```bash
   canton-devkit localnet clean --name foo --force
   ```
2. Use `down --keep-data` to preserve the registry entry:
   ```bash
   canton-devkit localnet down --name foo --keep-data
   canton-devkit localnet clean --name foo --force
   ```
3. Manual Docker cleanup:
   ```bash
   docker volume rm canton-foo_postgres canton-foo_domain-upgrade-dump
   ```

## Proposed fix

**Option A (recommended): Make `down` preserve the registry entry by default.**

Change the default `down` behavior to match the current `--keep-data` path: set `status=stopped` and keep the registry entry. This is the smallest change and preserves `clean`'s ability to work after `down`. The registry entry is lightweight (a few KB of JSON) and `up` already handles re-registration.

Replace the current `--keep-data` flag with an inverse `--remove-data` flag (or just always keep the entry). The `down` output message "run `clean` to remove volumes" then becomes correct.

**Option B: Make `clean` discover orphan volumes by naming convention.**

Teach `clean` to find Docker volumes matching `canton-<name>_*` even when the instance is unregistered. More resilient to any state corruption, but requires more code and assumptions about the naming convention.

**Option C: Fix the output message.**

If the current behavior is intentional, update the `down` output to say "run `up --name foo` to resume" and remove the reference to `clean`. Add docs noting that `clean --force` must be used on a running instance, not after `down`.
