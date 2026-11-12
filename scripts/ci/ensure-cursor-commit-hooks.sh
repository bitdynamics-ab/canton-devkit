#!/usr/bin/env bash
#
# Wire repo commit-msg hooks into Cursor Cloud's managed hooksPath so the
# initiator Co-authored-by trailer is stripped after Cursor re-adds it.
#
# Idempotent. Safe to run from Agent beforeShellExecution, environment
# start, or manually.
#
# What it does:
#   1. Points the Cursor dispatcher's .cursor-original-hooks-path at
#      <repo>/.githooks so our commit-msg runs in the chain.
#   2. Installs commit-msg.cursor.zz-strip-coauthor into the managed
#      hooks dir (alphabetically after commit-msg.cursor.co-author) that
#      strips Co-authored-by: Zhe Li <linuxcity.jn@gmail.com>.
#
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
githooks="${repo_root}/.githooks"
strip_src="${githooks}/strip-coauthored-by.sh"

if [ ! -x "${githooks}/commit-msg" ] || [ ! -f "${strip_src}" ]; then
  echo "error: missing executable .githooks/commit-msg or strip-coauthored-by.sh" >&2
  exit 1
fi

chmod +x "${githooks}/commit-msg" "${strip_src}" 2>/dev/null || true

hooks_path="$(git -C "${repo_root}" config --get core.hooksPath || true)"
if [ -z "${hooks_path}" ]; then
  # Local / non-Cursor: use .githooks directly.
  git -C "${repo_root}" config core.hooksPath "${githooks}"
  echo "set core.hooksPath=${githooks}"
  exit 0
fi

# Resolve relative hooksPath against the repo.
case "${hooks_path}" in
  /*) managed="${hooks_path}" ;;
  *) managed="${repo_root}/${hooks_path}" ;;
esac

if [ ! -d "${managed}" ]; then
  echo "warning: core.hooksPath=${hooks_path} is not a directory; skipping Cursor wire-up" >&2
  exit 0
fi

# Only touch Cursor-managed dispatcher trees.
if [ ! -f "${managed}/.dispatcher" ] && [ ! -f "${managed}/.cursor-original-hooks-path" ]; then
  echo "core.hooksPath=${hooks_path} is not a Cursor agent-hooks dir; leaving as-is"
  exit 0
fi

printf '%s\n' "${githooks}" > "${managed}/.cursor-original-hooks-path"
echo "pointed .cursor-original-hooks-path -> ${githooks}"

# Post-hook: must sort after commit-msg.cursor.co-author.
zz_hook="${managed}/commit-msg.cursor.zz-strip-coauthor"
cat > "${zz_hook}" <<EOF
#!/usr/bin/env bash
# @cursor-managed-by-repo — installed by scripts/ci/ensure-cursor-commit-hooks.sh
# Runs after commit-msg.cursor.co-author (zz- sorts after co-author).
set -euo pipefail
exec bash "${strip_src}" "\$1"
EOF
chmod +x "${zz_hook}"
echo "installed ${zz_hook}"
