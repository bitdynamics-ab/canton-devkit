# Plan: Weekly automated verification of the public canton-devkit DPM/OCI artifact

Hand-off plan for implementing a scheduled GitHub Actions workflow that verifies the
`ghcr.io/bitdynamics-ab/canton-devkit` DPM component is publicly pullable and
installable.

- **Workflow to create:** `.github/workflows/verify-public-oci.yml` in this repo.
- **Status:** plan only — nothing has been created yet.

---

## 1. Goal

A **weekly** (Mondays 05:00 UTC) + manually-dispatchable workflow on the self-hosted
Linux e2e runner that proves the published DPM component is:

1. **Anonymously pullable** from GHCR — i.e. the package is genuinely Public.
2. **Multi-arch in metadata** — the OCI index advertises all three release platforms
   (`linux/amd64`, `darwin/arm64`, `windows/amd64`).
3. **Installable and runnable (linux/amd64)** — `dpm install package oci://…` succeeds
   and `dpm localnet --help` runs the installed binary.

A failed run means something regressed: the package was flipped private, a release
broke the artifact, or a platform is missing from the index.

---

## 2. Runner constraint

The e2e runner is **Linux/amd64 only**. Therefore:

- **Functional execution** (Steps 4–5) tests **linux/amd64** only.
- **Multi-arch** (Step 3) is verified at **index-metadata level** (reading JSON from
  the OCI index manifest) — not by running the other-arch binaries.

---

## 3. Resolved: no `daml.yaml` / `sdk-version` needed

`dpm install package` accepts the OCI ref as a positional argument:

```sh
dpm install package oci://ghcr.io/bitdynamics-ab/canton-devkit:<version>
```

This requires no project file, no `sdk-version`, and no SDK download. The
`daml.yaml`-with-`components:` approach shown in `docs/getting-started.md` is the
end-user workflow; the direct-ref form works fine for CI verification.

---

## 4. Triggers

```yaml
on:
  schedule:
    - cron: "0 5 * * 1"   # Mon 05:00 UTC — offset from e2e(04:00) / integration(03:00) / refresh-versions(Mon 06:00)
  workflow_dispatch:
    inputs:
      version:
        description: "OCI tag to verify (default: latest)"
        required: false
        default: "latest"
```

---

## 5. Job header

```yaml
permissions:
  contents: read            # read-only detective check — no packages:write

jobs:
  verify:
    name: verify-public-oci
    runs-on: [self-hosted, Linux, X64, proxmox, e2e]
    timeout-minutes: 20
    env:
      NS: bitdynamics-ab/canton-devkit
      # Keep these in sync with release.yml (DPM_VERSION / DPM_LINUX_SHA256).
      # Add a cross-reference comment in both files when bumping.
      DPM_VERSION: "1.0.16"
      DPM_LINUX_SHA256: "387421d4b3d0e799f05cde1f5c2adc704acd2824796d436861602eb2be759874"
```

---

## 6. Steps

### Step 1 — Resolve version & ensure anonymity

```sh
set -euo pipefail
VERSION="${{ github.event.inputs.version || 'latest' }}"
echo "VERSION=${VERSION}" >> "$GITHUB_ENV"
# Defeat any cached runner docker credentials — this test must be truly anonymous.
docker logout ghcr.io || true
```

### Step 2 — Anonymous registry fetch (raw v2 API, no docker pull)

Fetch the index manifest without credentials using only a public registry token:

```sh
token=$(curl -fsS "https://ghcr.io/token?scope=repository:${NS}:pull" | jq -r .token)
code=$(curl -sS -o manifest.json -w '%{http_code}' \
  -H "Authorization: Bearer ${token}" \
  -H "Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json" \
  "https://ghcr.io/v2/${NS}/manifests/${VERSION}")
test "$code" = "200" || {
  echo "::error::GHCR returned HTTP ${code} anonymously for ${NS}:${VERSION} — package may be private"
  exit 1
}
```

> Note: `jq` is used here. Confirm it is installed on the e2e runner (it is used in
> other scripts in this repo). If not available, fall back to:
> `token=$(curl -fsS "..." | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')`

