# Canton Builders Office Hours: canton-devkit Q&A speaker notes

Use these as spoken answers, not a word-for-word script.

## Core questions

**1. What does canton-devkit add beyond cn-quickstart?**

They solve adjacent problems. [cn-quickstart](<https://github.com/digital-asset/cn-quickstart>) provides an application-provider starter project: application code, workflows, services, and front ends. canton-devkit operates the underlying LocalNet: it creates a named network from a tested Splice release, checks the host, waits for readiness, exposes endpoints and development credentials, and provides lifecycle and diagnostic commands. If you already use cn-quickstart, DevKit does not replace your app. It can make the LocalNet beneath it easier to run and test.

**2. Walk us through what we are looking at on screen.**

We are starting with the operational layer. `localnet doctor` checks Docker, Compose, memory, disk, ports, and platform support. `localnet up demo` then creates a named LocalNet from a pinned Splice definition and waits until it is ready. Once it is running, `status` shows the services and endpoints, `env` exports what an application or test suite needs, and then we can upload a DAR, inspect contracts and transactions, or run token flows. The important point is that the network is a named, inspectable test environment rather than a collection of manual Compose steps.

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

Yes. The intended CI flow is to pin both the DevKit and Splice versions, run `localnet doctor`, start a named instance with `localnet up`, export its endpoints into the job environment, run the application tests, and remove the instance in an always-run cleanup step. `up` waits for readiness, so the test job does not need arbitrary sleep calls. The repository includes GitHub Actions and GitLab CI examples.

**12. What machine specification is needed?**

For a single LocalNet, plan for Docker with Compose v2, about 8 GB of Docker memory and 20 GB of free disk. For a comfortable experience, especially with observability or more than one instance, 12 GB of Docker memory is the better target. `localnet doctor` checks the actual machine and reports a clear remediation when resources are insufficient.

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
