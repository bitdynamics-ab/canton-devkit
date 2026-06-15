#!/usr/bin/env bash
# Update the public builds repo Homebrew formula to point at the tarballs of a
# published release. Run after the release workflow has uploaded public
# artifacts to bitdynamics-ab/homebrew-canton-devkit.
#
# Usage:
#   scripts/update-homebrew-formula.sh vX.Y.Z [path/to/homebrew-canton-devkit]
#
# If the builds repo path is omitted, the script uses ../homebrew-canton-devkit.
#
# What it does:
#   1. Validates the tag exists on public GitHub Releases.
#   2. Downloads the SHA256SUMS file from the release and extracts the
#      checksums for the darwin_arm64 and linux_amd64 tarballs.
#   3. Rewrites version + the two sha256 fields in the public builds repo's
#      Formula/canton-devkit.rb.
#   4. Prints a diff and the proposed commit message.
#
# NOTE: release.yml now bumps the public formula automatically on every
# tag (via the GitHub contents API), so this script is a break-glass /
# manual-recovery path — use it when the automated step failed or to
# re-pin an existing tag. It does NOT commit or push: review the public
# builds repo diff first, then commit there.

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <tag>  (e.g. v0.1.0)" >&2
  exit 1
fi

tag="$1"
version="${tag#v}"
repo="bitdynamics-ab/homebrew-canton-devkit"
builds_repo_path="${2:-../homebrew-canton-devkit}"
formula="$builds_repo_path/Formula/canton-devkit.rb"

if [[ ! -f "$formula" ]]; then
  echo "error: $formula not found" >&2
  echo "usage: $0 <tag> [path/to/homebrew-canton-devkit]" >&2
  exit 1
fi

# Pull SHA256SUMS from the release. `gh release view --json` doesn't
# include asset contents, so download via the asset URL directly. The
# release workflow publishes a single GNU sha256sum manifest named
# SHA256SUMS (release.yml: `sha256sum * > SHA256SUMS`); it used to be
# called checksums.txt.
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
git -C "$builds_repo_path" --no-pager diff -- Formula/canton-devkit.rb || true
echo
echo "Suggested commit:"
echo "  cd $builds_repo_path"
echo "  git add Formula/canton-devkit.rb"
echo "  git commit -m 'chore: bump Homebrew formula to $tag'"
