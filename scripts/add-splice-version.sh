#!/usr/bin/env bash
# Add a new Splice version to internal/splice/versions.json.
#
# Usage:
#   scripts/add-splice-version.sh <tag>
#
# Example:
#   scripts/add-splice-version.sh 0.6.5
#
# What it does:
#   1. Resolves <tag> → commit SHA via the GitHub REST API (peels
#      annotated tags the same way internal/splice/resolver.go does).
#   2. Downloads the source archive at that commit.
#   3. Extracts the cluster/compose/localnet/ subtree to a scratch dir.
#   4. Computes the deterministic ContentSHA via scripts/compute-tree-sha.sh.
#   5. Inserts a new entry into internal/splice/versions.json.
#   6. Prints the diff. Does NOT git-add or git-commit — that's deliberate
#      so a maintainer reviews the change before it lands.
#
# Auth: set GITHUB_TOKEN or GH_TOKEN to raise the GitHub API rate limit
# (required in CI; optional locally).
#
# Output: the suggested commit message line and a reminder to bump
# `latest_alias` if the new tag is the newest.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <tag>" >&2
  exit 2
fi
tag="$1"

repo="canton-network/splice"
versions_json="internal/splice/versions.json"
api="https://api.github.com"

token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
curl_auth=()
if [[ -n "$token" ]]; then
  curl_auth=(-H "Authorization: Bearer ${token}")
fi
# Same Accept/User-Agent on every API call so rate-limit and auth failures
# surface consistently whether or not a token is set.
curl_api=(curl -sSf "${curl_auth[@]}"
  -H "Accept: application/vnd.github+json"
  -H "User-Agent: canton-devkit-add-splice-version")

if [[ ! -f "$versions_json" ]]; then
  echo "error: $versions_json not found (run from repo root)" >&2
  exit 1
fi

# Pre-flight: refuse if the tag is already catalogued — the insert
# below would silently append a duplicate entry.
if python3 -c "import json,sys;[sys.exit(0) for v in json.load(open('$versions_json'))['versions'] if v['tag']=='$tag'];sys.exit(1)" 2>/dev/null; then
  echo "error: tag $tag is already in $versions_json. Remove the entry first if you want to update it." >&2
  exit 1
fi

echo "Resolving $tag → commit SHA via api.github.com..."
# Lightweight tags have object.type=commit; annotated tags need a
# second hop via object.url so we pin the peeled commit, not the tag object.
ref_json=$("${curl_api[@]}" "$api/repos/$repo/git/refs/tags/$tag")
commit=$(printf '%s' "$ref_json" | python3 -c '
import json,sys
d=json.load(sys.stdin)
obj=d.get("object",{})
t=obj.get("type","")
sha=obj.get("sha","")
url=obj.get("url","")
if t=="commit":
  print(sha)
elif t=="tag":
  if not url:
    sys.exit("annotated tag missing object.url")
  print("ANNOTATED|"+url)
else:
  sys.exit("expected ref type commit or tag, got "+repr(t))
')

if [[ "$commit" == ANNOTATED\|* ]]; then
  ann_url="${commit#ANNOTATED|}"
  echo "  annotated tag — peeling via $ann_url"
  commit=$("${curl_api[@]}" "$ann_url" | python3 -c '
import json,sys
d=json.load(sys.stdin)
sha=d.get("object",{}).get("sha","")
if not sha:
  sys.exit("empty SHA in annotated-tag object")
print(sha)
')
fi

if [[ -z "$commit" ]]; then
  echo "error: failed to resolve commit SHA for $tag" >&2
  exit 1
fi
echo "  commit = $commit"

# Auto-detect major from the tag (e.g. 0.6.5 → 0.6, 1.2.3 → 1.2). If
# the tag doesn't match N.N.N the maintainer fills it manually.
major=""
if [[ "$tag" =~ ^([0-9]+)\.([0-9]+)\.[0-9]+ ]]; then
  major="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
fi
echo "  major  = ${major:-(needs manual edit)}"

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

echo "Downloading archive ($scratch/source.tar.gz)..."
curl -sSfL "https://github.com/$repo/archive/$commit.tar.gz" -o "$scratch/source.tar.gz"
size=$(stat -f%z "$scratch/source.tar.gz" 2>/dev/null || stat -c%s "$scratch/source.tar.gz")
echo "  size   = $size bytes"

echo "Extracting cluster/compose/localnet/ subtree..."
mkdir "$scratch/tree" "$scratch/full"
# Full extract then copy the subtree — avoids GNU-tar wildcard /
# directory-member edge cases that left compose.yaml missing in CI.
tar -xzf "$scratch/source.tar.gz" -C "$scratch/full"
top=$(python3 -c "
import os
ents=os.listdir(r'$scratch/full')
if len(ents)!=1:
    raise SystemExit(f'expected one top-level dir, got {ents!r}')
print(ents[0])
")
src="$scratch/full/$top/cluster/compose/localnet"
if [[ ! -d "$src" ]]; then
  echo "error: archive missing cluster/compose/localnet/ — is this tag valid?" >&2
  exit 1
fi
cp -a "$src"/. "$scratch/tree/"

if [[ ! -f "$scratch/tree/compose.yaml" ]]; then
  echo "error: extracted tree missing compose.yaml — is this tag valid?" >&2
  exit 1
fi

echo "Computing ContentSHA..."
content_sha=$(scripts/compute-tree-sha.sh "$scratch/tree")
echo "  content_sha = $content_sha"

echo "Inserting entry into $versions_json..."
python3 <<PY
import json
path = "$versions_json"
with open(path) as f:
    data = json.load(f)
entry = {
    "tag": "$tag",
    "commit": "$commit",
    "content_sha": "$content_sha",
    "size": $size,
    "major": "${major}",
}
data["versions"].append(entry)
# Keep versions sorted ascending by tag (string-sort works for N.N.N).
data["versions"].sort(key=lambda v: v["tag"])
with open(path, "w") as f:
    json.dump(data, f, indent=2, ensure_ascii=False)
    f.write("\n")
PY

echo
echo "--- Diff ---"
git --no-pager diff "$versions_json" || true
echo
echo "Next steps:"
echo "  1. Review the diff above."
echo "  2. If $tag is now the newest version, bump \"latest_alias\" in $versions_json."
echo "  3. Commit:"
echo "       git add $versions_json"
echo "       git commit -m 'chore: catalogue Splice $tag (commit ${commit:0:12})'"
