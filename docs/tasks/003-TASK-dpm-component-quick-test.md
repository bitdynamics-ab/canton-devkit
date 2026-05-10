# 003 - Quick Test: Run canton-devkit Binary as a DPM Component

**Type:** Task
**Status:** ⏳ Not started
**Created:** 2026-05-10
**Updated:** 2026-05-10

## Goal

Validate that the existing `canton-devkit` Go binary can be packaged and
invoked as a native DPM component, exposing a single `localnet` top-level
command in DPM. This is a proof-of-concept ahead of fully wiring the
component into our release CI.

The proposal commits to dual distribution (DPM component as primary,
standalone binary as additional). Before that becomes a Milestone 1
deliverable, we want a hands-on quick test confirming:

1. A `component.yaml` referencing our binary actually loads in DPM.
2. `dpm localnet ...` invocations end up calling our binary with the
   expected arguments.
3. The single-top-level-command pattern (everything nested under
   `localnet`) works as designed and avoids conflicts with DPM builtins.

## Background

DPM uses a component system rather than a traditional plugin API. Components
are OCI artifacts containing one or more binaries plus a `component.yaml`
manifest. They are published with `dpm publish component` and installed by
referencing them from a project's `daml.yaml` or `multi-package.yaml`.

References:

- DPM repository: https://github.com/digital-asset/dpm
- DPM components docs: `docs/src/public/dpm-components.rst` in the dpm repo
- Component manifest format: `apiVersion: digitalasset.com/v1`, `kind: Component`

Key constraints:

- Command names registered by a component must not collide with DPM
  builtins (e.g. `versions`, `install`, `resolve`, `publish`, `repo`,
  `component`, `login`, `bootstrap`, `tags`, `update`) or with commands
  from other components.
- We deliberately register only a single top-level command (`localnet`)
  to keep the DPM surface minimal and conflict-free. All DevKit
  subcommands (`up`, `down`, `dar ...`, `contracts ...`, `tx ...`,
  `token ...`, `metrics`, `doctor`, etc.) live under `localnet` in our
  own binary.

## Scope (Quick Test Only)

This task is a quick validation, not a full release-engineering setup.
Out of scope for now:

- Multi-platform OCI publishing.
- Real OCI registry hosting.
- Release CI integration.
- Versioning strategy and lockfile interaction.

In scope:

- Local DPM install on the developer machine.
- A minimal `component.yaml` pointing at a locally built `canton-devkit`
  binary.
- Either local component testing via `dpm component run` or a local OCI
  registry push, whichever is simpler to validate end-to-end.
- Verifying that `dpm localnet --help`, `dpm localnet up --help`, and at
  least one real subcommand (e.g. `dpm localnet doctor` if available, or
  a stubbed `dpm localnet ping`) reach our Go binary correctly.

## Proposed `component.yaml`

The component registers a single native command named `localnet` that
forwards everything to our binary. All DevKit subcommands are handled by
the binary's own argument parser, not by DPM.

```yaml
apiVersion: digitalasset.com/v1
kind: Component
spec:
  commands:
    - name: localnet
      path: bin/canton-devkit
      desc: "Canton DevKit: manage LocalNet, DARs, contracts, tokens, and observability"
      exec-args: ["localnet"]
```

Notes on this manifest:

- `path` is relative to the component root and must point at an
  executable file (not a directory).
- `exec-args: ["localnet"]` ensures the binary always sees `localnet` as
  its first argument, so the binary's CLI parser dispatches into the
  `localnet` subtree regardless of how DPM invoked it. Any user-supplied
  args (e.g. `up --name foo`) are appended after `exec-args`.
- DPM injects environment variables such as `DPM_PATH_INJECTED_ENV_VAR`,
  `DPM_RESOLUTION_FILE`, `DPM_SDK_VERSION`, and `DAML_PACKAGE` into the
  command's environment. Our binary may read these later but does not
  need to for this quick test.
- For a multi-platform release, this manifest is duplicated per platform
  directory (e.g. `darwin-arm64/`, `linux-amd64/`) and `dpm publish
  component` is given multiple `--platform <os>/<arch>=<dir>` flags.
  For the quick test, a single host-platform directory is enough.

## Local Layout for the Quick Test

```
quick-test/
├── component/
│   ├── component.yaml
│   ├── LICENSE              # required by `dpm publish component`
│   └── bin/
│       └── canton-devkit    # locally built binary (host platform)
└── consumer/
    └── daml.yaml            # references the component for `dpm install package`
```

Example consumer `daml.yaml` snippet (registry URL depends on whether we
push to a local OCI registry or use `dpm component run`):

```yaml
sdk-version: <some-installed-version>
name: canton-devkit-quick-test
version: 0.0.1
source: .
dependencies: []
components:
  - oci://localhost:5000/canton-devkit:0.0.1-quick-test
```

## Progress

- [ ] Build a host-platform `canton-devkit` binary into `quick-test/component/bin/canton-devkit`.
- [ ] Write `quick-test/component/component.yaml` per the snippet above.
- [ ] Add a `LICENSE` file alongside `component.yaml` (`dpm publish component` requires it).
- [ ] Install DPM locally and confirm `dpm versions` works.
- [ ] Quick-iteration option: run `dpm component run` against the local component directory and confirm `dpm localnet --help` reaches our binary.
- [ ] Optional second pass: `dpm publish component oci://localhost:5000/canton-devkit:0.0.1-quick-test --platform <os>/<arch>=quick-test/component` against a local OCI registry, then `dpm install package` from the consumer directory and re-run `dpm localnet --help`.
- [ ] Verify argument forwarding: `dpm localnet up --name foo --version 0.0.1` should reach our binary as `canton-devkit localnet up --name foo --version 0.0.1` (or equivalent argv slice).
- [ ] Document any DPM error messages we hit (name conflicts, missing fields, manifest schema issues) so we can refine the real `component.yaml` later.

## Acceptance Criteria

- A single Markdown note (or appended section here) capturing:
  - The exact `component.yaml` that worked.
  - The exact `dpm` commands used to install/run it.
  - Confirmation that `dpm localnet <anything>` invokes our binary with
    the expected argv.
  - Any DPM-side surprises (env vars, path resolution, subcommand
    handling, conflicts).
- No code changes to `canton-devkit` are required for this task. If the
  test reveals that we need to change how the binary parses argv when
  invoked via DPM, that is captured as a follow-up task rather than
  fixed inline.

## Notes for the Implementing Agent

- Prefer `dpm component run` for the first iteration if available — it
  avoids needing a local registry and is the recommended development
  flow per the DPM docs.
- If `dpm component run` is not sufficient, set up a local OCI registry
  (e.g. `docker run -d -p 5000:5000 registry:2`) and use the `--insecure`
  flag in the relevant DPM config.
- DPM config typically lives at `${DPM_HOME}/dpm-config.yaml` (defaults
  to `~/.dpm/dpm-config.yaml`). The relevant fields are `registry` and
  `insecure` if pointing at a local registry.
- Do not invest in CI integration, multi-arch builds, or release
  automation in this task — those land in a later task once we know the
  manifest and command shape are correct.

## Next Steps

After this quick test confirms the integration model:

1. Open a follow-up task to wire DPM component packaging into the
   release CI (multi-platform builds, OCI publishing with checksums,
   versioning strategy).
2. Open a follow-up task to coordinate command naming with the DPM team
   if any conflicts appear.
3. Update the user-facing install docs to lead with the DPM component
   path.
