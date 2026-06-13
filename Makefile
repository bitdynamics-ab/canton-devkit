BINARY_NAME := canton-devkit
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build clean docker-build lint test frontend frontend-install frontend-test ui

# frontend-install: ensure the Vite project has its node_modules.
# Idempotent: a no-op when the lockfile + node_modules are in sync.
# Pulled out so `make frontend` can be the build-only target for
# CI runners that pre-cache deps.
frontend-install:
	cd frontend && npm ci --silent

# frontend: produce the production Vite bundle into
# internal/ui/dist/. The Go binary's //go:embed picks it up at
# `go build` time, so the canonical release flow is:
#
#   make frontend && make build
#
# Without `make frontend`, `go build` embeds the dev placeholder
# (internal/ui/dist/index.html with DEVKIT_FRONTEND_PLACEHOLDER)
# and `dpm localnet ui` prints a stderr warning at startup. See
# internal/ui/assets.go IsPlaceholderBundle.
frontend: frontend-install
	cd frontend && npm run build

# frontend-test: run the Vitest suite (jsdom + RTL). Fast — no
# Vite bundle, no Go embed step. Wire into CI alongside `make test`
# once the frontend lands on main.
frontend-test: frontend-install
	cd frontend && npm test

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/canton-devkit

# ui: convenience target for `dpm localnet ui` development. Builds
# the Vite bundle THEN the Go binary, so the embedded //go:embed
# always reflects current frontend source. Use this instead of
# plain `make build` when you've touched frontend/ and want to
# poke the running UI in a browser.
#
# Plain `make build` is preserved for Go-only contributors who
# don't have node installed — the placeholder warning at startup
# is the intentional signal that they need `make frontend` first.
ui: frontend build

test:
	go test ./...

lint:
	golangci-lint run

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(BINARY_NAME):$(VERSION) .

clean:
	rm -rf bin dist
