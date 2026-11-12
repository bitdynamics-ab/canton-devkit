#!/usr/bin/env bash
# Regression checks for .githooks strip + Cursor beforeShellExecution gate.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

assert_eq() {
  local name="$1" got="$2" exp="$3"
  if [ "${got}" != "${exp}" ]; then
    echo "FAIL: ${name}" >&2
    echo "  got:      ${got}" >&2
    echo "  expected: ${exp}" >&2
    exit 1
  fi
}

assert_file_lacks() {
  local name="$1" file="$2" needle="$3"
  if grep -Fiq "${needle}" "${file}"; then
    echo "FAIL: ${name} — ${file} still contains: ${needle}" >&2
    exit 1
  fi
}

assert_file_contains() {
  local name="$1" file="$2" needle="$3"
  if ! grep -Fq "${needle}" "${file}"; then
    echo "FAIL: ${name} — ${file} missing: ${needle}" >&2
    exit 1
  fi
}

# --- strip-coauthored-by.sh ----------------------------------------------
msg="${tmp}/msg"
cat > "${msg}" <<'EOF'
docs: example

Body text.

Co-authored-by: Zhe Li <linuxcity.jn@gmail.com>
Co-authored-by: Someone Else <other@example.com>
EOF

bash "${repo_root}/.githooks/strip-coauthored-by.sh" "${msg}"
assert_file_lacks "strip initiator" "${msg}" "Zhe Li"
assert_file_contains "keep other" "${msg}" "Co-authored-by: Someone Else <other@example.com>"
assert_file_contains "keep body" "${msg}" "Body text."

# Case-insensitive strip
cat > "${msg}" <<'EOF'
subject

co-authored-by: zhe li <linuxcity.jn@gmail.com>
EOF
bash "${repo_root}/.githooks/strip-coauthored-by.sh" "${msg}"
assert_file_lacks "case fold" "${msg}" "linuxcity"

# --- beforeShellExecution gate -------------------------------------------
hook="${repo_root}/.cursor/hooks/check-git-commit.sh"
chmod +x "${hook}" "${repo_root}/.githooks/"* "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh"

# Env vars must prefix `bash` (not `printf`) so the hook process sees them.
# AI author -> deny
out="$(
  printf '%s' '{"command":"git commit -m test","cwd":"'"${repo_root}"'"}' | \
  GIT_AUTHOR_NAME='Cursor Agent' GIT_AUTHOR_EMAIL='cursoragent@cursor.com' \
  GIT_COMMITTER_NAME='Cursor Agent' GIT_COMMITTER_EMAIL='cursoragent@cursor.com' \
  bash "${hook}"
)"
assert_eq "deny ai author permission" "$(printf '%s' "${out}" | jq -r .permission)" "deny"

# Human author -> allow
out="$(
  printf '%s' '{"command":"git commit -m test","cwd":"'"${repo_root}"'"}' | \
  GIT_AUTHOR_NAME='Zhe Li' GIT_AUTHOR_EMAIL='linuxcity.jn@gmail.com' \
  GIT_COMMITTER_NAME='Zhe Li' GIT_COMMITTER_EMAIL='linuxcity.jn@gmail.com' \
  bash "${hook}"
)"
assert_eq "allow human author permission" "$(printf '%s' "${out}" | jq -r .permission)" "allow"

# Explicit Zhe Co-authored-by in command -> deny
out="$(
  printf '%s' '{"command":"git commit -m \"msg\n\nCo-authored-by: Zhe Li <linuxcity.jn@gmail.com>\"","cwd":"'"${repo_root}"'"}' | \
  GIT_AUTHOR_NAME='Zhe Li' GIT_AUTHOR_EMAIL='linuxcity.jn@gmail.com' \
  GIT_COMMITTER_NAME='Zhe Li' GIT_COMMITTER_EMAIL='linuxcity.jn@gmail.com' \
  bash "${hook}"
)"
assert_eq "deny explicit trailer permission" "$(printf '%s' "${out}" | jq -r .permission)" "deny"

# --no-verify -> deny
out="$(
  printf '%s' '{"command":"git commit --no-verify -m test","cwd":"'"${repo_root}"'"}' | \
  GIT_AUTHOR_NAME='Zhe Li' GIT_AUTHOR_EMAIL='linuxcity.jn@gmail.com' \
  GIT_COMMITTER_NAME='Zhe Li' GIT_COMMITTER_EMAIL='linuxcity.jn@gmail.com' \
  bash "${hook}"
)"
assert_eq "deny no-verify permission" "$(printf '%s' "${out}" | jq -r .permission)" "deny"

# --- ensure script against a fake Cursor hooks dir -----------------------
fake="${tmp}/agent-hooks"
mkdir -p "${fake}"
printf '%s' '/tmp/original' > "${fake}/.cursor-original-hooks-path"
touch "${fake}/.dispatcher"
chmod +x "${fake}/.dispatcher"
# Pretend git uses this hooksPath for a nested temp repo? ensure reads
# git config from repo_root — so temporarily set and restore.
old_hooks="$(git -C "${repo_root}" config --get core.hooksPath || true)"
git -C "${repo_root}" config core.hooksPath "${fake}"
bash "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh" >/dev/null
assert_file_contains "original path" "${fake}/.cursor-original-hooks-path" "${repo_root}/.githooks"
[ -x "${fake}/commit-msg.cursor.zz-strip-coauthor" ] || {
  echo "FAIL: zz-strip hook not installed" >&2
  exit 1
}
# Restore prior hooksPath (Cursor managed path, or unset).
if [ -n "${old_hooks}" ]; then
  git -C "${repo_root}" config core.hooksPath "${old_hooks}"
else
  git -C "${repo_root}" config --unset core.hooksPath || true
fi

bash -n "${repo_root}/.githooks/commit-msg"
bash -n "${repo_root}/.githooks/strip-coauthored-by.sh"
bash -n "${repo_root}/scripts/ci/ensure-cursor-commit-hooks.sh"
bash -n "${hook}"

echo "OK: commit-hooks"
