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

Task 001 completed the initial standard-library Go CLI boilerplate. A future
task tracks whether the project should migrate to Cobra later.

## Tasks & Epics

| ID | Type | Title | Status | File |
|----|------|-------|--------|------|
| 001 | Task | Golang CLI Boilerplate | **✅ Complete** | [001-TASK-golang-cli-boilerplate.md](tasks/001-TASK-golang-cli-boilerplate.md) |
| 002 | Task | Consider Cobra CLI Framework | ⏳ Not started | [002-TASK-consider-cobra-cli-framework.md](tasks/002-TASK-consider-cobra-cli-framework.md) |

## How to Add a New Task or Epic

1. Find the next available ID by checking the table above.
2. Create a new file: `docs/tasks/{ID}-{EPIC|TASK}-{kebab-case-title}.md`
3. Use the epic/task file template.
4. Add a row to the **Tasks & Epics** table above.
