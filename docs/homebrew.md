# Homebrew install

`canton-devkit` ships a Homebrew formula for macOS (Apple Silicon) and
Linux (x86_64). The formula and downloadable build artifacts live in the
dedicated tap repository
[`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit),
following the standard Homebrew tap layout.

This source repository does not keep a `Formula/` directory. Homebrew
distribution files are maintained in `homebrew-canton-devkit`; this repository only
keeps the release helper script and docs that describe the process.

## Install (direct, no tap)

```sh
brew install --formula \
  https://raw.githubusercontent.com/bitdynamics-ab/homebrew-canton-devkit/main/Formula/canton-devkit.rb
```

> Note: the formula's `url` + `sha256` are rewritten automatically by
> the release workflow on every release tag (see below), so the direct
> formula always points at the latest published release. There is no
> `--HEAD` install path — the formula installs prebuilt release
> artifacts only.

## Install (via tap)

```sh
brew tap bitdynamics-ab/canton-devkit
brew install canton-devkit
```

## How the formula stays in sync

This is **automatic** on every release tag (`v*`). `.github/workflows/release.yml`:

1. Builds and publishes the per-platform tarballs and a single GNU
   `sha256sum` manifest named `SHA256SUMS` to a public GitHub Release in
   `bitdynamics-ab/homebrew-canton-devkit`.
2. Reads the `darwin_arm64` and `linux_amd64` digests out of
   `dist/SHA256SUMS`, rewrites the `version` + two `sha256` fields of the
   public builds repo's `Formula/canton-devkit.rb`, and commits the
   change back via the GitHub contents API (commit message
   `chore: bump Homebrew formula to <tag>`).

No maintainer action is required for a normal release.

### Manual / break-glass: `scripts/update-homebrew-formula.sh`

`scripts/update-homebrew-formula.sh v0.1.0 [path/to/homebrew-canton-devkit]`
does the same rewrite locally against a checked-out public builds repo.
Use it only when the automated step failed, or to re-pin an existing
tag. It downloads `SHA256SUMS` from the public release, extracts the two
digests, rewrites the formula in place, and prints a diff — it does
**not** commit or push, so review the diff and commit by hand. This
keeps a human in the loop when the release tarballs themselves might be
wrong.

## Smoke test

```sh
brew install --formula ../homebrew-canton-devkit/Formula/canton-devkit.rb
brew test  canton-devkit                          # invokes `localnet --help`
canton-devkit localnet --help
```

`brew test` is also exercised by the formula's `test do …` block, which
runs `canton-devkit localnet --help` and asserts the LocalNet command
tree is reachable.

## What's not supported (yet)

- **Windows** — Homebrew doesn't target Windows. Use the standalone
  artifact from the [public builds release page](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases).
- **Linux ARM** — not in the release matrix. Could be added in a
  follow-up if there's demand.
- **macOS Intel** — same; the project's compatibility matrix is
  Apple Silicon only.
