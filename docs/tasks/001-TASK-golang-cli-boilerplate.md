# 001 - Golang CLI Boilerplate

**Type:** Task
**Status:** ✅ Complete
**Created:** 2026-04-27
**Updated:** 2026-04-27

## Goal

Create a Go CLI project boilerplate with automated build, release, linting, unit
test, and Docker image publishing support.

## Progress

- [x] Initialize Go CLI project structure
- [x] Add Dockerfile for building/running the CLI
- [x] Add GitHub Actions workflow for linting and unit tests
- [x] Add GitHub Actions workflow support for Docker image builds
- [x] Add GitHub Actions workflow support for macOS and Linux release artifacts
- [x] Verify linting and unit tests run successfully

## Notes

- Individual task files are stored under `docs/tasks/`.
- Repository remote: `git@github.com:bitdynamics-ab/canton-devkit.git`.
- Initial implementation will use only the Go standard library for CLI parsing.
- Host Go tooling was not installed locally, so verification was run through Docker.
- Verified with Docker-based `gofmt`, `go test ./...`, `go build ./cmd/canton-devkit`, `golangci-lint run`, `docker build`, and `docker run canton-devkit:dev version`.

## Next Steps

Use task 002 to reassess whether Cobra is warranted after real LocalNet command
workflows are implemented.
