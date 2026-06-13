# Homebrew install

`canton-devkit` ships a Homebrew formula for macOS (Apple Silicon) and
Linux (x86_64). The formula and downloadable build artifacts live in the public
[`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit)
repository so users can download release artifacts without access to the
private source repository.

This private source repository does not keep a `Formula/` directory. Homebrew
distribution files are maintained in `homebrew-canton-devkit`; this repository only
keeps the release helper script and docs that describe the process.

## Install (direct, no tap)

After a public release is published and the formula is updated with real
checksums:

```sh
brew install --formula \
  https://raw.githubusercontent.com/bitdynamics-ab/homebrew-canton-devkit/main/Formula/canton-devkit.rb
```

> Note: the formula's stable `url` + `sha256` start as placeholders
> (`version "0.0.0"`, all-zero SHA) until the first release tag is cut
> and `scripts/update-homebrew-formula.sh` rewrites them. There is no
> public `--HEAD` install path because the source repository is private.

## Install (via tap)

```sh
brew tap bitdynamics-ab/canton-devkit
brew install canton-devkit
```

## How the formula stays in sync

On every release tag (`v*`):

1. `.github/workflows/release.yml` builds and publishes the per-platform
   tarballs and a `checksums.txt` manifest to a public GitHub Release in
   `bitdynamics-ab/homebrew-canton-devkit`.
2. A maintainer runs:

   ```sh
   scripts/update-homebrew-formula.sh v0.1.0
   ```

   The script downloads `checksums.txt` from the public builds release,
   extracts the `darwin_arm64` and `linux_amd64` digests, and rewrites
   the `version` + two `sha256` fields in the checked-out public builds
   repo's `Formula/canton-devkit.rb`.

3. The diff is committed in `bitdynamics-ab/homebrew-canton-devkit` with a
   message such as `chore: bump Homebrew formula to v0.1.0`.

The script does NOT commit or push automatically. Review the diff in the public
builds repo first, then commit. This keeps the human in the loop in case the
release tarballs themselves are wrong.

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
