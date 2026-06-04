package main

import (
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
)

// version and channel are injected at release-build time via -ldflags
// (-X main.version=… -X main.channel=stable|nightly). channel defaults to
// "dev" for a local `go build`.
var (
	version = "dev"
	channel = "dev"
)

func main() {
	telemetry.SetChannel(channel)
	app := cli.New(os.Stdout, os.Stderr, version).WithTelemetry()
	os.Exit(app.Run(os.Args[1:]))
}
