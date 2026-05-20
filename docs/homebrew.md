# Homebrew install

`canton-devkit` ships a Homebrew formula at
[`Formula/canton-devkit.rb`](../Formula/canton-devkit.rb) for macOS
(Apple Silicon) and Linux (x86_64). Same binary as the standalone
release; Homebrew just wraps the download and verification.

## Install (direct, no tap)

Use this until the public tap exists:

```sh
brew install --formula \
  https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/Formula/canton-devkit.rb
```

> Note: the formula's stable `url` + `sha256` start as placeholders
> (`version "0.0.0"`, all-zero SHA) until the first release tag is cut
> and `scripts/update-homebrew-formula.sh` rewrites them. Until then,
> use the `--HEAD` path below.

## Install (build from source via `--HEAD`)

```sh
brew install --HEAD --formula \
  https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/Formula/canton-devkit.rb
```

The formula's `head do … end` block uses git + a build-time `go`
dependency to compile against the current `main` branch. This works on
day-1 before any release tag exists.

## Install (via tap, future)

Once `bitdynamics-ab/homebrew-canton-devkit` is published, the canonical
flow becomes:

```sh
brew tap bitdynamics-ab/canton-devkit
brew install canton-devkit
```

The tap repo is one-shot maintenance — when it exists, the formula in
this repo is mirrored there on every release.

## How the formula stays in sync

On every release tag (`v*`):

1. `.github/workflows/release.yml` builds and publishes the per-platform
   tarballs and a `SHA256SUMS` manifest to a GitHub Release.
2. A maintainer runs:

   ```sh
   scripts/update-homebrew-formula.sh v0.1.0
   ```

   The script downloads `SHA256SUMS` from the release, extracts the
   `darwin_arm64` and `linux_amd64` digests, and rewrites the `version`
   + two `sha256` fields in `Formula/canton-devkit.rb`.

3. The diff is committed (`chore: bump Homebrew formula to v0.1.0`)
   and merged.

The script does NOT commit or push automatically — `git diff Formula/`
first, then commit. This keeps the human in the loop in case the
release tarballs themselves are wrong.

## Smoke test

```sh
brew install --formula Formula/canton-devkit.rb   # from a clone
brew test  canton-devkit                          # invokes `localnet --help`
canton-devkit localnet --help
```

`brew test` is also exercised by the formula's `test do …` block, which
runs `canton-devkit localnet --help` and asserts the LocalNet command
tree is reachable.

## What's not supported (yet)

- **Windows** — Homebrew doesn't target Windows. Use the standalone zip
  from [the release page](https://github.com/bitdynamics-ab/canton-devkit/releases).
- **Linux ARM** — not in the release matrix. Could be added in BIT-19's
  follow-up if there's demand.
- **macOS Intel** — same; the project's compatibility matrix is
  Apple Silicon only.
