#!/usr/bin/env bash
#
# Fail if any commit in a range is authored, committed, or co-authored by an AI
# coding tool (Claude / Anthropic / Cursor).
#
# Usage: check-ai-authorship.sh <base-ref> [head-ref]
#        check-ai-authorship.sh <revision-range>

set -euo pipefail

# Matched case-insensitively against a whole "Name <email>" identity, with
# non-alphanumeric boundaries so names like "Claudia" or "precursor" pass.
PATTERN='(^|[^[:alnum:]])(claude|anthropic|cursor|cursoragent)([^[:alnum:]]|$)'

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

commits="$(git rev-list --no-merges "$range")"

if [ -z "$commits" ]; then
	echo "OK: no commits in range $range"
	exit 0
fi

violations=0

for sha in $commits; do
	subject="$(git show -s --format='%s' "$sha")"
	offenders=""

	author="$(git show -s --format='%an <%ae>' "$sha")"
	if printf '%s' "$author" | grep -Eiq "$PATTERN"; then
		offenders="${offenders}    author:    ${author}"$'\n'
	fi

	committer="$(git show -s --format='%cn <%ce>' "$sha")"
	if printf '%s' "$committer" | grep -Eiq "$PATTERN"; then
		offenders="${offenders}    committer: ${committer}"$'\n'
	fi

	# Scan the raw body, not just parsed trailers, so a Co-authored-by line that
	# is not in the final trailer block is still caught.
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		if printf '%s' "$line" | grep -Eiq "$PATTERN"; then
			offenders="${offenders}    ${line}"$'\n'
		fi
	done < <(git show -s --format='%B' "$sha" | grep -Ei '^[[:space:]]*co-authored-by:' || true)

	if [ -n "$offenders" ]; then
		violations=$((violations + 1))
		echo "FAILED: ${sha} ${subject}"
		printf '%s' "$offenders"
	fi
done

if [ "$violations" -gt 0 ]; then
	echo
	echo "${violations} commit(s) carry AI-tool authorship in range ${range}."
	echo "Rewrite the offending commits with a human author and drop the AI"
	echo "Co-authored-by trailers, then force-push the branch."
	exit 1
fi

echo "OK: no AI-tool authorship found in $(echo "$commits" | wc -l | tr -d ' ') commit(s)."
