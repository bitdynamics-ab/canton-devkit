---
title: LocalNet Lifecycle
description: Zero to a running Canton LocalNet — start, inspect, run multiple instances, pin ports, tear down, and answers to common questions.
---

Canton DevKit is a single-binary developer tool for running and operating a
Canton **LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet …`, or `dpm localnet …` under DPM) and an
embedded Web UI for the same operations.

This guide walks the full lifecycle: bring an instance up, inspect it,
run several at once, and clean up. See
[Installation & Getting Started](../../getting-started/) first if you
haven't installed DevKit yet.

## Zero to running LocalNet

```bash
# 1. Check the host (no changes made)
canton-devkit localnet doctor

# 2. Start a named LocalNet (downloads Splice on first run; waits for readiness)
canton-devkit localnet up --name demo

# 3. Inspect it — endpoints, health, credentials
canton-devkit localnet status --name demo

# 4. Export endpoints for your app/tests
eval "$(canton-devkit localnet env --name demo)"

# 5. Upload a DAR
canton-devkit localnet dar upload ./my-app.dar --instance demo

# 6. Watch live contracts. The participant gRPC endpoint isn't
#    host-published by default, so pass --endpoint host:port
#    (auto-discovery from --name is not yet supported). Find the
#    port under "participant_ledger_app-user" in `status` output.
canton-devkit localnet contracts watch --name demo --endpoint localhost:<ledger-port>

# 7. Tear it down
canton-devkit localnet down --name demo
```

Replace `canton-devkit` with `dpm` if you installed via the DPM
component. `up` waits for the stack to become healthy (Splice
onboarding can take several minutes on a cold start) and prints the
service endpoints and credential locations when ready.

## Running two LocalNets at once

```bash
canton-devkit localnet up --name alpha
canton-devkit localnet up --name beta
canton-devkit localnet list           # both instances + their state
```

Each named instance gets its own deterministic compose project,
network, and host ports, so they don't collide.

### Explicit, deterministic ports (`--port-base`)

By default DevKit **auto-allocates** host ports — the simplest path, and
it never conflicts because the kernel hands out free ports. When you need
a **fixed, predictable** port map instead — reproducible CI layouts, or
multiple instances at known offsets — pin a base:

```bash
canton-devkit localnet up --name alpha --port-base 20000   # services at 20000+N
canton-devkit localnet up --name beta  --port-base 30000   # services at 30000+N
```

Each service lands on `base + N`, identically across runs and machines.
Every derived port must be free or `up` fails fast (no silent fallback) —
so the layout you asked for is the layout you get. Pre-flight a base
before bringing anything up:

```bash
canton-devkit localnet doctor --port-base 20000   # are 20000..20000+services free?
```

The same control is available in the Web UI's **New instance** dialog
under *Advanced → Fixed port base*.

## Uninstall / clean up

```bash
# stop + remove a single instance's containers, volumes, and state
canton-devkit localnet clean --name demo

# remove every DevKit-managed instance
canton-devkit localnet clean --all

# remove the standalone binary
sudo rm /usr/local/bin/canton-devkit
```

`clean` refuses to touch a running instance unless you pass `--force`
(which tears it down first). Use `--dry-run` to preview.

## FAQ

Common questions about canton-devkit. See also
[Troubleshooting](../../reference/troubleshooting/) for failure-mode fixes.

### General

**What is canton-devkit?**
A single-binary developer tool for running and operating a Canton
**LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet …`, or `dpm localnet …` under DPM) and an
embedded Web UI for the same operations.

**CLI or Web UI — which should I use?**
Both expose the same operations — the two surfaces are kept in parity
by design. Use the CLI for scripting/CI; `canton-devkit localnet ui`
for a dashboard, the contract explorer, DAR management, metrics, and
the token workspace.

**Does it fork or patch Splice?**
No. It downloads the upstream `cluster/compose/localnet/` tree pinned by
immutable commit SHA and verified by SHA-256 after extraction. See the
[Splice version catalogue](../../reference/versions/).

**Which platforms are supported?**
macOS (arm64), Linux (amd64), and Windows (amd64) are the released,
tested targets. Other OS/arch combinations may work (DevKit only
orchestrates Docker) but are untested — `localnet doctor` warns on
unsupported platforms. See the
[compatibility matrix](../../getting-started/#compatibility-matrix).

### Versions

**What does `--version latest` give me?**
The curated catalogue's `latest_alias` (a production-ready stable
release). `localnet versions` lists the full catalogue; `--allow-uncurated`
plus an explicit tag lets you run an upstream version not yet curated.

**What's the difference between the curated catalogue and runtime
resolution?**
Curated entries (in `versions.json`) are tested and pinned by commit +
content SHA. Uncurated tags are resolved live against GitHub and cached
locally — handy for trying a brand-new upstream release before it's
curated.

### Tokens (CIP-0112 / V2)

**V1 or V2?**
This tool targets **Token Standard V2 (CIP-0112)** only. V1 / CIP-0056 is
not supported. See the [Tokens guide](../tokens/).

**Why is V2 "alpha" and what does `--profile tokens-v2` do?**
V2 runs on a special upstream Splice build (alpha protocol 35) on the
`-dev` image repo. `--profile tokens-v2` injects the Canton config that
enables alpha-version-support + protocol 35. Without it the stack can't
run the V2 protocol; `doctor` warns.

**Why can't I mint or burn Amulet?**
Amulet (Canton Coin) has no developer-facing mint/burn surface — those
are governance operations. The workspace observes Amulet and can transfer
it, but Mint/Burn are gated. Create your own `splice-test-token-v2`
instrument for full create → mint → transfer → burn.

**How does burn work if the example token has no burn choice?**
Correct — `splice-test-token-v2` has no protocol-level standalone burn.
On LocalNet you control the holding's signatories (account parties +
admin), so `token burn` archives the holder's `Holding` contracts
directly and returns change. Supply = sum of holdings, so this removes
the burned amount from circulation.

**Why are party aliases safe here but not in production?**
On LocalNet the `unsafe` dev secret signs for every party, so "you own
all parties" is true and the god-mode workspace is appropriate. That
assumption does **not** hold on a real network — the dev JWTs are
loopback-only and must never be reused off-box.

### Operations

**Can I run more than one instance at once?**
Yes. Each `--name` gets isolated Docker resources and a port block.
`localnet list` shows them all.

**Where does state live?**
`~/.canton-devkit/localnet/<name>/` (per-instance registry + data) and
`~/.canton-devkit/cache/` (downloaded Splice trees). Removing the cache
is safe; it re-downloads on next `up`.

**Snapshot / restore — is it crash-consistent?**
Snapshots capture Docker volumes + registry state. They are **not**
guaranteed application-consistent for a *running* instance — see the
warning in [Troubleshooting](../../reference/troubleshooting/#snapshot-consistency)
and `localnet snapshot --help`. Stop the instance for a fully consistent
snapshot.
