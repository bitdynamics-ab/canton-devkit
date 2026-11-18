#!/usr/bin/env bash
set -euo pipefail

msg_file="${1:-}"
if [ -z "${msg_file}" ] || [ ! -f "${msg_file}" ]; then
  exit 0
fi

tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT

grep -viE '^[[:space:]]*Co-authored-by:[[:space:]]*Zhe Li[[:space:]]*<linuxcity\.jn@gmail\.com>[[:space:]]*$' \
  "${msg_file}" > "${tmp}" || true
mv "${tmp}" "${msg_file}"
trap - EXIT
