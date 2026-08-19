# Canton Builders Office Hours: canton-devkit Q&A speaker notes

Use these as spoken answers, not a word-for-word script.

## Core questions

**1. What does canton-devkit add beyond cn-quickstart?**

They start from the same Splice LocalNet, but they optimize for different things. [cn-quickstart](<https://github.com/digital-asset/cn-quickstart>) is an application-provider starter project: Daml models, a Spring Boot backend, a React front end, a Gradle build, and a bundled LocalNet with an observability stack, all driven by `make start`. It is the right place to begin if you want a working reference application.

canton-devkit is not an application. It fetches the bare Splice LocalNet definition directly from `canton-network/splice` — the `cluster/compose/localnet/` subtree — and treats the network itself as the product: named instances, host preflight, readiness waiting, exported endpoints and JWTs, ledger and token inspection, snapshot and restore. Application code is deliberately out of scope.

So the honest comparison is: if you want an example app to learn from, use cn-quickstart. If you want to drive a LocalNet from a CLI or CI job and inspect what is on the ledger, that is the gap DevKit fills.

**2. Walk us through what we are looking at on screen.**

We are starting with the operational layer. `localnet doctor` checks the Docker CLI and daemon, Compose v2, Docker memory, free disk, host ports, platform support, and Web UI reachability. `localnet up demo` then creates a named LocalNet from a pinned Splice definition and waits until it is ready. Once it is running, `status` shows the services and endpoints, `env` exports what an application or test suite needs, and then we can upload a DAR, inspect contracts and transactions, or run token flows. The important point is that the network is a named, inspectable test environment rather than a collection of manual Compose steps.

**3. What LocalNet pain point motivated this?**

The recurring problem was not writing the application logic. It was getting a realistic multi-party LocalNet into a known-good state: choosing a compatible version, managing Compose profiles and ports, collecting role-specific endpoints and credentials, and knowing when the stack was actually ready. canton-devkit turns that setup into a reproducible workflow with preflight checks, readiness waiting, and explicit instance state.

**4. How long does it take from zero to a running LocalNet?**

I would not promise one universal number. A first run has to fetch the required LocalNet material and Docker images, and Splice onboarding can take several minutes depending on the machine and connection. A warm run is faster. The useful contract is that `localnet up` waits for readiness and either returns a usable environment or fails with diagnostics, rather than making developers guess when it is safe to start testing.

**5. What tests and workflows does it make easier?**

It is most useful for integration and end-to-end work that needs a real multi-party Canton environment. That includes uploading and iterating on DARs, wiring tests to the correct participant endpoints, exercising app-provider and app-user flows, watching contracts and transactions, testing token workflows, taking snapshots for repeatable scenarios, and running isolated named instances in parallel.

**6. Is it open source, and can the community contribute?**

Yes. canton-devkit is Apache-2.0 licensed and developed in the open on GitHub. The repository includes installation, operations, CI, and contribution documentation. For a non-trivial feature, the best starting point is an issue so the workflow and CLI/Web UI behavior can be agreed before a pull request.

**7. What is on the roadmap?**

I do not want to give a date-based roadmap that I cannot stand behind. The near-term direction is to keep supported LocalNet releases current, make local and CI runs more reproducible and easier to diagnose, and improve the workflows developers repeat most often around testing and ledger inspection. We will prioritize that work from real developer feedback and contributions.

**8. Where should developers install it and get started?**

Start with the GitHub repository or the documentation site. If you already use DPM, install it as a DPM component; otherwise use a standalone release, Homebrew, or APT where appropriate. Then run `canton-devkit localnet doctor`, followed by `canton-devkit localnet up demo`. The documentation explains the available installation paths and the requirements.

## Technical follow-ups

**9. How does canton-devkit handle compatibility as Canton releases new SDK versions?**

The SDK version and the LocalNet version are related but separate concerns. For the LocalNet side, DevKit maintains a curated catalogue of tested Splice releases. Each entry is pinned to an upstream commit and verified content, with the appropriate version adapter and resource thresholds. `localnet versions` shows what is supported. Developers can explicitly opt into an uncurated upstream tag for evaluation, but the default path remains the tested catalogue.

**10. What does debugging look like when a test breaks?**

Start from the outside in. Run `localnet doctor` for host issues, `localnet status` for the instance and its endpoints, and `localnet logs` for service-level failures. If the application test reaches the ledger, use the exported environment values rather than hard-coded ports, then inspect contracts, transactions, and deployed DARs through the DevKit commands or Web UI. That gives you a short path from “the test failed” to “the host, service, endpoint, credential, or ledger state is the problem.”

**11. Can teams run it in CI?**

Yes. The intended CI flow is to pin both the DevKit and Splice versions, run `localnet doctor`, start a named instance with `localnet up --name ci --version <tag>`, export its endpoints into the job environment with `localnet env --name ci --format github-env`, run the application tests, and finish with `localnet remove --name ci --force` in an `if: always()` cleanup step. `up` blocks until the stack is healthy and returns a non-zero exit code on timeout, so the test job does not need arbitrary sleep calls. The repository includes complete GitHub Actions and GitLab CI examples under `examples/ci/`.

**12. What machine specification is needed?**

For a single LocalNet on current Splice, plan for Docker with Compose v2, about 8 GB of Docker memory and about 20 GB of free disk. 12 GB of Docker memory is the recommended target, especially with observability enabled or more than one instance running. The enforced gate is slightly lower than the recommendation: `doctor` and `up` fail below 8 GB of Docker memory and below 10 GB of free disk, and warn between 8 and 12 GB. Both surfaces read the same thresholds, and the memory floor is per Splice version, so `doctor` never passes a host where `up` would fail.

## Live demo preparation checklist

The screen share has one hard constraint: a cold `localnet up` fetches the Splice
subtree and pulls Docker images, and Splice onboarding takes minutes. Never
demonstrate that live from cold. Prepare two instances — one already populated
that carries the demo, one empty name reserved so `up` can be shown warm.

### Machine and versions

- [ ] Pin the Splice version you will name on the call and use it everywhere below. `canton-devkit localnet versions` prints the curated catalogue; `latest` currently resolves to `0.6.12`.
- [ ] Build or install the exact DevKit binary you will demo, and check `canton-devkit version` matches what you will tell people to install.
- [ ] Raise Docker memory to at least 12 GB. Two instances at once needs more; if the machine cannot hold two, drop the warm `up` demo rather than the populated instance.
- [ ] Free at least 20 GB of disk. Splice images plus two instances' volumes fill a laptop quickly.
- [ ] Run `canton-devkit localnet doctor` and fix every FAIL and WARN. A clean `doctor` is the first thing on screen, so it must be genuinely green — do not rehearse around a warning.

### Warm the caches (do this the day before)

- [ ] Run one full `canton-devkit localnet up warmup --version <tag>` to fetch the Splice subtree and pull every image, then `canton-devkit localnet remove warmup --force`. The subtree cache and Docker image layers survive the removal, so the next `up` on the same version skips both downloads.
- [ ] Confirm the warm path is actually fast by timing a second `up` on a throwaway name before you rely on it live.

### Build the populated instance

This is the instance the demo actually runs on. Bring it up early and leave it running.

- [ ] `canton-devkit localnet up demo --version <tag>` and wait for it to report healthy.
- [ ] `canton-devkit localnet status demo` — confirm every service is up and note the endpoints you will point at.
- [ ] `eval "$(canton-devkit localnet env demo)"` in the terminal you will use, so the app-wiring question has something concrete behind it.

### Generate ledger data

An empty ledger is the worst thing to show. Explorer, the balance matrix, and the
activity feed are all convincing only with real content in them.

- [ ] Seed a transferable token in one step: `canton-devkit localnet token demo --instance demo --symbol DEMO --supply 1000000`. On Splice 0.6.11+ this allocates an issuer party, creates a V2 instrument on-ledger, and mints the supply to a separate holder party, so there is a real balance to move.
- [ ] Create named parties so the screen reads in English rather than in party IDs: `canton-devkit localnet token party new alice --instance demo` and the same for `bob`. Aliases resolve in every token command and show up in the balance matrix and activity feed.
- [ ] Fund them: `canton-devkit localnet token faucet alice 5000 --instance demo --instrument DEMO`, and again for `bob`.
- [ ] Run several transfers so history is not a single row: `canton-devkit localnet token transfer --instance demo --instrument DEMO --from alice --to bob --amount 250 --auto-accept`. Vary the amounts and directions, and add `--reason` on a couple so the TransferInstruction detail has something human in it.
- [ ] Leave exactly one transfer pending — same command without `--auto-accept` — so `token transfers` has a live offer to accept on camera. Accepting a real pending transfer is a better moment than creating one from nothing.
- [ ] Upload a DAR so the contract list contains templates the audience has not seen from Splice itself. `e2e-tests/daml-test-contracts/` is a minimal Daml project; build and upload it with `canton-devkit localnet dar build-upload --instance demo`, or upload a prebuilt `.dar` with `dar upload`.
- [ ] Exercise a choice or two on the uploaded templates so `contracts ls` and `tx ls` show non-Splice activity.
- [ ] Verify the result is worth showing: `token balances --instance demo`, `token activity --instance demo --instrument DEMO`, `contracts ls --name demo`, `tx ls --name demo`. Note the selector differs by command family — the token verbs take `--instance` because `--name` there means the instrument, while `contracts` and `tx` take `--name`. Get this wrong on camera and it looks like the tool is inconsistent. If any view is thin, generate more before the call, not during it.

### Snapshot the prepared state

- [ ] `canton-devkit localnet snapshot demo --to ~/demo-ready.tgz` once the data looks right. This is the recovery path: if anything is broken mid-demo, `localnet restore demo --from ~/demo-ready.tgz --force` is faster than rebuilding, and the snapshot itself is a feature worth showing.
- [ ] Test the restore once, before the call, so you know the archive is good and how long it takes.

### Optional surfaces

- [ ] If Grafana is part of the story, run `canton-devkit localnet observability enable demo` well in advance and confirm the dashboards have collected enough data to look alive.
- [ ] Open the Web UI with `canton-devkit localnet ui` (loopback, port 7777) and pre-load the Explorer tab against the `demo` instance with `?instance=demo` in the URL, so the instance picker is already correct on screen.

### Right before going live

- [ ] `canton-devkit localnet status demo` once more — containers can go unhealthy while the laptop sleeps.
- [ ] Confirm nothing is holding the ports for the second instance name you plan to bring up live.
- [ ] Terminal at a readable font size, wide window, clean scrollback, prompt short enough that command lines do not wrap.
- [ ] Browser tabs pre-opened: the Web UI Explorer on `demo`, the GitHub repository, and the documentation site.
- [ ] Have the install command and the repository URL in the clipboard or pinned in chat — people will ask for both.

### Demo order

1. `localnet doctor` — fast, live, and it sets up the "the tool checks your host" claim.
2. `localnet up <second-name>` — start it live on the warmed cache, then leave it running in a background pane while you talk. Cut back to it later so the wait is filled rather than watched.
3. Switch to the prepared `demo` instance: `status`, then `env`.
4. Web UI Explorer — contracts, transactions, timeline. This is the strongest visual, so give it time.
5. Token flow — show the balance matrix, accept the pending transfer you left, then show the activity feed updating.
6. `snapshot` and `restore`, if the timing holds.

### Fallbacks

- [ ] Record a short screen capture of a cold `up` finishing. If the live `up` stalls on the day, play the recording instead of waiting on camera.
- [ ] Keep `~/demo-ready.tgz` and the exact `restore` command one keystroke away.
- [ ] Have `localnet logs demo` ready. If something does fail live, walking through the diagnostic path is a better recovery than an apology — that is exactly the claim in Q10.

## Backup questions

**13. What common setup mistake does canton-devkit help avoid?**

Treating LocalNet as just a Compose file. A usable environment also needs compatible versions, enough Docker resources, available ports, role-specific credentials and endpoints, and a readiness signal. Hard-coding those details tends to create flaky tests. DevKit makes them explicit through `doctor`, `up`, `status`, and `env`.

**14. How did you discover Canton, and why build tooling for it?**

Replace the bracketed text with your real story:

“I first encountered Canton through \[your actual context\]. Once I began trying to run realistic end-to-end workflows locally, I kept repeating the same setup and reset work. That showed me the opportunity: developers needed a reproducible LocalNet loop, not another example application. canton-devkit grew out of that practical need.”

**15. What should every Canton developer know about LocalNet?**

LocalNet is a development environment, not a miniature production deployment. Its value is that it lets you test realistic topology and behavior safely: separate roles, separate parties, live endpoints, credentials, ledger state, and failure recovery. Treat it as a way to develop production-minded workflows locally, rather than as a one-party mock.

**16. Are there other tooling gaps you want to solve?**

The remaining gaps are mostly in the last mile between a local network and repeatable application tests: version-aware setup, simple CI wiring, clear ledger and service visibility, and ergonomic party and token workflows. DevKit already covers parts of that path. The most useful next work should come from the workflows developers repeatedly find slow or fragile.
