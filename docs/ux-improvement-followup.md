# UX Improvement Followup: Positional Instance Name + Aliases

This tracks the remaining docs and code surfaces that still use the
`--name <instance>` flag form after the CLI was updated to accept the
instance name as a positional argument (e.g., `localnet up dev`
instead of `localnet up --name dev`). The `--name` flag still works
(backward compatible); these updates are cosmetic — switching
examples and suggestions to the shorter positional form.

Also tracks surfaces that should mention the `start`/`stop` aliases
for `up`/`down`.

## Context

- **PR**: initial implementation of positional name + aliases
- **What changed**: 11 lifecycle commands (`up`, `down`, `restart`,
  `pause`, `resume`, `env`, `logs`, `creds`, `snapshot`, `restore`,
  `status`) accept the instance name as an optional positional arg
- **Aliases**: `start` → `up`, `stop` → `down`

## Remaining work

### 1. UI handler error strings

User-facing error messages in the Web UI handlers still suggest
`--name` form. Update to positional form.

- [ ] `internal/ui/handlers/instances.go` — `dpm localnet down --name …`
      and `dpm localnet up --name …` suggestion strings
- [ ] `internal/ui/handlers/dar.go` — restart suggestion:
      `dpm localnet down --name … followed by dpm localnet up --name …`
- [ ] `internal/ui/handlers/dar_inspect.go` — same pattern as dar.go
- [ ] `internal/ui/handlers/contracts.go` — same pattern as dar.go
- [ ] `internal/ui/handlers/metrics.go` —
      `dpm localnet up --profile observability --name …`

### 2. Internal doc-comment examples

- [ ] `internal/ui/term/box.go` — doc-comment example:
      `dpm localnet env --name hubble`
- [ ] `internal/ui/term/step.go` — doc-comment example:
      `dpm localnet up --name hubble`

### 3. User-facing docs guides

Update lifecycle command examples from `--name` to positional form:

- [ ] `docs/getting-started.md` — walkthrough commands (~15 instances)
- [ ] `docs/troubleshooting.md` — suggested commands (~10 instances)
- [ ] `docs/observability.md` — example commands (~4 instances)
- [ ] `docs/dashboard-customization.md` — example commands (~5 instances)
- [ ] `docs/tokens.md` — lifecycle examples (~3 instances; skip
      `token create --name` which is a token name, not instance name)
- [ ] `docs/explorer.md` — lifecycle examples (~6 instances)
- [ ] `docs/limitations.md` — CLI usage notes (~2 instances)
- [ ] `docs/validation-checklist.md` — validation commands (~3 instances)
- [ ] `docs/faq.md` — prose reference to `--name` (~1 instance)

### 4. E2E test transcript docs

These are verbose test-case transcripts. The `--name` form still works,
so these are low priority but should eventually reflect the preferred
form.

- [ ] `docs/tests/e2e-test-milestone-1.md` — ~100+ `--name` instances
      across lifecycle commands; also update the conventions section
      (lines ~779-782) to document positional form and aliases
- [ ] `docs/tests/e2e-test-milestone-2.md` — ~50+ `--name` instances
- [ ] `docs/tests/e2e-test-milestone-3.md` — ~20+ `--name` instances

## Explicitly out of scope

- `docs/original-devkit-proposal.md` — historical proposal, left as-is
- `docs/proposals/*` — design proposals, left as-is
- `AGENTS.md` — contributor conventions, not user-facing examples
