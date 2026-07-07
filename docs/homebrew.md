# Homebrew install

`canton-devkit` ships a Homebrew formula for macOS (Apple Silicon) and
Linux (x86_64). The formula and downloadable build artifacts live in the
dedicated tap repository
[`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit),
following the standard Homebrew tap layout.

This source repository does not keep a `Formula/` directory. Homebrew
distribution files are maintained in `homebrew-canton-devkit`; this repository only
keeps the release helper script and docs that describe the process.

## Install

Homebrew requires formulae to live in a tap (installing a formula from
a URL or a bare file path is no longer supported), so install via the
tap:

```sh
brew tap bitdynamics-ab/canton-devkit
brew install canton-devkit
```

> Note: the formula's `url` + `sha256` are rewritten automatically by
> the release workflow on every release tag (see below), so the tap
> always installs the latest published release. There is no `--HEAD`
> install path — the formula installs prebuilt release artifacts only.

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

Current Homebrew rejects bare-path formula installs ("Homebrew requires
formulae to be in a tap"), so test a not-yet-pushed formula bump by
tapping the local clone. `brew tap` clones the git repo, so commit the
bump locally in `../homebrew-canton-devkit` first, then:

```sh
brew tap bitdynamics-ab/canton-devkit ../homebrew-canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
brew test canton-devkit
canton-devkit localnet --help
```

After pushing, the plain `brew tap bitdynamics-ab/canton-devkit &&
brew install canton-devkit` form verifies the published tap.

`brew test canton-devkit` runs the formula's `test do` block, which
executes `canton-devkit localnet --help` and asserts the LocalNet
command tree is reachable.

## What's not supported (yet)

- **Windows** — Homebrew doesn't target Windows. Use the standalone
  artifact from the [public builds release page](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases).
- **Linux ARM** — not in the release matrix. Could be added in a
  follow-up if there's demand.
- **macOS Intel** — same; the project's compatibility matrix is
  Apple Silicon only.
