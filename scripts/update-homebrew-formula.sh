#!/usr/bin/env bash
# Update Formula/canton-devkit.rb to point at the tarballs of a published
# release. Run after the release workflow has uploaded public artifacts to
# bitdynamics-ab/canton-devkit-builds.
#
# Usage:
#   scripts/update-homebrew-formula.sh vX.Y.Z
#
# What it does:
#   1. Validates the tag exists on public GitHub Releases.
#   2. Downloads the SHA256SUMS file from the release and extracts the
#      checksums for the darwin_arm64 and linux_amd64 tarballs.
#   3. Rewrites version + the two sha256 fields in Formula/canton-devkit.rb.
#   4. Prints a diff and the proposed commit message.
#
# Does NOT commit or push — that's a deliberate maintainer step. Run
# `git diff Formula/canton-devkit.rb` first, then commit.

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <tag>  (e.g. v0.1.0)" >&2
  exit 1
fi

tag="$1"
version="${tag#v}"
repo="bitdynamics-ab/canton-devkit-builds"
formula="Formula/canton-devkit.rb"

if [[ ! -f "$formula" ]]; then
  echo "error: $formula not found (run from repo root)" >&2
  exit 1
fi

# Pull SHA256SUMS from the release. `gh release view --json` doesn't
# include asset contents, so download via the asset URL directly.
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Fetching SHA256SUMS from $repo $tag..."
gh release download "$tag" -R "$repo" -p SHA256SUMS -D "$tmpdir"

sha_darwin=$(grep "_${tag}_darwin_arm64.tar.gz$" "$tmpdir/SHA256SUMS" | awk '{print $1}')
sha_linux=$(grep "_${tag}_linux_amd64.tar.gz$"  "$tmpdir/SHA256SUMS" | awk '{print $1}')

if [[ -z "$sha_darwin" || -z "$sha_linux" ]]; then
  echo "error: could not locate darwin/arm64 or linux/amd64 entry in SHA256SUMS" >&2
  echo "SHA256SUMS contents:" >&2
  cat "$tmpdir/SHA256SUMS" >&2
  exit 1
fi

echo "  darwin_arm64: $sha_darwin"
echo "  linux_amd64:  $sha_linux"

# The formula has two sha256 lines, in order: darwin then linux. Use a
# tiny awk pass that toggles between them.
awk -v ver="$version" -v sd="$sha_darwin" -v sl="$sha_linux" '
  BEGIN { seen_sha = 0 }
  /^  version / { sub(/"[^"]*"/, "\"" ver "\""); print; next }
  /^      sha256 / {
    seen_sha++
    if (seen_sha == 1)      sub(/"[^"]*"/, "\"" sd "\"")
    else if (seen_sha == 2) sub(/"[^"]*"/, "\"" sl "\"")
    print; next
  }
  { print }
' "$formula" > "$tmpdir/formula.new"
mv "$tmpdir/formula.new" "$formula"

echo
echo "--- $formula updated; diff: ---"
git --no-pager diff "$formula" || true
echo
echo "Suggested commit:"
echo "  git add $formula"
echo "  git commit -m 'chore: bump Homebrew formula to $tag'"
