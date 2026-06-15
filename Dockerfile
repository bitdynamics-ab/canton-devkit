# syntax=docker/dockerfile:1

# Stage 1: build the Vite production bundle. `vite build` writes to
# ../internal/ui/dist (frontend/vite.config.ts outDir), so we lay the
# frontend out at /src/frontend and let it emit into /src/internal/ui/dist
# — the exact path the Go stage's //go:embed all:dist expects. Without
# this stage the binary would embed the dev placeholder
# (DEVKIT_FRONTEND_PLACEHOLDER) and `dpm localnet ui` would serve a
# broken UI, the same failure `make build` (vs `make ui`) warns about.
# Node pinned to frontend/.nvmrc (20).
FROM node:20-alpine AS frontend

WORKDIR /src/frontend
# Copy manifests first so `npm ci` is cached unless deps change.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build   # tsc --noEmit && vite build -> /src/internal/ui/dist

# Stage 2: build the Go binary. golang:1.26 matches go.mod's `go 1.26`
# (the official images set GOTOOLCHAIN=local, so a 1.22 base refused to
# build at all). go.sum is required for module verification.
FROM golang:1.26-alpine AS build

ARG VERSION=dev
ARG COMMIT=

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# The top-level assets package (github.com/bitdynamics-ab/canton-devkit/assets)
# embeds the Splice compose + Grafana files and is imported by
# internal/localnet; it must be present or the build fails with
# "no required module provides package .../assets".
COPY assets ./assets
# Overlay the real Vite bundle produced in stage 1 on top of the
# placeholder dist/ that ships in the source tree, so //go:embed picks
# up the compiled UI.
COPY --from=frontend /src/internal/ui/dist ./internal/ui/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/canton-devkit ./cmd/canton-devkit

FROM alpine:3.20

RUN addgroup -S canton && adduser -S canton -G canton
COPY --from=build /out/canton-devkit /usr/local/bin/canton-devkit

USER canton:canton
ENTRYPOINT ["canton-devkit"]
