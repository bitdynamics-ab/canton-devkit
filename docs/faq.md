# FAQ

Common questions about canton-devkit. See also
[troubleshooting.md](troubleshooting.md) for failure-mode fixes.

## General

**What is canton-devkit?**
A single-binary developer tool for running and operating a Canton
**LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet <command>`, or `dpm localnet <command>` under DPM) and an
embedded Web UI for the same operations.

**CLI or Web UI — which should I use?**
Both expose the same operations — the two surfaces are kept in parity
by design. Use the CLI for scripting/CI; `canton-devkit localnet ui`
for a dashboard, the contract explorer, DAR management, metrics, and
the token workspace.

**Does it fork or patch Splice?**
No. It downloads the upstream `cluster/compose/localnet/` tree pinned by
immutable commit SHA and verified by SHA-256 after extraction. See
[versions.md](versions.md).

**Which platforms are supported?**
macOS (arm64), Linux (amd64), and Windows (amd64) are the released,
tested targets. Other OS/arch combinations may work (DevKit only
orchestrates Docker) but are untested — `localnet doctor` warns on
unsupported platforms. See the compatibility matrix in
[getting-started.md](getting-started.md#4-compatibility-matrix).

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

## Tokens

**V1 or V2?**
Both, routed per instrument. Reads and transfers work against
**CIP-0056** (Final) instruments — what existing assets such as Canton
Coin implement on stable Splice releases. Creating a **new** instrument
uses **Token Standard V2 (CIP-0112**, approved but not yet final**)**,
which requires the alpha track below. See [tokens.md](tokens.md).

**Why is V2 "alpha" and what does `--profile tokens-v2` do?**
V2 runs on a special upstream Splice build (alpha protocol 35) on the
`-dev` image repo. `--profile tokens-v2` injects the Canton config that
enables alpha-version-support + protocol 35. Without it the stack can't
run the V2 protocol; `up` warns loudly if you select the alpha version
without the profile.

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

**How does the authorization work differently in production?**
On LocalNet, token commands authenticate with the **validator-backend
dev JWT** — a static token signed with the validator node's hardcoded
development secret. That credential can be granted act-as/read-as rights
for **any** party on the node, so your application can use a single token
for every party you allocate on the LocalNet validator (`bob`, `alice`, …)
and transfer, mint, or query on behalf of all of them.

Production networks won't expose that model: each party uses its **own**
credentials, tokens are issued per session (not static JWTs), and you
should not use backend credentials to sign for other parties on the
network.

## Operations

**Can I run more than one instance at once?**
Yes. Each `--name` gets isolated Docker resources and a port block.
`localnet list` shows them all.

**Where does state live?**
`~/.canton-devkit/localnet/<name>/` (per-instance registry + data) and
`~/.canton-devkit/cache/` (downloaded Splice trees). Removing the cache
is safe; it re-downloads on next `up`.

**Snapshot / restore — is it crash-consistent?**
Snapshots capture a logical PostgreSQL dump (`pg_dumpall`) of the
instance's database plus its registry state. The instance must be
**running** — `pg_dumpall` reads from the live Postgres. DevKit pauses
the node containers for the duration of the dump, so the snapshot is
application-consistent, not merely crash-consistent. See
`localnet snapshot --help`.
