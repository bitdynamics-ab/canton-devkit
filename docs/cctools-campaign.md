# CCTools Campaign Build

This prerelease is for local development and campaign feedback, not production
or real funds. It includes a persistent per-install campaign code, screenshot
sharing, and a prefilled issue-report link.

## Install

Use the campaign prerelease, **v0.18.2-cctools.1**, from
[GitHub Releases](https://github.com/bitdynamics-ab/canton-devkit/releases/tag/v0.18.2-cctools.1).
The normal installer, Homebrew formula, and DPM `latest` continue to select the
regular release; they do not enable the campaign.

Prerequisites: Docker Desktop or Docker Engine with Compose, sufficient free
disk space (20 GB recommended), and memory for the selected Splice version.
Run `localnet doctor` before starting a network; it checks the version-specific
requirements. Supported packages: macOS Apple Silicon, Linux x86-64, and
Windows x86-64 with Docker Desktop running Linux containers.

On macOS or Linux, install the pinned campaign release:

```sh
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/v0.18.2-cctools.1/install.sh | VERSION=0.18.2-cctools.1 sh
export PATH="$HOME/.local/bin:$PATH"
canton-devkit version
```

Alternatively, download the platform archive and `SHA256SUMS`, verify its hash,
then extract it. On Windows, compare `Get-FileHash <archive> -Algorithm SHA256`
with `SHA256SUMS`, extract the ZIP, and run `.\canton-devkit.exe` in place of
`canton-devkit` in the commands below. No Windows installer is required.

`version` must show `v0.18.2-cctools.1`, a commit ID matching `BUILD-INFO.txt`,
and `campaign code: CCT-XXXXXXXX` (your actual code will differ).

## Try It

```sh
canton-devkit localnet doctor
canton-devkit localnet up cctools-demo
canton-devkit localnet status cctools-demo
canton-devkit localnet token demo --instance cctools-demo
canton-devkit localnet token balances --instance cctools-demo
canton-devkit localnet ui
```

Open the loopback URL printed by the last command. Select `cctools-demo`, inspect
the Overview, Tokens, and Explorer screens, and try Share. Review the captured
image before posting; do not share credentials, private data, or screenshots
from non-demo environments. Report issue opens GitHub with the running version.

The campaign code is stored in `~/.canton-devkit/campaign-id` and persists across
restarts. It is an identifier for this installation, not proof of a unique
person or a security credential. `DEVKIT_CAMPAIGN=0` disables campaign features
for that process. The Web UI remains loopback-only by default.

Stop the UI with Ctrl+C. Stop the demo while keeping its data:

```sh
canton-devkit localnet down cctools-demo
```

To permanently delete only this demo and its volumes:

```sh
canton-devkit localnet remove cctools-demo
```

## Build and Verify

From the campaign branch, using the Go and Node versions pinned by the repo:

```sh
make test
make lint
make frontend-test
make frontend
VERSION=v0.18.2-cctools.1 OUTPUT_DIR=bin/campaign/v0.18.2-cctools.1/dist COMPONENT_BIN_DIR=bin/campaign/v0.18.2-cctools.1/bin bash scripts/build-release.sh
```

Run `scripts/smoke-campaign.sh` through Bash with the native binary path,
release version, and short commit ID as arguments. This checks persisted
campaign identity, opt-out, the version API, and embedded JS/CSS. It does not
replace a Docker-backed lifecycle and token smoke test.

The release workflow runs Go tests, lint, frontend tests, and the package smoke
test before publishing. Campaign tags must use `vMAJOR.MINOR.PATCH-cctools.N`.
They enable campaign mode at link time, publish as prereleases, and cannot update
the regular GitHub latest release, Homebrew formula, or DPM `latest` tag.
