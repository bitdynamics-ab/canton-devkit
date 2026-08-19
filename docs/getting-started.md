# Installation & Getting Started

Canton DevKit is a single Go binary that orchestrates the Splice
LocalNet Docker stack. It ships two ways:

1. **DPM component** (primary) — install through the Daml Package
   Manager and invoke as `dpm localnet <command>`.
2. **Standalone binary** (`canton-devkit`) — a self-contained
   executable for users who don't run DPM (CI, DevOps, workshop
   facilitators), shipped as release archives. Invoke as
   `canton-devkit localnet <command>`.

Both paths ship the **same binary** and expose the **same command
tree**. Throughout the docs, `dpm localnet <cmd>` and
`canton-devkit localnet <cmd>` are interchangeable.

> **The only system prerequisite is a working Docker runtime.** DevKit
> never installs Docker, never edits the Docker daemon config, and
> never changes host permissions. It orchestrates the existing Splice
> LocalNet container stack.

## 1. Prerequisites

| Requirement | Why | Check |
|---|---|---|
| Docker Engine / Desktop | DevKit runs LocalNet as containers | `docker version` |
| Docker Compose **v2** | LocalNet is a compose project | `docker compose version` |
| ~8 GB free RAM for Docker | Splice stack is memory-hungry | Docker Desktop → Settings → Resources |
| ~20 GB free disk | Splice images + volumes | `df -h` |

Run the built-in host check at any time — it never modifies anything:

```bash
dpm localnet doctor          # or: canton-devkit localnet doctor
```

`doctor` exits `0` when the host is ready (warnings allowed) and `2`
when a check fails, printing copy-pasteable remediation. It's the same
preflight `localnet up` runs, so a green `doctor` means `up` will pass
preflight.

## 2. Install — DPM component (primary)

DevKit is published as a native DPM component to an OCI registry. Remove
the `sdk-version` field from your project's `daml.yaml` (or
`multi-package.yaml`) and declare the SDK packages plus the DevKit
component under `components`, then install:

```yaml
# daml.yaml
#sdk-version: <your-sdk-version>
name: my-app
version: 0.1.0
source: .
dependencies: []
components:
  - canton-open-source:<your-sdk-version>
  - codegen:<your-sdk-version>
  - damlc:<your-sdk-version>
  - daml-new:<your-sdk-version>
  - daml-script:<your-sdk-version>
  - upgrade-check:<your-sdk-version>
  - scribe:<your-sdk-version>
  - daml-shell:<your-sdk-version>
  - oci://ghcr.io/bitdynamics-ab/canton-devkit:<version>
```

```bash
dpm install package
dpm localnet --help          # confirms the component loaded
```

Replace `<your-sdk-version>` with the Canton/Daml release you are
targeting, and `<version>` with a DevKit release tag (semver, no `v`
prefix) or `latest`.

DPM registers a single top-level `localnet` command; every DevKit
subcommand (`up`, `down`, `status`, `dar`, `contracts`, `tx`, `token`,
`metrics`, `doctor`, and the rest) lives under it. This keeps the DPM
surface minimal and conflict-free.

## 3. Install — standalone binary

