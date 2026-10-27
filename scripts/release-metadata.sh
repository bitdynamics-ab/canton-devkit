#!/usr/bin/env bash
set -euo pipefail

ref="${1:?expected a git ref}"
sha="${2:?expected a commit SHA}"
[[ "$sha" =~ ^[0-9a-f]{7,40}$ ]] || { echo "Invalid commit SHA" >&2; exit 1; }

push=false
prerelease=true
campaign=off
channel=dev
if [[ "$ref" == refs/tags/* ]]; then
  version="${ref#refs/tags/}"
  number='(0|[1-9][0-9]*)'
  semver="^v${number}\\.${number}\\.${number}(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$"
  [[ "$version" =~ $semver ]] || { echo "Expected a full vMAJOR.MINOR.PATCH release tag" >&2; exit 1; }
  component="${version#v}"
  push=true
  if [[ "$version" != *-* ]]; then
    prerelease=false
    channel=stable
  elif [[ "$version" == *-cctools* ]]; then
    campaign_tag="^v${number}\\.${number}\\.${number}-cctools\\.[1-9][0-9]*$"
    [[ "$version" =~ $campaign_tag ]] || { echo "Expected -cctools.N with N >= 1" >&2; exit 1; }
    campaign=on
  fi
else
  version="dev-${sha:0:7}"
  component="0.0.0-${sha:0:7}"
fi

printf '%s\n' "devkit_version=$version" "component_version=$component" \
  "push=$push" "prerelease=$prerelease" "campaign=$campaign" "channel=$channel"
