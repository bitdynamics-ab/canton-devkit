#!/usr/bin/env bash
# Strip the Cloud Agent initiator Co-authored-by trailer from a commit
# message file ($1). Shared by .githooks/commit-msg and the Cursor
# agent-hooks post-hook installed by scripts/ci/ensure-cursor-commit-hooks.sh.
#
# Cursor's commit-msg.cursor.co-author appends:
#   Co-authored-by: Zhe Li <linuxcity.jn@gmail.com>
# after repo hooks run. A later *.cursor.zz-* hook (or a local commit-msg
# when Cursor isn't in the chain) removes it again.
set -euo pipefail

msg_file="${1:-}"
if [ -z "${msg_file}" ] || [ ! -f "${msg_file}" ]; then
  exit 0
fi

# Exact initiator identity Cursor injects for this workspace's owner.
# Match case-insensitively; keep any other Co-authored-by lines.
tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT

grep -viE '^[[:space:]]*Co-authored-by:[[:space:]]*Zhe Li[[:space:]]*<linuxcity\.jn@gmail\.com>[[:space:]]*$' \
  "${msg_file}" > "${tmp}" || true
mv "${tmp}" "${msg_file}"
trap - EXIT
