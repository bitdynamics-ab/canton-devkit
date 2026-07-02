---
title: "Packaging & Distribution"
description: "How canton-devkit ships — standalone binaries, the DPM component, the Debian/APT package — and the current supply-chain integrity story."
---

`canton-devkit` ships through complementary channels:

1. **DPM component** (primary) — installed via `dpm install package`.
2. **Standalone Go binary** (additional) — direct download / install.
3. **Package-manager convenience** — a hosted APT repo wraps the same
   standalone Linux binary for Debian/Ubuntu workflows.

All release artifacts are produced by the same release workflow
([`.github/workflows/release.yml`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/.github/workflows/release.yml))
from the same Go source. The DPM component and Debian package both wrap
the standalone binary for their respective ecosystems.

## Standalone binary

Tagged releases (`v*`) publish per-platform standalone artifacts to
GitHub Releases:

| File | Platform |
|---|---|
| `canton-devkit_<version>_linux_amd64.tar.gz` | Linux x86_64 |
| `canton-devkit_<version>_darwin_arm64.tar.gz` | macOS Apple Silicon |
| `canton-devkit_<version>_windows_amd64.zip` | Windows x86_64 |
| `canton-devkit_<version-without-v>_amd64.deb` | Debian/Ubuntu x86_64 |
| `SHA256SUMS` | GNU `sha256sum --check`-compatible manifest |

Each tarball/zip contains the binary, `LICENSE`, and `README.md`; the
Debian package installs the same binary plus docs under standard Linux
paths. Verify before unpacking/installing:

```sh
sha256sum --check SHA256SUMS
tar -xzf canton-devkit_v0.7.0_linux_amd64.tar.gz
./canton-devkit localnet --help
```

> **Version-string asymmetry:** the standalone archive filenames keep the
> `v` prefix (`canton-devkit_v0.7.0_…`), matching the git tag, while the
> DPM/OCI tag strips it (`…:0.7.0`) because DPM requires a bare-semver
> tag. Same release, two conventions — chosen to match each ecosystem's
> norm.

## DPM component

The DPM component is published to GitHub Container Registry on every
tagged release at `ghcr.io/bitdynamics-ab/canton-devkit:<version>`.
Install via:

```sh
dpm install package oci://ghcr.io/bitdynamics-ab/canton-devkit:<version>
dpm localnet --help
```

`<version>` follows semver (no `v` prefix); tag `latest` always points
at the newest published release.

### Manifest

The component registers a single top-level command `localnet` that
delegates the rest of the DevKit CLI surface to the binary's own argv
parser. See [`packaging/component.yaml.tmpl`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/packaging/component.yaml.tmpl).

DPM does NOT pass the registered command name into the binary's argv —
only `exec-args` + user args reach it. `exec-args: ["localnet"]` is
therefore required so the binary always dispatches into its `localnet`
subtree regardless of how DPM invoked the component. A contract test
(`TestRunIsArgvOnly`) locks this invariant.

The manifest lives as a template with a `@@BINARY_PATH@@` token: the
release workflow substitutes `bin/canton-devkit` on Unix platforms and
`bin/canton-devkit.exe` on Windows. DPM does NOT auto-append `.exe`
on Windows — empirically verified against DPM 1.0.16, which fails
manifest validation with `stat ...: no such file or directory` when
the path doesn't include the extension.

### Why a single top-level command?

DPM components register top-level commands into a flat namespace shared
with DPM builtins and every other component. DevKit deliberately
registers only `localnet` to:

- Avoid collisions with DPM builtins (`install`, `publish`, `versions`,
  `bootstrap`, …) or with future first-party components.
- Keep the DPM surface minimal — `dpm localnet up`, `dpm localnet dar
  upload`, `dpm localnet contracts ls`, etc. nest naturally.

All DevKit subcommands live inside the binary's own Cobra tree, not in
the DPM manifest.

## Local validation

Before pushing a release-affecting change, validate the manifest
end-to-end against a real DPM CLI:

```sh
# 1. Build a host-platform binary into the expected layout.
mkdir -p /tmp/cdk-component/bin
go build -o /tmp/cdk-component/bin/canton-devkit ./cmd/canton-devkit

# 2. Render the manifest from the template for this platform.
sed "s|@@BINARY_PATH@@|bin/canton-devkit|" \
    packaging/component.yaml.tmpl > /tmp/cdk-component/component.yaml
cp LICENSE /tmp/cdk-component/LICENSE

# 3. Run dpm publish --dry-run; it validates the manifest schema and
#    reports the OCI layout that would be pushed.
dpm publish component oci://localhost:5000/canton-devkit:0.0.1-dryrun \
    --dry-run \
    --platform darwin/arm64=/tmp/cdk-component
```

`✅ Component manifest is valid` confirms the manifest schema. CI runs
the same `--dry-run` on every push and the real publish only on `v*`
tags.

## Debian / APT package

The release workflow builds a Debian package from the exact same
`linux/amd64` binary used in the standalone tarball and DPM component.
It publishes the `.deb` as a release asset and updates a static APT repo
in the public builds repository:

```sh
echo "deb [trusted=yes arch=amd64] https://raw.githubusercontent.com/bitdynamics-ab/homebrew-canton-devkit/main/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/canton-devkit.list
sudo apt update
sudo apt install canton-devkit
```

Available versions can be inspected with:

```sh
apt list -a canton-devkit
apt policy canton-devkit
```

Direct artifact install remains available:

```sh
sudo apt install ./canton-devkit_0.7.0_amd64.deb
canton-devkit version
```

The package installs:

```text
/usr/bin/canton-devkit
/usr/share/doc/canton-devkit/LICENSE
/usr/share/doc/canton-devkit/README.md
```

It deliberately does **not** depend on or install Docker. DevKit's
runtime preflight remains `canton-devkit localnet doctor`, which checks
Docker CLI availability, daemon connectivity, Compose v2, ports, disk,
memory, and host-specific prerequisites.

The Debian `postinst` script calls
`canton-devkit telemetry _record-install-surface apt` as a best-effort
hook. The binary owns opt-out precedence, local spooling, and uploader
timeouts, so package installation never fails if telemetry is disabled or
the collector is unreachable.

The hosted repo is generated on every release by preserving all existing
`apt/pool/main/c/canton-devkit/*.deb` files in
`bitdynamics-ab/homebrew-canton-devkit`, adding the new version, and
rewriting `Packages`, `Packages.gz`, and `Release` metadata under
`apt/dists/stable/main/binary-amd64/`.

**Known limitation:** the APT repo is unsigned and documented with
`trusted=yes`. The repository is backed by HTTPS and release checksums,
but a GPG-signed `InRelease` file and install instructions using
`signed-by=` are planned hardening steps.

## Supply-chain integrity

Today's integrity story is **SHA-256 checksums** (`SHA256SUMS`, verifiable
with `sha256sum --check`) plus the immutability of the GHCR OCI digest.
The CI pipeline also pins every GitHub Action and the DPM CLI tarball by
SHA.

**Known limitation:** the release artifacts are **not yet
cryptographically signed**. There are no [cosign](https://github.com/sigstore/cosign)/Sigstore
signatures on `SHA256SUMS` or on the OCI artifact, so consumers can
verify *integrity* (the bytes match the checksum) but not *provenance*
(the bytes were produced by the project's release pipeline). Keyless
cosign signing plus a published verification step is a planned
hardening item.
