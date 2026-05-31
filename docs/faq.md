# FAQ

Common questions about canton-devkit. See also
[troubleshooting.md](troubleshooting.md) for failure-mode fixes.

## General

**What is canton-devkit?**
A single-binary developer tool for running and operating a Canton
**LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet …`, or `dpm localnet …` under DPM) and an
embedded Web UI for the same operations.

**CLI or Web UI — which should I use?**
Both expose the same operations (CLI ↔ UI parity is a project rule). Use
the CLI for scripting/CI; `canton-devkit localnet ui` for a dashboard,
the contract explorer, DAR management, metrics, and the token workspace.

**Does it fork or patch Splice?**
No. It downloads the upstream `cluster/compose/localnet/` tree pinned by
immutable commit SHA and verified by SHA-256 after extraction. See
[versions.md](versions.md).

**Which platforms are supported?**
macOS (arm64) and Linux (amd64) are the primary, CI-tested targets.
Windows (amd64) binaries are published; cross-platform coverage is
tracked under the release matrix.

## Versions

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

## Tokens (CIP-0112 / V2)

**V1 or V2?**
This tool targets **Token Standard V2 (CIP-0112)** only. V1 / CIP-0056 is
not supported. See [tokens.md](tokens.md).

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

## Operations

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
warning in [troubleshooting.md](troubleshooting.md#snapshot-consistency)
and `localnet snapshot --help`. Stop the instance for a fully consistent
snapshot.
