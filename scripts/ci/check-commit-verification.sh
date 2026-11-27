#!/usr/bin/env bash
#
# Report commits in a range that are not GitHub-verified.
# GitHub's Commits API is authoritative; local git verify-commit is not.
# When run on a pull_request workflow with GITHUB_TOKEN + GITHUB_PR_NUMBER,
# posts (or refreshes) an advisory PR comment. Never fails the job for
# unverified commits (exit 0). Usage / setup errors still exit 2.
#
# Usage: check-commit-verification.sh <base-ref> [head-ref]
#        check-commit-verification.sh <revision-range>

set -euo pipefail

COMMENT_MARKER='<!-- canton-devkit-commit-verification -->'

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
comments_api="https://api.github.com/repos/${GITHUB_REPOSITORY}/issues"
auth_args=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
	auth_args=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

gh_api() {
	curl -sS \
		-H "Accept: application/vnd.github+json" \
		-H "X-GitHub-Api-Version: 2022-11-28" \
		"${auth_args[@]}" \
		"$@"
}

violations=0
report_lines=()

for sha in $commits; do
	subject="$(git show -s --format='%s' "$sha")"
	payload="$(gh_api "${api}/${sha}")"

	if ! printf '%s' "$payload" | jq -e '.commit.verification' >/dev/null 2>&1; then
		violations=$((violations + 1))
		echo "UNVERIFIED: ${sha} ${subject}"
		echo "    could not read commit.verification from GitHub API"
		msg="$(printf '%s' "$payload" | jq -r '.message // empty' 2>/dev/null || true)"
		[ -n "$msg" ] && echo "    api: ${msg}"
		report_lines+=("- \`${sha:0:7}\` ${subject} — API: could not read verification${msg:+ (${msg})}")
		continue
	fi

	verified="$(printf '%s' "$payload" | jq -r '.commit.verification.verified')"
	reason="$(printf '%s' "$payload" | jq -r '.commit.verification.reason')"

	if [ "$verified" != "true" ]; then
		violations=$((violations + 1))
		echo "UNVERIFIED: ${sha} ${subject}"
		echo "    verified: false (reason: ${reason})"
		report_lines+=("- \`${sha:0:7}\` ${subject} — reason: \`${reason}\`")
	fi
done

pr_number="${GITHUB_PR_NUMBER:-}"
if [ -z "$pr_number" ] && [ -n "${GITHUB_EVENT_PATH:-}" ] && [ -f "${GITHUB_EVENT_PATH}" ]; then
	pr_number="$(jq -r '.pull_request.number // empty' "${GITHUB_EVENT_PATH}")"
fi

find_sticky_comment_id() {
	local page=1
	local payload
	local id
	while true; do
		payload="$(gh_api "${comments_api}/${pr_number}/comments?per_page=100&page=${page}")"
		if ! printf '%s' "$payload" | jq -e 'type == "array"' >/dev/null 2>&1; then
			echo "warning: could not list PR comments" >&2
			msg="$(printf '%s' "$payload" | jq -r '.message // empty' 2>/dev/null || true)"
			[ -n "$msg" ] && echo "    api: ${msg}" >&2
			return 1
		fi
		id="$(printf '%s' "$payload" | jq -r --arg m "$COMMENT_MARKER" '.[] | select(.body | contains($m)) | .id' | head -n1)"
		if [ -n "$id" ]; then
			printf '%s' "$id"
			return 0
		fi
		if [ "$(printf '%s' "$payload" | jq 'length')" -lt 100 ]; then
			return 1
		fi
		page=$((page + 1))
	done
}

post_or_update_comment() {
	local body="$1"
	local existing_id
	local http_code
	local tmp

	if [ -z "${GITHUB_TOKEN:-}" ] || [ -z "$pr_number" ]; then
		echo "advisory: skipping PR comment (GITHUB_TOKEN or GITHUB_PR_NUMBER unset)"
		return 0
	fi

	existing_id=""
	existing_id="$(find_sticky_comment_id || true)"

	tmp="$(mktemp)"
	if [ -n "$existing_id" ]; then
		http_code="$(
			curl -sS -o "$tmp" -w '%{http_code}' \
				-X PATCH \
				-H "Accept: application/vnd.github+json" \
				-H "X-GitHub-Api-Version: 2022-11-28" \
				"${auth_args[@]}" \
				-H "Content-Type: application/json" \
				--data "$(jq -n --arg body "$body" '{body: $body}')" \
				"${comments_api}/comments/${existing_id}"
		)"
	else
		http_code="$(
			curl -sS -o "$tmp" -w '%{http_code}' \
				-X POST \
				-H "Accept: application/vnd.github+json" \
				-H "X-GitHub-Api-Version: 2022-11-28" \
				"${auth_args[@]}" \
				-H "Content-Type: application/json" \
				--data "$(jq -n --arg body "$body" '{body: $body}')" \
				"${comments_api}/${pr_number}/comments"
		)"
	fi

	case "$http_code" in
	200 | 201)
		echo "advisory: posted PR comment on #${pr_number}"
		;;
	*)
		echo "warning: failed to post PR comment (HTTP ${http_code}); check stays green" >&2
		jq -r '.message // empty' "$tmp" 2>/dev/null | sed '/^$/d' | sed 's/^/    api: /' >&2 || true
		;;
	esac
	rm -f "$tmp"
}

delete_sticky_comment() {
	local existing_id
	local http_code
	local tmp

	if [ -z "${GITHUB_TOKEN:-}" ] || [ -z "$pr_number" ]; then
		return 0
	fi

	existing_id=""
	existing_id="$(find_sticky_comment_id || true)"
	[ -n "$existing_id" ] || return 0

	tmp="$(mktemp)"
	http_code="$(
		curl -sS -o "$tmp" -w '%{http_code}' \
			-X DELETE \
			-H "Accept: application/vnd.github+json" \
			-H "X-GitHub-Api-Version: 2022-11-28" \
			"${auth_args[@]}" \
			"${comments_api}/comments/${existing_id}"
	)"
	case "$http_code" in
	204)
		echo "advisory: cleared stale PR comment on #${pr_number}"
		;;
	*)
		echo "warning: failed to delete stale PR comment (HTTP ${http_code})" >&2
		;;
	esac
	rm -f "$tmp"
}

if [ "$violations" -gt 0 ]; then
	echo
	echo "${violations} commit(s) are not GitHub-verified in range ${range}."
	echo "This check is advisory and does not fail CI. Sign each commit with a"
	echo "GPG or SSH key registered to the commit author on GitHub (Settings →"
	echo "SSH and GPG keys → Signing keys), then force-push. A Cursor/Cloud-Agent"
	echo "SSH signature on a human-authored commit yields reason=unknown_key"
	echo "(Unverified) — re-sign as the author."

	body="$(
		cat <<EOF
${COMMENT_MARKER}
## Commit verification (advisory)

${violations} commit(s) in this PR are not GitHub-**Verified**:

$(printf '%s\n' "${report_lines[@]}")

Sign each commit with a GPG or SSH key registered to the commit author
(Settings → SSH and GPG keys → **Signing keys**), then force-push. Cursor /
Cloud Agent SSH signatures on human-authored commits often show as
\`unknown_key\` (Unverified) — re-sign as the author:

\`\`\`sh
git rebase --exec 'git commit --amend --no-edit -S' origin/main
git push --force-with-lease
\`\`\`

_This check is non-blocking; it only posts this comment._
EOF
	)"
	post_or_update_comment "$body"
	exit 0
fi

delete_sticky_comment
echo "OK: all $(echo "$commits" | wc -l | tr -d ' ') commit(s) are GitHub-verified."
