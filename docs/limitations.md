# Known limitations

Living list of things DevKit does not (yet) do well, with the rationale
and links to follow-up tickets where applicable. Updated as we ship.

## Concurrency / locking

- **Windows: registry locking is process-local only.**
  `internal/registry/lock_windows.go` returns a no-op `Lock()` and
  `withIndexLock` uses a `sync.Mutex` that protects only within the
  same canton-devkit process. Two concurrent `localnet up --name foo`
  invocations on Windows can race past the lock. We print a one-line
  stderr warning on every `Lock` call so users notice. A proper fix
  needs `CreateFileW` + `LockFileEx` via `golang.org/x/sys/windows`;
  out of scope until someone hits this in practice.
  *Linux/macOS use `syscall.Flock` and are unaffected.*

## Splice version pinning

- **Tarball SHA-256 may shift if GitHub regenerates the source archive.**
  `internal/splice/versions.go` pins each version by hashing the
  upstream GitHub source-tarball. GitHub regenerates these lazily
  and the gzip metadata can vary. If a regenerated archive yields a
  new digest, every pinned version fails until the catalogue is
  updated. Two future fixes: verify by extracting + hashing the file
  tree (not the gzip), or pin a git commit and `git archive` locally.

## Compose env reconstruction

- **`composeContext` rebuilds env from registry state.**
  `down` / `logs` / `creds` need the env that was passed to `up`. We
  reconstruct it from `state.json` so a fresh shell can still operate
  the instance. Any new env var Splice adds in a future release that
  we don't capture in state will silently break operations from a
  fresh shell. Mitigation: integration tests in CI (follow-up ticket).

## Integration testing

- **No CI integration test for `localnet up` against real Splice.**
  Unit tests cover parsers and orchestration well, but the actual
  bring-up flow is never exercised end-to-end in CI. The first
  upstream-contract drift will be found by a user, not by us. Filed
  separately as a follow-up.

## Platform parity

- **Homebrew formula targets macOS arm64 and Linux x86_64 only.**
  Matches the release matrix. macOS Intel, Linux ARM, and Windows are
  intentionally out of scope.
- **Windows users**: use the standalone zip from GitHub Releases
  rather than DPM until the Windows `.exe` path through DPM is
  verified.
