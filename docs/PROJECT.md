# Canton Devkit - Project Tracking

This file is the main index for the project. It contains project context and a
table of all tracked epics and tasks. Read this file at the start of a session.

For detailed progress, notes, and history, see the individual epic/task files
linked in the table below.

## Context

| Key | Value |
|-----|-------|
| Repository | `git@github.com:bitdynamics-ab/canton-devkit.git` |
| Task files | `docs/tasks/` |

## Current Status

Task 001 completed the initial standard-library Go CLI boilerplate. Task 004
(BIT-18) completed the CI baseline: cross-platform build/test matrix
(Linux/macOS/Windows), migration to `golangci-lint-action`, README layout
documentation, and a contract test locking the DPM invocation interface.

Task 002 tracks a future Cobra migration decision. Task 003 captures the DPM
component quick-test proof-of-concept, now superseded for full release
engineering by Linear issue BIT-19.

## Tasks & Epics

| ID | Type | Title | Status | File |
|----|------|-------|--------|------|
| 001 | Task | Golang CLI Boilerplate | **✅ Complete** | [001-TASK-golang-cli-boilerplate.md](tasks/001-TASK-golang-cli-boilerplate.md) |
| 002 | Task | Consider Cobra CLI Framework | ⏳ Not started | [002-TASK-consider-cobra-cli-framework.md](tasks/002-TASK-consider-cobra-cli-framework.md) |
| 003 | Task | DPM Component Quick Test | ⏳ Not started | [003-TASK-dpm-component-quick-test.md](tasks/003-TASK-dpm-component-quick-test.md) |
| 004 | Task | Bootstrap Go Project and CI Baseline (BIT-18) | **✅ Complete** | [004-TASK-bootstrap-go-project-ci-baseline.md](tasks/004-TASK-bootstrap-go-project-ci-baseline.md) |

## How to Add a New Task or Epic

1. Find the next available ID by checking the table above.
2. Create a new file: `docs/tasks/{ID}-{EPIC|TASK}-{kebab-case-title}.md`
3. Use the epic/task file template.
4. Add a row to the **Tasks & Epics** table above.
