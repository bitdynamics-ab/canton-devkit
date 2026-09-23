# FAQ: Canton LocalNet & DevKit

Common questions about canton-devkit and Canton LocalNet. See also
[troubleshooting](troubleshooting.md) for failure-mode fixes and
[LocalNet lifecycle](localnet-lifecycle.md) for the full operational guide.

## General

### What is Canton LocalNet?

**Canton LocalNet** is a multi-validator local Canton Network for
development and integration testing. It runs sequencers, mediators,
participants, and Splice apps in Docker so you can exercise multi-party
workflows without connecting to a shared network. It is not intended for
production. Official topology and cn-quickstart docs live on
[docs.canton.network](https://docs.canton.network/sdks-tools/development-tools/localnet);
DevKit is how many teams start and manage that same Splice LocalNet stack
from a single CLI or Web UI.

### What is canton-devkit?

A single-binary developer tool for running and operating a Canton
**LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet <command>`, or `dpm localnet <command>` under DPM) and an
embedded Web UI for the same operations.

### How do I start a Canton LocalNet with DevKit?

Install DevKit ([Installation & Getting Started](getting-started.md)), then:

```bash
canton-devkit localnet doctor
canton-devkit localnet up --name demo
canton-devkit localnet status --name demo
eval "$(canton-devkit localnet env --name demo)"
```

`dpm localnet …` is equivalent if you installed the DPM component.
Details: [LocalNet lifecycle](localnet-lifecycle.md).

### How does DevKit relate to cn-quickstart / official LocalNet?

DevKit downloads the bare Splice LocalNet compose tree from
[`canton-network/splice`](https://github.com/canton-network/splice)
(`cluster/compose/localnet/`) and manages lifecycle (`up` / `down` /
`status` / `creds` / …).
[`cn-quickstart`](https://github.com/digital-asset/cn-quickstart) builds
an App-Provider quickstart *on top of* that same LocalNet base. Use
DevKit when you want one-command LocalNet operations; use cn-quickstart
when you want its full-stack sample app. See
[versions.md](versions.md#not-to-be-confused-with-cn-quickstart).

### CLI or Web UI — which should I use?

Both expose the same operations — the two surfaces are kept in parity
by design. Use the CLI for scripting/CI; `canton-devkit localnet ui`
for a dashboard, the contract explorer, DAR management, metrics, and
the token workspace.

### Does it fork or patch Splice?

No. It downloads the upstream `cluster/compose/localnet/` tree pinned by
immutable commit SHA and verified by SHA-256 after extraction. See
[versions.md](versions.md).

### Which platforms are supported?

macOS (arm64), Linux (amd64), and Windows (amd64) are the released,
tested targets. Other OS/arch combinations may work (DevKit only
orchestrates Docker) but are untested — `localnet doctor` warns on
unsupported platforms. See the compatibility matrix in
[getting-started.md](getting-started.md#4-compatibility-matrix).

## Versions

### What does `--version latest` give me?

The curated catalogue's `latest_alias` (a production-ready stable
release). `localnet versions` lists the full catalogue; `--allow-uncurated`
plus an explicit tag lets you run an upstream version not yet curated.

### What's the difference between the curated catalogue and runtime resolution?

Curated entries (in `versions.json`) are tested and pinned by commit +
content SHA. Uncurated tags are resolved live against GitHub and cached
locally — handy for trying a brand-new upstream release before it's
curated.

## Tokens

### V1 or V2?

Both, routed per instrument. Reads and transfers work against
**CIP-0056** (Final) instruments — what existing assets such as Canton
Coin implement on stable Splice releases. Creating a **new** instrument
uses **Token Standard V2 (CIP-0112)**, released in Splice 0.6.11. See
[tokens.md](tokens.md).

### Does V2 require `--profile tokens-v2`?

No. Splice 0.6.11 and newer include V2 on the stable release path. The
`token-standard-v2` version alias and `--profile tokens-v2` reproduce the
older alpha environment and are not needed for a normal current LocalNet.

### Why can't I mint or burn Amulet?

Amulet (Canton Coin) has no developer-facing mint/burn surface — those
are governance operations. The workspace observes Amulet and can transfer
it, but Mint/Burn are gated. Create your own `splice-test-token-v2`
instrument for full create → mint → transfer → burn.

### How does burn work if the example token has no burn choice?

Correct — `splice-test-token-v2` has no protocol-level standalone burn.
On LocalNet you control the holding's signatories (account parties +
admin), so `token burn` archives the holder's `Holding` contracts
directly and returns change. Supply = sum of holdings, so this removes
the burned amount from circulation.

### How does the authorization work differently in production?

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

### Can I run more than one instance at once?

Yes. Each `--name` gets isolated Docker resources and a port block.
`localnet list` shows them all.

### Where does state live?

`~/.canton-devkit/localnet/<name>/` (per-instance registry + data) and
`~/.canton-devkit/cache/` (downloaded Splice trees). Removing the cache
is safe; it re-downloads on next `up`.

### Snapshot / restore — is it crash-consistent?

Snapshots capture a logical PostgreSQL dump (`pg_dumpall`) of the
instance's database plus its registry state. The instance must be
**running** — `pg_dumpall` reads from the live Postgres. DevKit pauses
the node containers for the duration of the dump, so the snapshot is
application-consistent, not merely crash-consistent. See
`localnet snapshot --help`.
