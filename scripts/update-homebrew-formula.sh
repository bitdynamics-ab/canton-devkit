#!/usr/bin/env bash
# Update the public builds repo Homebrew formula to point at the tarballs of a
# published release. Run after the release workflow has uploaded public
# artifacts to bitdynamics-ab/canton-devkit-builds.
#
# Usage:
#   scripts/update-homebrew-formula.sh vX.Y.Z [path/to/canton-devkit-builds]
#
# If the builds repo path is omitted, the script uses ../canton-devkit-builds.
#
# What it does:
#   1. Validates the tag exists on public GitHub Releases.
#   2. Downloads the checksums.txt file from the release and extracts the
#      checksums for the darwin_arm64 and linux_amd64 tarballs.
#   3. Rewrites version + the two sha256 fields in the public builds repo's
#      Formula/canton-devkit.rb.
#   4. Prints a diff and the proposed commit message.
#
# Does NOT commit or push — that's a deliberate maintainer step. Review the
# public builds repo diff first, then commit there.

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <tag>  (e.g. v0.1.0)" >&2
  exit 1
fi

tag="$1"
version="${tag#v}"
repo="bitdynamics-ab/canton-devkit-builds"
builds_repo_path="${2:-../canton-devkit-builds}"
formula="$builds_repo_path/Formula/canton-devkit.rb"

if [[ ! -f "$formula" ]]; then
  echo "error: $formula not found" >&2
  echo "usage: $0 <tag> [path/to/canton-devkit-builds]" >&2
  exit 1
fi

# Pull checksums.txt from the release. `gh release view --json` doesn't
# include asset contents, so download via the asset URL directly.
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Fetching checksums.txt from $repo $tag..."
gh release download "$tag" -R "$repo" -p checksums.txt -D "$tmpdir"

sha_darwin=$(grep "_${tag}_darwin_arm64.tar.gz$" "$tmpdir/checksums.txt" | awk '{print $1}')
sha_linux=$(grep "_${tag}_linux_amd64.tar.gz$"  "$tmpdir/checksums.txt" | awk '{print $1}')

if [[ -z "$sha_darwin" || -z "$sha_linux" ]]; then
  echo "error: could not locate darwin/arm64 or linux/amd64 entry in checksums.txt" >&2
  echo "checksums.txt contents:" >&2
  cat "$tmpdir/checksums.txt" >&2
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
