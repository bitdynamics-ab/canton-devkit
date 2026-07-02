BINARY_NAME := canton-devkit
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build clean docker-build lint test frontend frontend-install frontend-test ui

# frontend-install: sync frontend/node_modules with the lockfile.
# Separate target so CI runners with pre-cached deps can build only.
frontend-install:
	cd frontend && npm ci --silent

# frontend: build the production Vite bundle into internal/ui/dist/,
# which `go build` embeds via //go:embed. Canonical release flow:
# `make frontend && make build`. Without it the binary embeds the dev
# placeholder and `dpm localnet ui` warns at startup (see
# internal/ui/assets.go IsPlaceholderBundle).
frontend: frontend-install
	cd frontend && npm run build

# frontend-test: run the Vitest suite (jsdom + RTL); no Vite bundle
# or Go embed step needed.
frontend-test: frontend-install
	cd frontend && npm test

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/canton-devkit

# ui: build the Vite bundle then the Go binary so the embedded assets
# reflect current frontend source. Plain `make build` stays node-free
# for Go-only contributors — the placeholder warning at startup is the
# signal to run `make frontend` first.
ui: frontend build

test:
	go test ./...

lint:
	golangci-lint run

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(BINARY_NAME):$(VERSION) .

clean:
	rm -rf bin dist
