BINARY_NAME := canton-devkit
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build clean docker-build lint test

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/canton-devkit

test:
	go test ./...

lint:
	golangci-lint run

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY_NAME):$(VERSION) .

clean:
	rm -rf bin dist
