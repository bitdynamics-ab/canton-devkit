# Packaging & Distribution

`canton-devkit` ships through two complementary channels:

1. **DPM component** (primary) — installed via `dpm install package`.
2. **Standalone Go binary** (additional) — direct download / install.

Both are produced by the same release workflow
([`.github/workflows/release.yml`](../.github/workflows/release.yml))
from the same Go source. The DPM component just wraps the standalone
binary in an OCI artifact with a `component.yaml` manifest.

## Standalone binary

Tagged releases (`v*`) publish per-platform tarballs to GitHub Releases:

| File | Platform |
|---|---|
| `canton-devkit_<version>_linux_amd64.tar.gz` | Linux x86_64 |
| `canton-devkit_<version>_darwin_arm64.tar.gz` | macOS Apple Silicon |
| `canton-devkit_<version>_windows_amd64.zip` | Windows x86_64 |
| `SHA256SUMS` | GNU `sha256sum --check`-compatible manifest |

Each archive contains the binary, `LICENSE`, and `README.md`. Verify
before unpacking:

```sh
sha256sum --check SHA256SUMS
tar -xzf canton-devkit_v0.1.0_linux_amd64.tar.gz
./canton-devkit localnet --help
```

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
parser. See [`packaging/component.yaml.tmpl`](../packaging/component.yaml.tmpl).

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
with DPM builtins and every other component. We deliberately register
only `localnet` to:

- Avoid collisions with DPM builtins (`install`, `publish`, `versions`,
  `bootstrap`, …) or with future first-party components.
- Keep the DPM surface minimal — `dpm localnet up`, `dpm localnet dar
  upload`, `dpm localnet contracts ls`, etc. nest naturally.

All DevKit subcommands live inside our binary's own Cobra tree, not in
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
