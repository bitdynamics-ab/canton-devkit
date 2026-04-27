package main

import (
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli"
)

var version = "dev"

func main() {
	app := cli.New(os.Stdout, os.Stderr, version)
	os.Exit(app.Run(os.Args[1:]))
}
