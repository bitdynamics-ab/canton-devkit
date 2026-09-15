# HackCanton Season 3 starter

canton-devkit runs and tests your Daml application in a LocalNet.
Both `dpm localnet <cmd>` and `canton-devkit localnet <cmd>` use the same command tree.

Telegram support channel: https://t.me/+ysKrAz_QALk5NTM0

## 1. Install

| Requirement | Why | Check |
|---|---|---|
| Docker Engine / Desktop | LocalNet runs as containers | `docker version` |
| ~8 GB free RAM for Docker | Splice needs memory (12 GB recommended) | Docker Desktop → Settings → Resources |
| ~20 GB free disk | Images + volumes | `df -h` |

Tested platforms: macOS arm64 (Apple Silicon), Linux amd64, Windows amd64.

### Fast path (macOS Apple Silicon / Linux x86_64)

```bash
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
```

The installer places the binary in `~/.local/bin` by default.
It warns if that directory is not on your `PATH`.
Open a new terminal after you install.

Homebrew:

```bash
brew tap bitdynamics-ab/canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
```

**Windows (amd64):** download the `.zip` from
[GitHub Releases](https://github.com/bitdynamics-ab/canton-devkit/releases).
Follow the PowerShell steps in
[Installation & Getting Started](getting-started.md).
Docker Desktop needs the WSL 2 backend.

**Already have a Daml project?** Install DevKit as a DPM component
(`dpm install package`, then `dpm localnet …`).
Full `daml.yaml` steps are in that same guide.

Then check the host. This command does not change anything:

```bash
canton-devkit localnet doctor
```

Exit `0` means ready. Warnings do not fail the check.
Exit `2` means a check failed.
The output prints a fix you can copy.
`doctor` is the same preflight that `localnet up` runs.

## 2. One working example

Start a named LocalNet, then start a transferable demo token.
You do not need a DAR file for this.

```bash
canton-devkit localnet up demo
canton-devkit localnet status demo
canton-devkit localnet token demo --instance demo
canton-devkit localnet token balances --instance demo
```

What that does:

- `up demo` downloads Splice on first run and **waits until healthy**.
  A cold start takes several minutes. That is normal.
  If it stays on "waiting for healthy" until timeout, Docker memory is usually too low.
  See the table below.

- `up` defaults to `--version latest` (the catalogue alias).
  A new Token Standard V2 instrument needs Splice **0.6.11 or newer**.
  Do not pin an older tag for this example.
  List catalogue tags with `canton-devkit localnet versions`.

- `token demo --instance demo` allocates parties `demo-issuer` and `demo-holder`.
  It creates a `DEMO` instrument and mints the initial supply to the holder.
  This matches the Web UI **Launch demo token** button.
  You must pass `--instance`.
  The participant ledger endpoint comes from `status`.

- `token balances` prints the party × instrument matrix for the instance.

Optional. Move some DEMO.
On LocalNet you own both parties, so the transfer can settle in one step:

```bash
canton-devkit localnet token transfer --instance demo \
  --instrument DEMO --from demo-holder --to demo-issuer --amount 250 --auto-accept
canton-devkit localnet token balances --instance demo
```

### Dashboard and app wiring

```bash
canton-devkit localnet ui  
eval "$(canton-devkit localnet env demo)"
```

`env` exports endpoints, party IDs, and JWTs for tests and your app.
Those JWTs are **dev-only**.
They work against this LocalNet.
They do not work against DevNet, TestNet, or MainNet.

When you have your own DAR:

```bash
canton-devkit localnet dar upload ./my-app.dar --instance demo
```

Stop containers (data volumes stay):

```bash
canton-devkit localnet down demo
```

Remove the instance fully (volumes and registry state):
`canton-devkit localnet remove demo`.

## 3. Troubleshooting

Run `canton-devkit localnet doctor` before other commands.
Full write-ups are in [troubleshooting](troubleshooting.md).

| Symptom | Cause | Fix |
|---|---|---|
| `doctor` says **Docker daemon** FAILED | Docker is not running | Start Docker Desktop, or `sudo systemctl start docker` |
| `doctor` says **Compose v2** FAILED | Only Compose v1 is present | Upgrade so `docker compose version` works (`docker compose`, not `docker-compose`) |
| `up` hangs at "waiting for healthy", or Canton containers OOM-loop | Docker memory is below the version floor (~8 GiB for Splice 0.6.x) | Raise Docker Desktop → Settings → Resources to the value `doctor` prints. Two instances on 8 GB will OOM. |
| `PORTS_IN_USE` on `up` | Another instance or a stale container holds the port block | `canton-devkit localnet list`, then `localnet down <other>`. Or pick a different instance name. |
| Linux: `permission denied` on the Docker socket | User is not in the `docker` group | `sudo usermod -aG docker $USER`, then log out and back in |
| macOS: "cannot be opened because the developer cannot be verified" | Gatekeeper quarantine | `xattr -d com.apple.quarantine $(which canton-devkit)` |
| `command not found: canton-devkit` after the curl installer | `~/.local/bin` is not on `PATH` | Add it and open a new terminal |
| Instance name rejected | Names must be DNS labels | Lowercase `[a-z0-9-]`, 1 to 63 chars, start and end alphanumeric. No underscores, no `MyStack`. |
| `token create` / mint to another participant: package not vetted | Test-token DAR is missing on that participant | `token create --instance <name>` uploads and vets on every LocalNet participant. If the fetch failed: `localnet dar upload <dar> --instance <name> --all-participants` |
| Cannot mint or burn Amulet in the CLI or Web UI | Amulet has no developer mint/burn surface | Use your own instrument (`token demo` or `token create`) |
| Token or ledger commands cannot find a JWT after a failed `up` | `up` captures credentials only when it finishes | Re-run `localnet up` to completion. `localnet creds demo --role app-provider --format raw` prints a captured JWT |
| Web UI / Explorer shows stale ports after a restart | Docker reassigned ephemeral host ports | Re-read them from `localnet status demo`. DevKit re-captures within ~15 s. Or run `localnet restart demo`. |

Still stuck: run `canton-devkit localnet logs demo`
(repeat `--service <svc>` to filter).
Then file a
[GitHub issue](https://github.com/bitdynamics-ab/canton-devkit/issues)
with the full `doctor` output and the failing command.

## Next

- [Installation & Getting Started](getting-started.md). DPM, checksums, Windows, `go install`.

- [LocalNet lifecycle](localnet-lifecycle.md). Multiple instances, `--port-base`, pause / stop / down.

- [Tokens](tokens.md). Create / mint / transfer / burn beyond the demo.

- [Explorer](explorer.md). Active Contract Set and transactions in the Web UI.

- [Troubleshooting](troubleshooting.md), [FAQ](faq.md), [Known limitations](limitations.md).