### Step 3 — Multi-arch metadata assertion (index JSON, not execution)

Assert that all three release platforms appear in the OCI index:

```sh
for plat in linux/amd64 darwin/arm64 windows/amd64; do
  os="${plat%/*}"; arch="${plat#*/}"
  jq -e --arg os "$os" --arg arch "$arch" \
    '.manifests[]?.platform | select(.os==$os and .architecture==$arch)' manifest.json > /dev/null \
    || {
      echo "::error::OCI index for ${NS}:${VERSION} is missing platform ${plat}"
      exit 1
    }
done
```

This runs fine on Linux because it only reads JSON. It fails if the manifest is a
single-platform manifest rather than an index — which is the expected failure mode
for a malformed release.

### Step 4 — Install DPM CLI (verbatim from `release.yml`, sha256-verified)

```sh
tar="${RUNNER_TEMP}/dpm-${DPM_VERSION}-linux-amd64.tar.gz"
curl -sSfL \
  "https://github.com/digital-asset/dpm/releases/download/${DPM_VERSION}/dpm-${DPM_VERSION}-linux-amd64.tar.gz" \
  -o "$tar"
echo "${DPM_LINUX_SHA256}  ${tar}" | sha256sum --check --strict -
bindir="${RUNNER_TEMP}/dpm-bin"
mkdir -p "$bindir"
tar -xzf "$tar" -C "$bindir" dpm
chmod 0755 "$bindir/dpm"
echo "$bindir" >> "$GITHUB_PATH"
dpm --version
```

### Step 5 — Anonymous install + smoke test (linux/amd64)

Install the component from the public registry using the direct-ref form (no project
file or sdk-version required):

```sh
dpm install package "oci://ghcr.io/${NS}:${VERSION}"
dpm localnet --help    # success ⇒ linux/amd64 binary resolved, downloaded, registered, executable
```

### Step 6 — Cleanup (always runs)

```yaml
- name: Cleanup
  if: always()
  run: rm -f manifest.json
```

---

## 7. Conventions to follow

Match the style of `e2e.yml` and `integration.yml`:

- **SHA-pin every third-party action** with a `# owner/action@vX` comment above it.
  This workflow needs only `actions/checkout` (optional — checkout is not strictly
  required since Step 5 installs globally, not into the workspace). Aim for **zero**
  marketplace actions and do everything in `run:` blocks to minimize supply-chain
  surface.
- `set -euo pipefail` in every multi-line `run:` block.
- Header comment block: purpose, triggers, Linux-only note, cron offset rationale.
- No `packages: write` — this is a read-only detective check.

---

## 8. Validation before relying on the cron

1. Open a PR, trigger via `workflow_dispatch` with default (`latest`) — confirm green.
2. Trigger with a pinned known-good tag (`0.10.1`) — confirm green.
3. **Negative test:** dispatch with `version: 0.0.0-nope` — Step 2 must fail with a
   clear `::error::` message. This proves the guard actually works.
4. Merge; Monday cron takes over.

---

## 9. Maintenance notes (include as comments in the workflow)

- `DPM_VERSION` / `DPM_LINUX_SHA256` are duplicated from `release.yml`.
  Add a cross-reference comment in **both** files: `# Keep in sync with verify-public-oci.yml`
  and `# Keep in sync with release.yml`. Bump them together.
- The platform list in Step 3 mirrors `RELEASE_TARGETS` in `release.yml` — keep in sync.
- Namespace hard-coded to `ghcr.io/bitdynamics-ab/canton-devkit`. Update if the org/repo moves.

---

## 10. Alerting

GitHub's default failed-scheduled-run email notifications are sufficient.
No webhook or additional secrets required.

---

## 11. Audit / compliance note

This is a **read-only detective control** monitoring intentional public exposure of
the package — appropriate ISO 27001 / Vanta evidence that public access is verified
on a recurring basis. It introduces **no credentials** and **no write scopes**.
Document alongside the "package made public" change-management entry from the
Option-A rollout.
