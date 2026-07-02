# Installation & Getting Started

Canton DevKit is a single Go binary that orchestrates the Splice
LocalNet Docker stack. It ships two ways:

1. **DPM component** (primary) — install through the Daml Package
   Manager and invoke as `dpm localnet …`.
2. **Standalone binary** (`canton-devkit`) — a self-contained
   executable for users who don't run DPM (CI, DevOps, workshop
   facilitators), shipped as release archives plus APT convenience
   packages for Debian/Ubuntu hosts. Invoke as `canton-devkit localnet …`.

Both paths ship the **same binary** and expose the **same command
tree**. Throughout the docs, `dpm localnet <cmd>` and
`canton-devkit localnet <cmd>` are interchangeable.

> **The only system prerequisite is a working Docker runtime.** DevKit
> never installs Docker, never edits the Docker daemon config, and
> never changes host permissions. It orchestrates the existing Splice
> LocalNet container stack.

---

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

---

## 2. Install — DPM component (primary)

DevKit is published as a native DPM component to an OCI registry. Add
it to your project's `daml.yaml` (or `multi-package.yaml`) `components`
list and install:

```yaml
# daml.yaml
sdk-version: <your-sdk-version>
name: my-app
version: 0.1.0
source: .
dependencies: []
components:
  - oci://ghcr.io/bitdynamics-ab/canton-devkit:<version>
```

```bash
dpm install package
dpm localnet --help          # confirms the component loaded
```

DPM registers a single top-level `localnet` command; every DevKit
subcommand (`up`, `down`, `status`, `dar …`, `contracts …`, `token …`,
`metrics`, `doctor`, …) lives under it. This keeps the DPM surface
minimal and conflict-free.

---

## 3. Install — standalone binary

Download the binary for your platform from the
[Releases page](https://github.com/bitdynamics-ab/canton-devkit/releases),
verify its checksum, mark it executable, and put it on your `PATH`.

Release assets are versioned archives named
`canton-devkit_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) — each
contains the `canton-devkit` binary plus `LICENSE` and `README.md`. Every
release also publishes a single `SHA256SUMS` file covering all archives;
the examples below verify against it.

### macOS (Apple Silicon)

```bash
VERSION=v0.7   # replace with the latest release tag
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

### Linux (amd64)

```bash
VERSION=v0.7
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

### APT — Debian / Ubuntu (amd64)

Tagged releases update a static APT repository hosted from the public
builds repo. Add it once, then install or upgrade with normal APT:

```bash
echo "deb [trusted=yes arch=amd64] https://raw.githubusercontent.com/bitdynamics-ab/homebrew-canton-devkit/main/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/canton-devkit.list
sudo apt update
sudo apt install canton-devkit
canton-devkit version
```

List available versions:

```bash
apt list -a canton-devkit
apt policy canton-devkit
```

Install a specific version:

```bash
sudo apt install canton-devkit=0.7.0
```

The APT repo is currently unsigned and therefore uses `trusted=yes`;
the release still publishes SHA-256 metadata. A signed repository key
is planned. Package installation records a best-effort anonymous `apt`
install-surface telemetry ping — see [telemetry.md](./telemetry.md)
for what is sent and how to opt out before installing.

Direct `.deb` install also works:

```bash
VERSION=v0.7
DEB_VERSION="${VERSION#v}"
ASSET="canton-devkit_${DEB_VERSION}_amd64.deb"
base="https://github.com/bitdynamics-ab/canton-devkit/releases/download/${VERSION}"
curl -fLO "${base}/${ASSET}"
curl -fLO "${base}/SHA256SUMS"
grep " ${ASSET}\$" SHA256SUMS | sha256sum -c - || { echo "checksum mismatch"; exit 1; }
sudo apt install "./${ASSET}"
canton-devkit version
```

The Debian package installs `/usr/bin/canton-devkit`. It does not install
Docker; run `canton-devkit localnet doctor` after installation to verify
Docker CLI, Compose v2, ports, disk, memory, and host prerequisites.

### Windows (amd64, PowerShell)

```powershell
$Version = "v0.7"
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

### Homebrew (macOS arm64 / Linux amd64)

```bash
brew tap bitdynamics-ab/canton-devkit
brew install canton-devkit
```

> See [homebrew.md](./homebrew.md) for the direct-formula install,
> the tap layout, and how the formula is kept in sync on each release.

### From source (Go toolchain)

```bash
go install github.com/bitdynamics-ab/canton-devkit/cmd/canton-devkit@latest
```

---

## 4. Zero to running LocalNet

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

### Running two LocalNets at once

```bash
canton-devkit localnet up --name alpha
canton-devkit localnet up --name beta
canton-devkit localnet list           # both instances + their state
```

Each named instance gets its own deterministic compose project,
network, and host ports, so they don't collide.

#### Explicit, deterministic ports (`--port-base`)

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

---

## 5. Compatibility matrix

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

DevKit pins a **curated** set of Splice versions in
[`internal/splice/versions.json`](../internal/splice/versions.json);
`localnet up --version <tag>` selects one. List them at runtime:

```bash
canton-devkit localnet versions
```

See [docs/versions.md](./versions.md) for how the catalogue is fetched
and verified. Uncurated upstream tags can be used at your own risk via
`up --version <tag> --allow-uncurated`.

---

## 6. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `doctor` says **Docker daemon** ✗ | Docker not running | Start Docker Desktop / `sudo systemctl start docker` |
| `doctor` says **Compose v2** ✗ | Only Compose v1 present | Upgrade to Docker Compose v2 (`docker compose`, not `docker-compose`) |
| `up` fails **PORTS_IN_USE** | Another process holds a port | Stop the conflicting process, or use a different `--name` |
| `up` hangs at "waiting for healthy" | Insufficient Docker memory | Raise Docker memory to ≥ 8 GB; see [docs/limitations.md](./limitations.md) |
| Linux: `permission denied` on the Docker socket | User not in `docker` group | `sudo usermod -aG docker $USER` then re-login |
| macOS: "cannot be opened because the developer cannot be verified" | Gatekeeper quarantine | `xattr -d com.apple.quarantine $(which canton-devkit)` |
| Web UI / Explorer shows stale ports after a restart | Docker re-assigned ephemeral ports | DevKit re-captures them within ~15 s; or run `localnet restart --name <n>` |

For anything else, attach the full `localnet doctor` output to a
[GitHub issue](https://github.com/bitdynamics-ab/canton-devkit/issues) —
it includes OS/arch, Docker/Compose versions, and the check results.

---

## 7. Uninstall / clean up

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