Standalone builds are published on this repository's
[GitHub Releases](https://github.com/bitdynamics-ab/canton-devkit/releases).
Release archives are named
`canton-devkit_v<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) — each
contains the `canton-devkit` binary plus `LICENSE` and `README.md`. Every
release also publishes a single `SHA256SUMS` file covering all archives.

### Quick install (macOS arm64 / Linux amd64)
```bash
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
```

Or with `wget`:

```bash
wget -qO- https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
```

Options (pass as environment variables):

```bash
# Pin a specific version
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | VERSION=0.12.2 sh

# Custom install directory
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

The installer detects your platform, downloads the matching archive from
the [releases page](https://github.com/bitdynamics-ab/canton-devkit/releases),
verifies the SHA-256 checksum, and installs to `~/.local/bin` by default.
It warns when that directory is not on your `PATH`.

Supported platforms:

- macOS Apple Silicon (`darwin/arm64`)
- Linux x86_64 (`linux/amd64`)

### Homebrew (macOS arm64 / Linux amd64)

```bash
brew tap bitdynamics-ab/canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
```

To upgrade after a new release is published:

```bash
brew update
brew upgrade canton-devkit
```

The formula downloads platform-specific release tarballs from this
repository's
[releases page](https://github.com/bitdynamics-ab/canton-devkit/releases);
the tap only hosts the Homebrew formula.

> See the [Homebrew guide](homebrew.md) for the tap layout and how the
> formula is kept in sync on each release.

> **Note:** the hosted APT repository and `.deb` packages are no longer
> maintained. If you previously added
> `/etc/apt/sources.list.d/canton-devkit.list`, remove that file and use
> the quick-install script, a release tarball, or Homebrew instead.

### Manual download — macOS (Apple Silicon)

Download the binary for your platform from the
[releases page](https://github.com/bitdynamics-ab/canton-devkit/releases),
verify its checksum, mark it executable, and put it on your `PATH`:

```bash
VERSION=v0.12.2   # replace with the latest release tag
ASSET="canton-devkit_${VERSION}_darwin_arm64.tar.gz"
base="https://github.com/bitdynamics-ab/canton-devkit/releases/download/${VERSION}"
curl -fLO "${base}/${ASSET}"
curl -fLO "${base}/SHA256SUMS"
# verify against the release checksums (recommended)
grep " ${ASSET}\$" SHA256SUMS | shasum -a 256 -c - || { echo "checksum mismatch"; exit 1; }
tar -xzf "${ASSET}"             # → canton-devkit, LICENSE, README.md
chmod +x canton-devkit
sudo mv canton-devkit /usr/local/bin/
# Gatekeeper: first run may need this once
xattr -d com.apple.quarantine /usr/local/bin/canton-devkit 2>/dev/null || true
canton-devkit version
```

### Manual download — Linux (amd64)

```bash
VERSION=v0.12.2   # replace with the latest release tag
ASSET="canton-devkit_${VERSION}_linux_amd64.tar.gz"
base="https://github.com/bitdynamics-ab/canton-devkit/releases/download/${VERSION}"
curl -fLO "${base}/${ASSET}"
curl -fLO "${base}/SHA256SUMS"
grep " ${ASSET}\$" SHA256SUMS | sha256sum -c - || { echo "checksum mismatch"; exit 1; }
tar -xzf "${ASSET}"             # → canton-devkit, LICENSE, README.md
chmod +x canton-devkit
sudo mv canton-devkit /usr/local/bin/
canton-devkit version
```

### Windows (amd64, PowerShell)

```powershell
$Version = "v0.12.2"   # replace with the latest release tag
$Asset = "canton-devkit_${Version}_windows_amd64.zip"
$base = "https://github.com/bitdynamics-ab/canton-devkit/releases/download/$Version"
Invoke-WebRequest -Uri "$base/$Asset" -OutFile $Asset
Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile SHA256SUMS
# verify against the release checksums
$expected = ((Get-Content SHA256SUMS | Select-String -SimpleMatch $Asset) -split '\s+')[0]
$actual = (Get-FileHash $Asset -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" }
Expand-Archive -Path $Asset -DestinationPath canton-devkit-dist -Force
# put it somewhere on PATH, e.g. a tools dir you've added to PATH
Move-Item canton-devkit-dist\canton-devkit.exe "$env:USERPROFILE\bin\canton-devkit.exe"
canton-devkit version
```

### From source (Go toolchain)

```bash
go install github.com/bitdynamics-ab/canton-devkit/cmd/canton-devkit@latest
```

## 4. Compatibility matrix

### Platforms (released, tested)

| OS | Arch | Status |
|---|---|---|
| macOS | arm64 (Apple Silicon) | ✅ Supported |
| Linux | amd64 | ✅ Supported |
| Windows | amd64 | ✅ Supported |

Other OS/arch combinations may work (DevKit only orchestrates Docker)
but are untested — `localnet doctor` prints a warning on unsupported
platforms.

### Splice LocalNet versions

DevKit pins a catalogue of tested Splice versions; `localnet up
--version <tag>` selects one. List them at runtime:

```bash
canton-devkit localnet versions
```

See the [Splice version catalogue](versions.md) for how the
catalogue is fetched and verified. Uncurated upstream tags can be used
at your own risk via `up --version <tag> --allow-uncurated`.

## 5. Troubleshooting the install

| Symptom | Cause | Fix |
|---|---|---|
| `doctor` says **Docker daemon** ✗ | Docker not running | Start Docker Desktop / `sudo systemctl start docker` |
| `doctor` says **Compose v2** ✗ | Only Compose v1 present | Upgrade to Docker Compose v2 (`docker compose`, not `docker-compose`) |
| `up` fails **PORTS_IN_USE** | Another process holds a port | Stop the conflicting process, or use a different `--name` |
| `up` hangs at "waiting for healthy" | Insufficient Docker memory | Raise Docker memory to ≥ 8 GB; see [Known limitations](limitations.md) |
| Linux: `permission denied` on the Docker socket | User not in `docker` group | `sudo usermod -aG docker $USER` then re-login |
| macOS: "cannot be opened because the developer cannot be verified" | Gatekeeper quarantine | `xattr -d com.apple.quarantine $(which canton-devkit)` |
| Web UI / Explorer shows stale ports after a restart | Docker re-assigned ephemeral ports | DevKit re-captures them within ~15 s; or run `localnet restart --name <n>` |

For anything else, attach the full `localnet doctor` output to a
[GitHub issue](https://github.com/bitdynamics-ab/canton-devkit/issues) —
it includes OS/arch, Docker/Compose versions, and the check results.

## 6. Next steps

- [LocalNet lifecycle](localnet-lifecycle.md) — zero to a running
  LocalNet, multiple instances, deterministic ports, and clean-up.
- [Tokens](tokens.md) — CIP-0112 token flows on LocalNet.
- [Explorer](explorer.md) — browse the Active Contract Set and
  recent transactions from the Web UI.
