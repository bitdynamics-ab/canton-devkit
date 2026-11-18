#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
# shellcheck source=../../scripts/ci/coding-tool-identity.sh
source "${repo_root}/scripts/ci/coding-tool-identity.sh"

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

if ! printf '%s' "${command}" | grep -Eq '(^|[[:space:];|&])git([[:space:]]+|-c[[:space:]]|-)([^;|&]*[[:space:]])?commit([[:space:]]|$)'; then
  allow
fi

if [ -x "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh" ]; then
  bash "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh" >/dev/null 2>&1 || true
fi

identity_is_coding_tool() {
  printf '%s' "$1" | grep -Eiq "${CODING_TOOL_IDENTITY_PATTERN}"
}

resolve_identity_part() {
  local var="$1" env_val="$2"
  local from_cmd
  from_cmd="$(env_value_from_command "${var}" "${command}")"
  if [ -n "${from_cmd}" ]; then
    printf '%s' "${from_cmd}"
    return
  fi
  if [ -n "${env_val}" ]; then
    printf '%s' "${env_val}"
    return
  fi
  case "${var}" in
    GIT_AUTHOR_NAME | GIT_COMMITTER_NAME)
      git -C "${repo_root}" config --get user.name 2>/dev/null || true
      ;;
    GIT_AUTHOR_EMAIL | GIT_COMMITTER_EMAIL)
      git -C "${repo_root}" config --get user.email 2>/dev/null || true
      ;;
  esac
}

author_name="$(resolve_identity_part GIT_AUTHOR_NAME "${GIT_AUTHOR_NAME:-}")"
author_email="$(resolve_identity_part GIT_AUTHOR_EMAIL "${GIT_AUTHOR_EMAIL:-}")"
committer_name="$(resolve_identity_part GIT_COMMITTER_NAME "${GIT_COMMITTER_NAME:-}")"
committer_email="$(resolve_identity_part GIT_COMMITTER_EMAIL "${GIT_COMMITTER_EMAIL:-}")"

author="${author_name} <${author_email}>"
committer="${committer_name} <${committer_email}>"

if identity_is_coding_tool "${author}" || identity_is_coding_tool "${committer}"; then
  deny "git commit blocked: author/committer looks like a coding tool (${author} / ${committer}). Prefix the same shell command with human GIT_AUTHOR_* and GIT_COMMITTER_* (the gate reads those from the command text). Example:
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
