#!/usr/bin/env bash
#
# Fail if any commit in a range is not GitHub-verified.
# GitHub's Commits API is authoritative; local git verify-commit is not.
#
# Usage: check-commit-verification.sh <base-ref> [head-ref]
#        check-commit-verification.sh <revision-range>

set -euo pipefail

usage() {
	echo "usage: $0 <base-ref> [head-ref]" >&2
	echo "       $0 <revision-range>" >&2
	exit 2
}

[ "$#" -ge 1 ] || usage

if [ "$#" -eq 1 ]; then
	range="$1"
else
	range="$1..${2:-HEAD}"
fi

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set (owner/repo)}"

if ! command -v jq >/dev/null 2>&1; then
	echo "jq is required" >&2
	exit 2
fi

commits="$(git rev-list --no-merges "$range")"

if [ -z "$commits" ]; then
	echo "OK: no commits in range $range"
	exit 0
fi

api="https://api.github.com/repos/${GITHUB_REPOSITORY}/commits"
auth_args=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
	auth_args=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

violations=0

for sha in $commits; do
	subject="$(git show -s --format='%s' "$sha")"
	payload="$(
		curl -sS \
			-H "Accept: application/vnd.github+json" \
			-H "X-GitHub-Api-Version: 2022-11-28" \
			"${auth_args[@]}" \
			"${api}/${sha}"
	)"

	if ! printf '%s' "$payload" | jq -e '.commit.verification' >/dev/null 2>&1; then
		violations=$((violations + 1))
		echo "FAILED: ${sha} ${subject}"
		echo "    could not read commit.verification from GitHub API"
		msg="$(printf '%s' "$payload" | jq -r '.message // empty' 2>/dev/null || true)"
		[ -n "$msg" ] && echo "    api: ${msg}"
		continue
	fi

	verified="$(printf '%s' "$payload" | jq -r '.commit.verification.verified')"
	reason="$(printf '%s' "$payload" | jq -r '.commit.verification.reason')"

	if [ "$verified" != "true" ]; then
		violations=$((violations + 1))
		echo "FAILED: ${sha} ${subject}"
		echo "    verified: false (reason: ${reason})"
	fi
done

if [ "$violations" -gt 0 ]; then
	echo
	echo "${violations} commit(s) are not GitHub-verified in range ${range}."
	echo "Sign each commit with a GPG or SSH key registered to the commit"
	echo "author on GitHub (Settings → SSH and GPG keys → Signing keys), then"
	echo "force-push. A Cursor/Cloud-Agent SSH signature on a human-authored"
	echo "commit yields reason=unknown_key (Unverified) — re-sign as the author."
	exit 1
fi

echo "OK: all $(echo "$commits" | wc -l | tr -d ' ') commit(s) are GitHub-verified."
