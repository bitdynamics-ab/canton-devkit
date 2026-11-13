#!/usr/bin/env bash
# beforeShellExecution: gate git commit for this repo.
# - Ensures Cursor commit-msg strip hooks are installed
# - Denies AI-tool author/committer identities (same idea as
#   scripts/ci/check-authorship.sh)
# - Denies an explicit initiator Co-authored-by in the command text
#
# Stdin: JSON { command, cwd, ... }
# Stdout: JSON { permission, agent_message?, user_message? }
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

input="$(cat)"
command="$(printf '%s' "${input}" | jq -r '.command // empty')"

allow() {
  jq -nc --arg msg "${1:-}" \
    '{permission:"allow"} + (if $msg == "" then {} else {agent_message:$msg} end)'
  exit 0
}

deny() {
  local msg="$1"
  jq -nc --arg msg "${msg}" '{permission:"deny",agent_message:$msg,user_message:$msg}'
  exit 0
}

# Only care about git commit (including amend). Matcher already filters, but
# be defensive for hooks.json edits.
if ! printf '%s' "${command}" | grep -Eq '(^|[[:space:];|&])git([[:space:]]+|-c[[:space:]]|-)([^;|&]*[[:space:]])?commit([[:space:]]|$)'; then
  allow
fi

# Keep the strip hook after Cursor's co-author hook.
if [ -x "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh" ]; then
  bash "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh" >/dev/null 2>&1 || true
fi

# Same boundary-aware pattern as scripts/ci/check-authorship.sh.
AI_PATTERN='(^|[^[:alnum:]])(claude|anthropic|cursor|cursoragent)([^[:alnum:]]|$)'

identity_is_ai() {
  printf '%s' "$1" | grep -Eiq "${AI_PATTERN}"
}

author_name="${GIT_AUTHOR_NAME:-}"
author_email="${GIT_AUTHOR_EMAIL:-}"
committer_name="${GIT_COMMITTER_NAME:-}"
committer_email="${GIT_COMMITTER_EMAIL:-}"

# Fall back to git config when env overrides are absent.
if [ -z "${author_name}" ]; then
  author_name="$(git -C "${repo_root}" config --get user.name 2>/dev/null || true)"
fi
if [ -z "${author_email}" ]; then
  author_email="$(git -C "${repo_root}" config --get user.email 2>/dev/null || true)"
fi
if [ -z "${committer_name}" ]; then
  committer_name="$(git -C "${repo_root}" config --get user.name 2>/dev/null || true)"
fi
if [ -z "${committer_email}" ]; then
  committer_email="$(git -C "${repo_root}" config --get user.email 2>/dev/null || true)"
fi

author="${author_name} <${author_email}>"
committer="${committer_name} <${committer_email}>"

if identity_is_ai "${author}" || identity_is_ai "${committer}"; then
  deny "git commit blocked: author/committer looks like an AI tool (${author} / ${committer}). Re-run with human GIT_AUTHOR_* and GIT_COMMITTER_* (and no Co-authored-by: Zhe Li trailer). Example:
GIT_AUTHOR_NAME='Zhe Li' GIT_AUTHOR_EMAIL='linuxcity.jn@gmail.com' \\
GIT_COMMITTER_NAME='Zhe Li' GIT_COMMITTER_EMAIL='linuxcity.jn@gmail.com' \\
git commit ..."
fi

if printf '%s' "${command}" | grep -Eiq 'Co-authored-by:[[:space:]]*Zhe Li[[:space:]]*<linuxcity\.jn@gmail\.com>'; then
  deny "git commit blocked: do not put 'Co-authored-by: Zhe Li <linuxcity.jn@gmail.com>' in the commit message. Use human author/committer instead; the repo hooks strip Cursor's injected copy of that trailer."
fi

if printf '%s' "${command}" | grep -Eq -- '--no-verify|--no-v\b'; then
  deny "git commit blocked: --no-verify skips the commit-msg hook that strips Cursor's Co-authored-by trailer and would fail Check commit authorship. Omit --no-verify."
fi

allow "Commit authorship looks human. Repo hooks will strip Co-authored-by: Zhe Li if Cursor re-injects it."
