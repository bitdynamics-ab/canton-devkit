# Agent skills

Canton DevKit ships six skills that teach coding agents to use `dpm localnet` or `canton-devkit localnet`. They cover network lifecycle, DAR deployment, hot redeployment, ledger inspection, token testing, and CI. Skills provide workflow instructions; install [DevKit](getting-started.md), Docker, and any required Daml tooling separately.

## Install with npx

Use the [Skills CLI](https://github.com/vercel-labs/skills#readme), which requires Node.js and npm. The command is `npx skills` (plural).

List the available skills without installing:

```sh
npx skills add bitdynamics-ab/canton-devkit --list
```

Install all six into your current project for Codex and Claude Code:

```sh
npx skills add bitdynamics-ab/canton-devkit --skill '*' --agent codex claude-code
```

Install a selected workflow:

```sh
npx skills add bitdynamics-ab/canton-devkit --skill canton-dar-upload --agent codex
```

Add `--global` to install across projects for your user, or omit `--agent` to choose agents interactively. From a DevKit source checkout, use `./skills` instead of `bitdynamics-ab/canton-devkit`.

## Install the bundled catalogue

The CLI and Web UI install the catalogue included in your installed DevKit version. This needs no Node.js and may differ from the latest repository catalogue.

```sh
canton-devkit localnet skills list
canton-devkit localnet skills install --target codex
canton-devkit localnet skills install --target claude
```

The CLI defaults to Claude Code. Use `--dir <path>` for a custom destination. Existing files with different content are preserved unless you supply `--force`.

In the Web UI, open **Agent Skills** to preview each document. Select **~/.codex/skills** or **~/.claude/skills** to install the full bundled catalogue on the machine running DevKit. The page reports preserved local edits; **Overwrite** replaces them when explicitly selected. Restart your agent session if it does not discover newly installed skills.

Choose one installation method per agent to avoid duplicates. The Skills CLI manages its own agent directories; DevKit's installer uses `~/.codex/skills` and `~/.claude/skills`.

## Choose a workflow

| Skill name (`--skill`) | Use it for |
| --- | --- |
| `canton-localnet-lifecycle` | Host checks, startup, health, pause, stop, teardown |
| `canton-dar-upload` | Uploading, listing, inspecting, downloading, comparing DARs |
| `canton-hot-deploy` | Build-upload and continuous redeployment |
| `canton-inspect-contracts` | Contracts, transactions, and party-specific visibility |
| `canton-token-flow` | Local token creation, minting, transfer, burning, balance |
| `canton-ci-localnet` | Disposable CI networks, version pinning, guaranteed cleanup |

The skills stay separate because these tasks have distinct triggers and constraints. Install all six for the full development loop; agents can select the relevant workflow without loading unrelated guidance.

The repository exports live under `skills/canton-devkit-<workflow>/SKILL.md`. The folder prefix does not change the skill names above. See the [repository packaging guide](../skills/README.md) for source layout and `make skills` regeneration.
