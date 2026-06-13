package main

import (
	"os"
	"runtime/debug"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
)

// version, commit, and channel are injected at release-build time via
// -ldflags (-X main.version=… -X main.commit=… -X main.channel=stable|nightly).
// They default to "dev"/"" for a local `go build`; commit falls back to the
// VCS revision embedded by the Go toolchain when not set via ldflags.
var (
	version = "dev"
	commit  = ""
	channel = "dev"
)

func main() {
	telemetry.SetChannel(channel)
	app := cli.New(os.Stdout, os.Stderr, version, resolveCommit()).WithTelemetry()
	os.Exit(app.Run(os.Args[1:]))
}

// resolveCommit returns the ldflags-injected commit when present, otherwise
// the short VCS revision the Go toolchain embeds for `go build`/`go install`.
func resolveCommit() string {
	if commit != "" {
		return commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
