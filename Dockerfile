# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build

ARG VERSION=dev

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/canton-devkit ./cmd/canton-devkit

FROM alpine:3.20

RUN addgroup -S canton && adduser -S canton -G canton
COPY --from=build /out/canton-devkit /usr/local/bin/canton-devkit

USER canton:canton
ENTRYPOINT ["canton-devkit"]
