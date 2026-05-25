<!-- SPDX-License-Identifier: Apache-2.0 -->
# DevKit agent skills

Markdown files that teach an AI coding agent (Claude Code, Codex, Cursor)
how to use `dpm localnet` *safely* — so the agent can spin up a ledger,
upload a DAR, inspect contracts, or run a token flow on your behalf without
being given raw shell access.

Each `.md` file in this directory is a self-contained skill. The 2026-05-25
Web UI Agent screen (BIT-141 follow-on) renders these files into the
"Install" panel; users click **Open in Claude** to copy the skill into
their `~/.claude/skills/` directory, or install all of them at once with:

```sh
dpm localnet skills install --target ~/.claude/skills/
```

## Design rules for skill authors

These rules exist because *every* skill ends up running with the user's
filesystem and network privileges. Treat each as load-bearing.

1. **No raw `docker` commands** in skill examples. Everything goes through
   `dpm localnet <verb>`. The CLI knows about port allocation, registry
   locks, JWT refresh — raw `docker` doesn't, and an agent that fell back
   to `docker compose down` would skip the registry write that keeps
   `dpm localnet status` honest.
2. **Pair every state-mutating example with a verification step.**
   `dpm localnet up` → `dpm localnet status --name X` → "look for Status:
   running". The verification step is what the agent uses to decide
   "did that work?" without inventing one.
3. **First failure → `dpm localnet doctor`.** When a command fails, the
   skill says "run `dpm localnet doctor` and read the output." The
   doctor's structured output is what the agent uses to diagnose — it
   should never try `dmesg` or `journalctl` directly.
4. **Name the exit-code class for each failure mode.** `2 =
   ExitPreflightFail`, `3 = ExitTimeout`, `4 = ExitRuntimeFailure`.
   Agents branch on numeric exit; the README friendly names exist for
   humans reading the same skill.
5. **Reference the canonical mockup screen.** Each skill's "What this
   does" paragraph cites the JSX screen it mirrors
   (`docs/design/mockups/screens-lifecycle.jsx`, etc.) so a maintainer
   updating the mockup knows which skill needs touching.

## What ships

| File | Workflow | Mirrors |
|---|---|---|
| `localnet-lifecycle.md` | up · status · down · clean | `screens-lifecycle.jsx` |
| `dar-upload.md` | dar upload + verify | `screens-dar.jsx` |
| `hot-deploy.md` | upload → vet → re-upload (smart diff) | `screens-dar.jsx` |
| `inspect-contracts.md` | acs / tx / by-template | `screens-contracts.jsx` |
| `token-flow.md` | create · mint · transfer · burn · balance | `screens-tokens-help.jsx` |
| `ci-localnet.md` | minimal ephemeral instance for a CI job | `screens-lifecycle.jsx` |

## Installing

```sh
# Default target = ~/.claude/skills/<repo-name>/
dpm localnet skills install

# Custom target (codex, cursor, etc.)
dpm localnet skills install --target ~/.codex/skills/canton-devkit/

# Dry run — just print what would be copied
dpm localnet skills install --dry-run
```

## Updating

Skill docs ship with the binary (`go:embed`-ed). `dpm localnet skills
install` always copies the version matching the installed `dpm` binary
— upgrading `dpm` does not touch already-installed skills. Re-run
`install` after `dpm upgrade` if you want the new versions.
