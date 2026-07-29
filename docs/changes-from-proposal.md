# Changes from Original Proposal

This document records every deliberate deviation — command syntax, flag names, behaviour, or scope — between the [original DevKit Development Fund proposal](./original-devkit-proposal.md) and the shipped implementation.

Every deviation listed here is **intentional**, not an oversight or implementation mistake. Each one was made for a concrete reason: improving developer or user experience, system performance or resource efficiency, security, correctness, or CLI ↔ Web UI parity. The per-entry **"Why"** notes record that rationale. Where the proposal's wording was a high-level intent rather than a precise spec, the shipped form is the deliberate concretization of that intent.

**Maintenance rule:** any PR that introduces or changes a command name, flag name, alias, default, or user-facing behaviour relative to the proposal **must** add or update an entry here in the same PR. See the "Proposal deviation tracking" rule in [AGENTS.md](../AGENTS.md).

---

## Table of contents

- [Cross-cutting conventions](#cross-cutting-conventions)
  - [Instance name addressing](#instance-name-addressing)
  - [Machine-readable output flag](#machine-readable-output-flag)
  - [Command aliases](#command-aliases)
- [`localnet remove` (renamed from `clean`)](#localnet-remove-renamed-from-clean)
- [`localnet up`](#localnet-up)
  - [`--allow-uncurated` flag (new)](#--allow-uncurated-flag-new)
  - [`--profile` flag (new)](#--profile-flag-new)
  - [`--port-base` flag (new)](#--port-base-flag-new)
- [`localnet pause` / `resume` (new)](#localnet-pause--resume-new)
- [`localnet stop` / `start` (new)](#localnet-stop--start-new)
- [`localnet creds` (new)](#localnet-creds-new)
- [`localnet versions` (new)](#localnet-versions-new)
- [`localnet ui` (new)](#localnet-ui-new)
- [`localnet refresh` (new)](#localnet-refresh-new)
- [`localnet container` (new)](#localnet-container-new)
- [`localnet observability` (new)](#localnet-observability-new)
- [`localnet skills` (new)](#localnet-skills-new)
- [`localnet contracts` / `tx`](#localnet-contracts--tx)
  - [`contracts ls` (new)](#contracts-ls-new)
  - [Endpoint not yet auto-discovered](#endpoint-not-yet-auto-discovered)
- [`localnet dar`](#localnet-dar)
  - [Connection flags per-command](#connection-flags-per-command)
  - [`--instance` flag name](#--instance-flag-name)
- [`localnet token`](#localnet-token)
  - [Additional subcommands (new)](#additional-subcommands-new)
  - [`transfer accept` subcommand](#transfer-accept-subcommand)
  - [`transfer --atomic` flag (experimental, new)](#transfer---atomic-flag-experimental-new)
  - [`allocations settle` deferred](#allocations-settle-deferred)
  - [`burn` requires explicit confirmation](#burn-requires-explicit-confirmation)
  - [`--instance` required flag](#--instance-required-flag)
  - [`--name` collision in `token create`](#--name-collision-in-token-create)
- [`telemetry` (root-level, new)](#telemetry-root-level-new)

---

## Cross-cutting conventions

### Instance name addressing

**Proposal said:** instance name is always passed as `--name <name>` across all commands.

**Shipped:**
- Most lifecycle/inspection commands (`up`, `down`, `restart`, `pause`, `resume`, `status`, `logs`, `creds`, `snapshot`, `restore`) accept the name as **either** a positional argument **or** `--name` — both are equivalent. Example: `dpm localnet up dev` and `dpm localnet up --name dev` do the same thing.
- `remove`, `list`, `doctor`, `refresh`, `metrics` are `--name`-only (no positional arg).
- `dar` subcommands use `--instance` (alias `--name`).
- `token` subcommands use required `--instance`.

**Why:** The positional form is faster to type for interactive use and matches conventions in similar tools (`kubectl`, `docker`). `--name`-only commands are those that are conceptually multi-instance by default (e.g. `list`) or where positional args would be ambiguous.

---

### Machine-readable output flag

**Proposal said:** machine-readable output is requested via `--json`.

**Shipped:** commands use `--format <value>` with accepted values `json`, `text` (and sometimes `table`). Example: `dpm localnet status dev --format json`.

**Why:** `--format` is more flexible (allows future formats such as `yaml` or `table` without adding new flags) and is consistent with the established pattern in tools like `docker` and `gh`.

---

### Command aliases

The following aliases are not in the proposal but are shipped:

| Canonical command | Alias(es) | Notes |
|---|---|---|
| `localnet remove` | `clean` | Backward-compatible after the `clean` → `remove` rename (see [`localnet remove`](#localnet-remove-renamed-from-clean)) |
| `localnet resume` | `unpause` | Matches `docker compose unpause` terminology |
| `localnet observability` | `obs` | Shorter for interactive use |
| `localnet container list` | `ls`, `ps` | Matches Docker CLI conventions |
| `localnet token party ls` | `list` | Consistency within party subcommand |
| `localnet token party rm` | `remove` | Consistency within party subcommand |

`localnet list` has **no** `ls` alias despite the pattern above — adding it would shadow `localnet logs` with a common prefix, increasing ambiguity in tab-completion.

**Behaviour change (removed aliases):** earlier builds shipped `start` as an alias for `up` and `stop` as an alias for `down`. These aliases have been **removed** — `start` and `stop` are now standalone commands with distinct behaviour (see [`localnet stop` / `start`](#localnet-stop--start-new)). `localnet stop` no longer removes containers (use `down` for that), and `localnet start` no longer unconditionally recreates the stack (though it converges to a running instance, falling back to `up` when containers are gone).

---

## `localnet remove` (renamed from `clean`)

**Proposal said:** the destructive teardown verb — the command that removes an instance's data volumes and registry state — was named `clean`.

**Shipped:** the canonical command is `dpm localnet remove`, with `clean` retained as an alias. Both forms are equivalent: `dpm localnet remove dev` and `dpm localnet clean dev` do the same thing. The instance name is now a **positional argument** (`dpm localnet remove <name>`), matching `up`/`down`/`stop`/`start`; `--name <name>` is still accepted for backward compatibility, but passing both the positional and `--name` is an error. `--all` remains mutually exclusive with naming a single instance. Other flags are unchanged (`--force`, `--dry-run`).

**Why:** `remove` names the action plainly — it removes the instance's containers, volumes, and registry state — and reads unambiguously next to the other lifecycle verbs (`down`, `stop`, `remove`), where "clean" could be mistaken for a non-destructive tidy-up. The `clean` alias is kept so existing scripts, CI pipelines, and muscle memory continue to work without a breaking change. Accepting the name positionally aligns `remove` with the rest of the lifecycle verbs, which already take `<name>` positionally.

---

## `localnet up`

### `--allow-uncurated` flag (new)

**Proposal said:** `--version <version>` pins a Splice LocalNet version from the supported set. Unsupported versions were not addressed.

**Shipped:** `--allow-uncurated` lets users pass a Splice tag that is not in the DevKit curated catalogue. DevKit resolves the tag against the upstream Splice GitHub repo and proceeds, printing a warning that the resulting LocalNet is not tested by DevKit.

**Why:** Gives power users and maintainers a path to test prereleases and alpha tags without waiting for a catalogue update, while keeping the default path (no flag) restricted to tested versions.

---

### `--profile` flag (new)

**Proposal said:** per-component toggles for Prometheus and Grafana as a LocalNet configuration model item; the exact mechanism was not specified.

**Shipped:** `--profile <name>` (repeatable) is a flag on `localnet up`. Supported values include `prometheus`, `grafana`, and `observability` (legacy umbrella that activates both). Profiles are persisted in instance state so a subsequent `up` re-enables the same set. The `localnet observability enable/disable` command can toggle sidecars on a running instance without `--profile` at `up` time.

**Why:** Docker Compose profiles are the natural mechanism for optional service groups in the Splice LocalNet stack. Exposing them directly as `--profile` keeps the model transparent and auditable. Persisting the profile set enables reproducible restarts.

---

### `--port-base` flag (new)

**Proposal said:** named instances use explicit port configuration so two LocalNets can run on one machine, but the mechanism for specifying ports was not defined.

**Shipped:** `--port-base <n>` pins host ports deterministically starting from `n` (each service gets `base+N`). With `--port-base 0` (default), ports are auto-allocated with stable reuse across restarts. Every derived port must be free or `up` fails immediately with no silent fallback.

**Why:** Auto-allocation works for single-developer use; `--port-base` is needed for CI layouts and reproducible multi-instance setups where port assignments must be predictable and documented.

---

## `localnet pause` / `resume` (new)

**Proposal said:** not mentioned.

**Shipped:** `dpm localnet pause <name>` and `dpm localnet resume <name>`.

`pause` sends SIGSTOP to all containers in the instance (via `docker compose pause`) — they hold in-memory state and published ports but stop using CPU. `resume` sends SIGCONT (alias `unpause`, matching `docker compose unpause`). No readiness wait is performed on resume.

**Why:** Useful when stepping away briefly without wanting to pay the full boot cost of `down`/`up`. Frees CPU and reduces resource consumption without discarding ledger state. Required for CLI ↔ Web UI parity (the UI exposes a pause/resume action on the instance card).

---

## `localnet stop` / `start` (new)

**Proposal said:** not mentioned as standalone commands. Earlier DevKit builds shipped `stop` and `start` only as aliases for `down` and `up`.

**Shipped:** `dpm localnet stop <name>` and `dpm localnet start <name>` are now first-class lifecycle commands sitting between pause/resume and down/up:

- `stop` gracefully stops the instance's containers (`docker compose stop`) but **keeps** them on disk. CPU and the container runtime are freed; ledger state and the containers themselves survive.
- `start` starts a stopped instance's containers (`docker compose start`), skipping image pulls and stack recreation. If the containers have already been removed (e.g. the instance was `down`ed, or containers were pruned externally), `start` transparently falls back to a full `up` — reusing the recorded Splice version and profiles — with no extra flag or confirmation. `start` accepts `--no-wait` to skip the readiness wait.

The teardown/bring-up ladder is therefore: `pause`/`resume` (freeze, RAM held) → `stop`/`start` (stop containers, kept on disk) → `down`/`up` (remove and recreate containers) → `remove` (alias `clean`; removes data volumes and state).

**Why:** `stop`/`start` fill the gap between the instant-but-RAM-heavy pause and the slow-but-clean down: they free container resources while avoiding the cost of recreating the stack on the next start. Making them standalone commands (rather than aliases) gives users the full Docker Compose lifecycle vocabulary. The intelligent `start` fallback means users never have to remember whether an instance was stopped or downed — `start` always converges to a running instance. Required for CLI ↔ Web UI parity (the UI exposes Stop and Start actions on the instance card).

**Behaviour change:** because `stop`/`start` are no longer aliases, `localnet stop` no longer removes containers and `localnet start` no longer unconditionally recreates the stack. Users who relied on the old alias behaviour should use `down`/`up` explicitly.

---

## `localnet creds` (new)

**Proposal said:** not mentioned as a standalone command. `env` was the credential/config export surface.

**Shipped:** `dpm localnet creds [name]` prints the HS256 JWTs captured at `up` time, in four formats: `table` (default — includes JWTs), `env` (shell-exportable `AUTH_<ROLE>_TOKEN=...` lines), `json` (full credential objects including JWTs), `raw` (single JWT, requires `--role`). All successful LocalNet credential surfaces return raw JWT values because LocalNet is a loopback-only development environment. The `localnet env` and `localnet status` commands no longer expose a redaction opt-in flag.

**Why:** `env` covers Ledger API endpoints and wallet URLs, while `creds` remains the dedicated surface for auth tokens. LocalNet credentials are intentionally usable by default; error messages, audit records, and access logs continue to exclude raw JWTs.

---

## `localnet versions` (new)

**Proposal said:** `--version <version>` in `localnet up` selects the Splice version. Supported versions and a compatibility matrix were mentioned as documentation items, not as a CLI command.

**Shipped:** `dpm localnet versions` is a live command that lists every Splice version in the DevKit curated catalogue plus every tag the upstream Splice GitHub repository currently exposes. Each row has a status: `supported`, `drifted` (force-pushed — security signal), `available` (upstream only, not yet catalogued), or `catalogued-only` (removed upstream). Supports `--offline` and `--format json`.

**Why:** The catalogue cross-reference against upstream helps maintainers catch force-pushed tags early (a security signal) and gives users live visibility into which versions are safe to pin, without consulting external documentation.

---

## `localnet ui` (new)

**Proposal said:** a Web UI exists, but the proposal described it as a dashboard accessible alongside the CLI, not as a separately invocable CLI command.

**Shipped:** `dpm localnet ui` starts the embedded Vite/React HTTP server (default port 7777, loopback-only). Flags: `--port`, `--host`, `--allow-non-loopback`, `--insecure-skip-origin-check`. Non-loopback binding is refused by default as a DNS-rebinding defence; SSH tunnelling is the recommended remote-access path. `--insecure-skip-origin-check` disables the Origin/Referer CSRF gate for local frontend-dev setups (Vite proxy edge cases); the Host allowlist still applies.

**Why:** Packaging the UI launch as a CLI subcommand keeps the single-binary model and lets users control when the UI server is running. The loopback-only default and the `--allow-non-loopback` guard are a deliberate security measure — the UI handles JWTs and party identifiers and is not designed for unauthenticated LAN-wide exposure. The Origin-check skip exists only as an explicit local-dev escape hatch when a frontend proxy cannot keep `Origin` aligned with `Host`.

---

## `localnet refresh` (new)

**Proposal said:** not mentioned.

**Shipped:** `dpm localnet refresh [--name <name>]` triggers an on-demand reconciliation pass that syncs the registry's persisted status with the live `docker compose ps` state. This is the CLI mirror of the background reconciler that runs inside `localnet ui`.

**Why:** Required for CLI ↔ Web UI parity. Useful when a user has stopped containers externally (e.g. via `docker compose down` directly) and wants the registry to reflect that without restarting the UI server.

---

## `localnet container` (new)

**Proposal said:** `dpm localnet restart [service] --name <name>` restarts the full LocalNet or one service.

**Shipped:** Full-instance restart remains `dpm localnet restart`. Per-container operations are under a separate `container` parent:

- `localnet container list <instance>` (aliases `ls`, `ps`) — lists containers with state/health.
- `localnet container restart <instance> <service>` — restarts one container; verifies it belongs to the instance's compose project before acting.
- `localnet container logs <instance> <service>` — tails logs for one container (flags: `--tail`, `--since`).

**Why:** Separating the `container` subtree from top-level lifecycle commands keeps the namespace clean and mirrors the Web UI's Container Health panel. Accepting both the service short name and the full container name (e.g. `splice` or `pr432-splice`) improves UX over the raw Docker form. The membership check before restart is a security measure that prevents a typo or hostile input from restarting an arbitrary host container.

---

## `localnet observability` (new)

**Proposal said:** `dpm localnet metrics` prints Grafana dashboard URLs and a concise text summary. No separate toggle command was proposed; observability components were to be controlled via `--profile` flags at `up` time.

**Shipped:** In addition to `localnet metrics`, a `localnet observability` command (alias `obs`) manages the Prometheus/Grafana sidecars **on a running instance** without restarting Canton:

- `observability enable [--prometheus] [--grafana]` — brings sidecars up.
- `observability disable [--prometheus] [--grafana]` — stops them; Canton is untouched.
- `observability status` — read-only report of which sidecars are active and their URLs.

Both `--prometheus` and `--grafana` flags allow controlling each sidecar independently. With neither flag, both are selected (umbrella semantics). The enabled state is persisted so a subsequent `down`/`up` re-enables it automatically.

**Why:** Enabling observability at `up` time via `--profile` requires a full restart to change. The `observability enable/disable` path lets developers toggle the monitoring stack without disrupting a running ledger — saving the boot cost and preserving in-flight ledger state. Matches the Web UI's "Enable observability now" toggle for CLI ↔ Web UI parity.

---

## `localnet skills` (new)

**Proposal said:** DevKit "may provide optional, editor-agnostic AI agent skill documents." The proposal described them as documentation artifacts, not as CLI commands.

**Shipped:** `dpm localnet skills` is a full subcommand tree:

- `skills list` — lists the embedded skill documents (name, description, filename).
- `skills install [--target claude|codex] [--dir <path>] [--force]` — writes the embedded skill documents into the appropriate agent skills directory (`~/.claude/skills/` for Claude, `~/.codex/skills/` for Codex). Clobber-safe by default: a destination that exists with different content is skipped unless `--force` is passed.

The embedded skill docs are the same artifacts that back the Web UI's Agent Skills screen, ensuring CLI and UI show the same content.

**Why:** Users need a one-step way to install skill documents without manually locating and copying files. The clobber-safe default protects hand-edited skill docs from being silently overwritten on re-install. Required for CLI ↔ Web UI parity (the Web UI's Agent Skills screen surfaces the same embedded docs).

---

## `localnet contracts` / `tx`

### `contracts ls` (new)

**Proposal said:** `dpm localnet contracts watch` — live tail of create/archive events.

**Shipped:** `contracts watch` is present and matches the proposal. In addition, `contracts ls` lists active contracts via a one-shot query rather than a live stream.

**Why:** A non-streaming snapshot is more useful than a continuous watch in CI and scripted contexts where the caller wants to assert on current state without keeping a long-lived process open.

---

### Endpoint not yet auto-discovered

**Proposal said:** commands connect to the LocalNet participants automatically (implied by the named-instance model).

**Shipped:** `contracts` and `tx` commands require callers to pass `--endpoint host:port` explicitly. Auto-discovery of the gRPC participant port from registry state is not yet implemented. A comment in `localnet.go` documents this as pending work.

**Why:** Auto-discovery was deferred to avoid blocking the initial contract/tx CLI release. The explicit `--endpoint` flag is a deliberate interim design — it keeps the commands usable against any Ledger API endpoint (not just DevKit-managed instances) until the auto-discovery path lands.

---

## `localnet dar`

### Connection flags per-command

**Proposal said:** DAR commands connect to participants via the named instance implicitly.

**Shipped:** Each `dar` subcommand carries its own connection flags: `--admin-host`, `--token`, `--insecure` (defaults to `true`), `--ca-cert`, `--instance` (alias `--name`), `--role` (default `app-user`). There is no standalone `dar connect` command.

**Why:** Per-command connection flags make the DAR subcommands usable against any Ledger API endpoint, not just DevKit-managed instances. This gives operators more flexibility in CI and multi-environment workflows without requiring a running LocalNet registry.

---

### `--instance` flag name

**Proposal said:** instance selection is `--name <name>` uniformly.

**Shipped:** `dar` subcommands use `--instance` as the primary flag name (with `--name` as an alias).

**Why:** In `dar` contexts, `--name` is ambiguous between the instance name and the DAR/package name. Using `--instance` as the primary name eliminates that ambiguity and makes commands self-documenting at a glance.

---

## `localnet token`

### Additional subcommands (new)

**Proposal said:** `token create`, `token mint`, `token transfer`, `token burn`, `token balance`.

**Shipped:** all five from the proposal, plus:

| New command | Purpose |
|---|---|
| `token balances` | Portfolio-style matrix view across all instruments for one or more parties |
| `token summary` | Aggregate stats for one instrument (supply, holder count, recent activity) |
| `token activity` | Recent transaction history feed for an instrument (`--limit` defaults to 50) |
| `token party new <alias>` | Register a named party alias for use in token commands |
| `token party ls` | List registered party aliases |
| `token party rm <alias>` | Remove a party alias |
| `token faucet <party> <amount>` | Fund a party with an auto-accepted transfer (no recipient interaction needed) |
| `token demo` | One-step provision: creates a DEMO instrument and seeds a holder wallet |
| `token identity` | List the act-as identities (roles) available for the instance's token commands |
| `token allocations` | List V2 DvP allocations, optionally filtered to one authorizer party |
| `token allocations withdraw` | Withdraw a V2 allocation (only after its settlement deadline) |
| `token allocations cancel` | Cancel a V2 allocation |

**Why:** The alias registry (`token party`) improves UX by eliminating repeated `--party <long-id>` flags across commands. `faucet` and `demo` target workshop and onboarding use cases where speed matters more than exercising the full CIP-0112 two-phase flow. `balances`, `summary`, and `activity` provide portfolio-level and historical views that are essential for verifying token operations during testing.

---

### `transfer accept` subcommand

**Proposal said:** `token transfer` as a single command.

**Shipped:** `token transfer` initiates a transfer; `token transfer accept` accepts a pending incoming transfer. CIP-0112 transfers are two-phase (offer + accept), so both halves are exposed as CLI subcommands.

**Why:** The two-phase model is required by the CIP-0112 protocol — it is not a simplification but a faithful implementation of the standard. Exposing both steps gives scripts and workshops full control over the accept timing, enabling realistic multi-party test scenarios.

---

### `transfer --atomic` flag (experimental, new)

**Proposal said:** not mentioned; `token transfer` was a single-shot command.

**Shipped:** `token transfer --atomic` is an **experimental** flag that only takes effect together with `--auto-accept`. When both are set, the transfer and the receiver-side accept are batched into one all-or-nothing `BatchingUtilityV2` transaction (on-ledger test tokens only). On current Splice this is **not yet functional** — the accept leg cannot reference the transfer leg's instruction within a single batch, so the command errors and nothing commits; the default sequential path (`--auto-accept` alone) is the supported behaviour. In the Web UI the atomic checkbox is disabled unless auto-accept is on and carries an explicit experimental warning.

**Why:** The flag is shipped ahead of ledger support so the atomic-settlement path is wired and testable end-to-end the moment Splice can reference a batched instruction. Gating it behind `--auto-accept`, defaulting it off, and surfacing an experimental warning keeps the unsupported path from being reached accidentally.

---

### `allocations settle` deferred

**Proposal said:** not mentioned (allocations/DvP settlement were not part of the proposal's token surface).

**Shipped:** the DvP allocation surface ships `allocations` (list), `allocations withdraw`, and `allocations cancel`, but the **`allocations settle` verb is intentionally not exposed** on the CLI or in the Web UI. The settlement factory plumbing exists and is unit-tested, but the end-to-end settle path is not yet functional against current Splice, so the user-facing action is withheld until it works rather than shipping a command that always fails.

**Why:** Exposing a settle action that cannot succeed would be a misleading dead end. Withholding it — while keeping withdraw/cancel, which do work — keeps the surface honest; the verb will be re-enabled in the same place once the settlement flow is functional.

---

### `burn` requires explicit confirmation

**Proposal said:** `token burn {token-name} {amount}` as a straightforward command.

**Shipped:** `token burn` prompts for confirmation before executing because the operation is irreversible. The prompt is bypassed with `--yes` / `-y`.

**Why:** Guarding an irreversible ledger operation with a confirmation prompt is standard CLI practice and prevents accidental burns in interactive sessions. The `--yes` flag preserves full scriptability for automation.

---

### `--instance` required flag

**Proposal said:** token commands connect to the active or `--name`-selected instance.

**Shipped:** `--instance` is a **required** flag on all `token` subcommands (no default or auto-resolution from a single registered instance).

**Why:** Making `--instance` explicit prevents token commands from silently targeting the wrong LocalNet when multiple instances are registered — a correctness and safety measure, not an inconvenience.

---

### `--name` collision in `token create`

**Proposal said:** instance selection via `--name <name>`.

**Shipped:** In `token create`, `--name` refers to the **instrument name** (e.g. `--name "My Token"`), not the instance. The instance is selected via `--instance`. This is an intentional exception to the general `--name` = instance name convention.

**Why:** The instrument name is the primary user-facing input in the token creation wizard. Using `--name` for it matches natural language ("name this token") and makes the interactive wizard more intuitive, even though it breaks the global `--name` = instance convention elsewhere.

---

## `telemetry` (root-level, new)

**Proposal said:** not mentioned. Adoption measurement was described as a reporting/documentation exercise.

**Shipped:** A root-level `telemetry` command (sibling to `localnet`, not nested under it) manages privacy-preserving usage telemetry:

- `telemetry on` / `off` — opt in or out.
- `telemetry status` — show current state and the anonymous install ID.
- `telemetry preview [--format]` — show the payload that would be sent without sending it.
- `telemetry flush` — send any buffered events immediately.
- `telemetry reset-id` — generate a new anonymous ID.

Telemetry is **on by default** with opt-out via `DPM_TELEMETRY=off` or `DO_NOT_TRACK=1`. An internal hidden subcommand `_record-install-surface <surface>` is used by install scripts to record the distribution channel.

**Why:** Provides the adoption signals described in Milestone 4 (install counts, usage trends) in a privacy-preserving, opt-out model without requiring manual tracking. The opt-out via standard `DO_NOT_TRACK` honours widely adopted ecosystem conventions. Placing it at the root level (not under `localnet`) reflects that it is a tool-wide concern, not a LocalNet-specific one.
