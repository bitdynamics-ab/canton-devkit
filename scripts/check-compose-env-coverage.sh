#!/usr/bin/env bash
# Drift-detect env-var coverage between Splice's compose.yaml and DevKit's
# adapter OverlayEnv()/UIPortEnvVars()/EnvFiles().
#
# Splice's compose.yaml references env vars via `${SOMETHING}`. DevKit
# satisfies them three ways:
#
#   1. Adapter.OverlayEnv()    - vars DevKit sets explicitly per instance
#   2. UIPortEnvVars()         - host ports DevKit allocates ephemerally
#   3. Splice env files        - common.env / postgres.env / splice.env
#                                / role-auth.env (loaded by adapter.EnvFiles)
#
# If Splice adds a new ${VAR} that none of the above provide, every
# `localnet up` will substitute empty and silently misbehave. This
# script enumerates that gap.
#
# Usage:
#   scripts/check-compose-env-coverage.sh /path/to/splice-cache-dir
#   (defaults to the most-recently-cached Splice if no arg given)
#
# Exits non-zero if any ${VAR} in compose.yaml is unaccounted for, so
# the script is suitable as a CI gate.

set -euo pipefail

cache_root="${1:-${HOME}/.canton-devkit/cache}"
if [[ ! -d "$cache_root" ]]; then
  cache_root="${HOME}/.canton/devkit-cache"
fi
if [[ ! -d "$cache_root" ]]; then
  echo "no Splice cache found; run 'canton-devkit localnet up --name probe' once first" >&2
  exit 2
fi

# Pick the most recently modified splice-* dir.
project=$(ls -td "$cache_root"/splice-* 2>/dev/null | head -1 || true)
if [[ -z "$project" ]]; then
  echo "no splice-* dirs under $cache_root" >&2
  exit 2
fi

compose="$project/compose.yaml"
if [[ ! -f "$compose" ]]; then
  echo "$compose missing" >&2
  exit 2
fi

echo "Scanning $compose"

# REQUIRED ${VAR} references in compose.yaml. A reference counts as
# required only if it has NO default: bare `${VAR}` but not `${VAR:-...}`
# or `${VAR-...}`. Refs with defaults are harmless when DevKit doesn't
# set the value (compose substitutes the default).
referenced=$(grep -oE '\$\{[A-Z_]+[A-Z0-9_]*\}' "$compose" \
  | sed -E 's/^\$\{([A-Z_][A-Z0-9_]*)\}$/\1/' \
  | sort -u)

# Vars DevKit's adapter.OverlayEnv() always sets. Mirrors
# internal/splice/v05/adapter.go and v06/adapter.go.
overlay_env=(
  LOCALNET_DIR LOCALNET_ENV_DIR IMAGE_TAG DOCKER_NETWORK PARTY_HINT
  COMPOSE_PROFILES TEST_PORT ALPHA_PROTOCOL_VERSION_ENV
)

# Vars DevKit allocates as ephemeral host ports. Mirrors
# internal/localnet/portblock.go.UIPortEnvVars().
ui_port_env=(
  APP_USER_UI_PORT APP_PROVIDER_UI_PORT SV_UI_PORT SWAGGER_UI_PORT DB_PORT
)

# Vars provided by the Splice env files DevKit loads via Adapter.EnvFiles().
# Extracted by grep-ing the files we know exist in the cached project.
env_files_provide=$(
  for f in "$project"/env/common.env "$project"/compose.env \
           "$project"/env/postgres.env "$project"/env/splice.env \
           "$project"/env/alpha-protocol-version.env \
           "$project"/env/sv-auth-on.env "$project"/env/app-provider-auth-on.env \
           "$project"/env/app-user-auth-on.env; do
    [[ -f "$f" ]] && grep -oE '^[A-Z_][A-Z0-9_]*' "$f" 2>/dev/null
  done | sort -u
)

# Build the union of all "satisfied" sets.
satisfied=$( (
  printf '%s\n' "${overlay_env[@]}"
  printf '%s\n' "${ui_port_env[@]}"
  echo "$env_files_provide"
) | sort -u )

# Diff.
missing=$(comm -23 <(echo "$referenced") <(echo "$satisfied") || true)

if [[ -z "$missing" ]]; then
  echo "OK - every \${VAR} in compose.yaml is satisfied by DevKit overlay env, UI port alloc, or a Splice env file."
  exit 0
fi

echo
echo "DRIFT - compose.yaml references env vars DevKit does not provide:"
while IFS= read -r v; do
  echo "  - \${$v}"
done <<<"$missing"
echo
echo "Possible fixes:"
echo "  1. Add to Adapter.OverlayEnv() if DevKit should compute it per instance."
echo "  2. Add to UIPortEnvVars() if it's a new host-bound port."
echo "  3. Add a Splice env file path to Adapter.EnvFiles() if upstream now ships it."
echo "  4. Document a default if the empty-string fallback is intentional."
exit 1
