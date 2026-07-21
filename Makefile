BINARY_NAME := canton-devkit
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build clean docker-build lint test frontend frontend-install frontend-test ui e2e-dpm analyzer-image analyzer-push

# daml-analyzer image (see build/daml-analyzer/). Keep DAML_ANALYZER_IMAGE in
# sync with analyzer.DefaultImage.
DAML_ANALYZER_IMAGE ?= ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2

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

# e2e-dpm: run the `dpm localnet` bats e2e suite. bats-core and its
# helper libs are vendored as git submodules under e2e-tests/; this
# target initializes them on demand so a fresh checkout just works.
# Requires `dpm` on PATH (the suite skips gracefully otherwise).
e2e-dpm:
	@if [ ! -x e2e-tests/bats/bin/bats ]; then \
		git submodule update --init --recursive e2e-tests; \
	fi
	BATS_LIB_PATH="$(CURDIR)/e2e-tests/test_helper" \
		e2e-tests/bats/bin/bats e2e-tests/

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(BINARY_NAME):$(VERSION) .

analyzer-image:
	docker build -t $(DAML_ANALYZER_IMAGE) build/daml-analyzer

analyzer-push: analyzer-image
	docker push $(DAML_ANALYZER_IMAGE)

clean:
	rm -rf bin dist
