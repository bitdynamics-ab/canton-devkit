#!/usr/bin/env bash
# Compute the deterministic tree SHA-256 over a directory's files.
# Used to populate Version.ContentSHA in internal/splice/versions.go
# for a new Splice catalogue entry.
#
# Algorithm mirrors internal/splice.computeTreeSHA: walk the tree, sort
# entries by forward-slash relative path, then SHA-256 over the
# concatenation of `<rel-path>\0<size>\0<content>` triples.
#
# Usage:
#   scripts/compute-tree-sha.sh ~/.canton-devkit/cache/splice-0.6.4
#
# Output: 64-char lowercase hex on stdout.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <dir>" >&2
  exit 2
fi
root="$1"
if [[ ! -d "$root" ]]; then
  echo "$root is not a directory" >&2
  exit 1
fi

(
  cd "$root"
  LC_ALL=C find . -type f | LC_ALL=C sort | while read -r rel; do
    rel="${rel#./}"
    size=$(stat -f%z "$rel" 2>/dev/null || stat -c%s "$rel")
    printf '%s\x00%s\x00' "$rel" "$size"
    cat "$rel"
  done
) | shasum -a 256 | awk '{print $1}'
