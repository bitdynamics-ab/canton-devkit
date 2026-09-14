# HackCanton Season 3 starter

Day-one LocalNet for teams who have never run Canton. Install, one
working example, then the breaks that eat hackathon hours. Deeper
guides are linked at the bottom.

`dpm localnet <cmd>` and `canton-devkit localnet <cmd>` are the same
command tree — use whichever binary you installed.

## 1. Install

You need Docker. DevKit never installs Docker, never edits the daemon,
and never changes host permissions.

| Requirement | Why | Check |
|---|---|---|
| Docker Engine / Desktop | LocalNet runs as containers | `docker version` |
| Docker Compose **v2** | LocalNet is a compose project | `docker compose version` |
| ~8 GB free RAM for Docker | Splice is memory-hungry (12 GB recommended) | Docker Desktop → Settings → Resources |
| ~20 GB free disk | Images + volumes | `df -h` |

Released, tested platforms: macOS arm64 (Apple Silicon), Linux amd64,
Windows amd64.

### Fastest path (macOS Apple Silicon / Linux x86_64)

```bash
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
```

The installer puts the binary in `~/.local/bin` by default and warns if
that directory is not on your `PATH`. Open a new terminal after
installing.

Homebrew:

```bash
brew tap bitdynamics-ab/canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
```

**Windows (amd64):** download the `.zip` from
[GitHub Releases](https://github.com/bitdynamics-ab/canton-devkit/releases)
and follow the PowerShell steps in
[Installation & Getting Started](getting-started.md). Docker Desktop
needs the WSL 2 backend.

**Already have a Daml project?** Install DevKit as a DPM component
(`dpm install package`, then `dpm localnet …`). Full `daml.yaml` steps
are in that same guide.

Then check the host — this never modifies anything:

```bash
canton-devkit localnet doctor
```

Exit `0` means ready (warnings are allowed). Exit `2` means a check
failed; the output prints a copy-pasteable fix. `doctor` is the same
preflight `localnet up` runs.

## 2. One working example

Bring up a named LocalNet, then launch a transferable demo token. You
do not need a DAR file for this.

```bash
canton-devkit localnet doctor
canton-devkit localnet up demo
canton-devkit localnet status demo
canton-devkit localnet token demo --instance demo
canton-devkit localnet token balances --instance demo
```

What that does:

- `up demo` downloads Splice on first run and **blocks until healthy**.
  A cold start takes several minutes. That is normal. If it sits on
  "waiting for healthy" until timeout, Docker memory is usually too
  low — see below.
- `up` defaults to `--version latest` (the curated catalogue alias).
  Creating a new Token Standard V2 instrument needs Splice **0.6.11 or
  newer**. Do not pin an older tag for this example. List curated tags
  with `canton-devkit localnet versions`.
- `token demo --instance demo` allocates parties `demo-issuer` and
  `demo-holder`, creates a `DEMO` instrument, and mints the initial
  supply to the holder. Same orchestration as the Web UI **Launch demo
  token** button. `--instance` is required; the participant ledger
  endpoint is auto-discovered from `status`.
- `token balances` prints the party × instrument matrix for the
  instance.

Optional — move some DEMO (on LocalNet you own both parties, so this
can settle in one step):

```bash
canton-devkit localnet token transfer --instance demo \
  --instrument DEMO --from demo-holder --to demo-issuer --amount 250 --auto-accept
canton-devkit localnet token balances --instance demo
```

### Dashboard and app wiring

```bash
canton-devkit localnet ui          # default http://127.0.0.1:7777/ (loopback only)
eval "$(canton-devkit localnet env demo)"
```

`env` exports endpoints, party IDs, and JWTs for tests and your app.
Those JWTs are **dev-only** — valid against this LocalNet, not
DevNet / TestNet / MainNet.

When you have a DAR of your own:

```bash
canton-devkit localnet dar upload ./my-app.dar --instance demo
```

Tear down containers (data volumes kept):

```bash
canton-devkit localnet down demo
```

Throw the instance away entirely (volumes and registry state):
`canton-devkit localnet remove demo`.

## 3. Things that commonly break

Run `canton-devkit localnet doctor` before anything else. Full
write-ups live in [troubleshooting](troubleshooting.md).

| Symptom | Cause | Fix |
|---|---|---|
| `doctor` says **Docker daemon** ✗ | Docker not running | Start Docker Desktop, or `sudo systemctl start docker` |
| `doctor` says **Compose v2** ✗ | Only Compose v1 present | Upgrade so `docker compose version` works (`docker compose`, not `docker-compose`) |
| `up` hangs at "waiting for healthy", or Canton containers OOM-loop | Docker memory below the version floor (~8 GiB for Splice 0.6.x) | Raise Docker Desktop → Settings → Resources to the value `doctor` prints. Two instances on 8 GB will OOM. |
| `PORTS_IN_USE` on `up` | Another instance or a stale container holds the port block | `canton-devkit localnet list`, then `localnet down <other>` — or pick a different instance name |
| Linux: `permission denied` on the Docker socket | User not in the `docker` group | `sudo usermod -aG docker $USER`, then log out and back in |
| macOS: "cannot be opened because the developer cannot be verified" | Gatekeeper quarantine | `xattr -d com.apple.quarantine $(which canton-devkit)` |
| `command not found: canton-devkit` after the curl installer | `~/.local/bin` is not on `PATH` | Add it and open a new terminal |
| Instance name rejected | Names must be DNS labels | Lowercase `[a-z0-9-]`, 1–63 chars, start and end alphanumeric. No underscores, no `MyStack`. |
| `token create` / mint to another participant: package not vetted | Test-token DAR missing on that participant | `token create --instance <name>` uploads and vets on every LocalNet participant. If the fetch failed: `localnet dar upload <dar> --instance <name> --all-participants` |
| Can't mint or burn Amulet in the CLI or Web UI | Amulet has no developer mint/burn surface | Use your own instrument (`token demo` or `token create`) |
| Token or ledger commands can't find a JWT after a failed `up` | Credentials are captured only when `up` finishes | Re-run `localnet up` to completion. `localnet creds demo --role app-provider --format raw` prints a captured JWT |
| Web UI / Explorer shows stale ports after a restart | Docker reassigned ephemeral host ports | Re-read them from `localnet status demo`. DevKit re-captures within ~15 s; or run `localnet restart demo` |

Still stuck: `canton-devkit localnet logs demo` (repeat `--service <svc>`
to filter), then file a
[GitHub issue](https://github.com/bitdynamics-ab/canton-devkit/issues)
with the full `doctor` output and the failing command.

## Next

- [Installation & Getting Started](getting-started.md) — DPM, checksums, Windows, `go install`
- [LocalNet lifecycle](localnet-lifecycle.md) — multiple instances, `--port-base`, pause / stop / down
- [Tokens](tokens.md) — create / mint / transfer / burn beyond the demo
- [Explorer](explorer.md) — Active Contract Set and transactions in the Web UI
- [Troubleshooting](troubleshooting.md) · [FAQ](faq.md) ·
  [Known limitations](limitations.md)
