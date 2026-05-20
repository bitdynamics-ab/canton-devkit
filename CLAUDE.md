# canton-devkit — agent notes

Short, actionable guidance accumulated from review feedback. Read before
implementing; consult before claiming done.

## Pre-implementation discipline

- **Grep the artifact, don't trust memory.** Anything that parses an
  external format (env files, `docker compose ps` output, tar headers,
  proto wire bytes) starts with grepping the real corpus for syntactic
  variants before you design the parser. Example miss: regex-based env
  expander shipped without supporting bare `$VAR` because I never
  grepped Splice's `env/*.env` files for `\$[A-Za-z_]` first.

- **Cross-reference upstream for adapter constants.** When asserting
  endpoint maps / port numbers / service names against an external
  project (Splice compose.yaml, canton package_service.proto, …), put
  a comment pointing at the exact upstream file *and* write a lock-in
  test that fails if upstream drifts.

- **Failure-mode enumeration before implementation.** For state
  classifiers, write the decision table in a comment first, implement
  to it, then test each row. Don't ship `if line != "healthy"` when the
  real input has five states × five health values.

## Testing discipline (the part I was bad at)

- **Test every public method at least once.** If a method isn't called
  yet (forward-declared for a future PR), either delete it or add a
  test that exercises its wiring directly. Don't ship untested seams.
  Example miss: `Endpoints` shipped with no `cmd.Dir`/`cmd.Env` because
  it had no caller in PR-A and I skipped it under "obviously trivial."

- **Fresh-shell smoke test for every CLI verb.** Run `up`, then close
  the shell, open a new one, run `down` / `status` / `logs`. Inherited
  process env will mask broken `cmd.Env` plumbing if you test in the
  same shell as `up`.

- **Construct adversarial inputs, not just happy-path fixtures.** Tested
  Fetch against the real Splice tarball → never saw the empty-subtree
  case. For every "verify X" function, ship a "rejects bad X" test in
  the same commit.

- **One test per failure mode in the decision table.** Table-driven
  tests force you to enumerate the cases up front; they also document
  the contract for the next reader.

## Cross-cutting wiring

- **Centralise the seam that everyone forgets.** `exec.Cmd`
  construction is now funnelled through `ComposeRunner.command()` so
  no method can drop `Dir`/`Env`. If a wiring concern repeats across
  three call sites and one of them gets it wrong, extract the seam.

- **`~/.canton` is reserved by upstream Canton tooling.** Use
  `~/.canton-devkit/` for all DevKit state. Tests override via
  `CANTON_DEVKIT_REGISTRY`.

- **`ExitCodeError` must travel through `errors.As`.** Cobra's default
  collapses every error to exit 1 and prints its `Error()` string. The
  CLI dispatcher (`internal/cli/app.go`) extracts the wrapped code via
  `errors.As(err, &ece)` before the generic stderr-print path.
  Tests: assert specific exit codes, not just "non-zero".

## PR mechanics

- **Split PRs early.** If a PR crosses 1000 LOC or covers 3+ tickets,
  split it before requesting review. The cascade rebase pattern
  (`git rebase --onto NEW_BASE OLD_BASE_TIP`) handles dependent PRs;
  use the explicit `<upstream>` form because `git rebase TARGET` alone
  will try to replay already-applied commits and conflict.

- **Reply on every review comment with a commit hash + lock-in test
  name.** "Fixed in 6ffc8e5; locked by TestX" beats "fixed."

- **Post a PR-level summary comment alongside inline replies.** Inline
  threads are easy to miss in a scrollback; a top-level summary with a
  table mapping ask → resolution → test is what reviewers actually
  read.

## Tooling

- **Always run `go test ./... && golangci-lint run ./...` after every
  amend.** Cascade rebases re-run them on every dependent branch
  before force-pushing.

- **`gofmt -w` before committing.** golangci-lint will catch it but
  the round-trip is wasteful.

- **Don't accidentally commit `.claude/scheduled_tasks.lock` or other
  runtime state.** `.gitignore` should cover `.claude/*.lock`.
