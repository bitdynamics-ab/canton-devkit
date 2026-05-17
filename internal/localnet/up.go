package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/config"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

const (
	ExitSuccess        = 0
	ExitUserError      = 1
	ExitPreflightFail  = 2
	ExitTimeout        = 3
	ExitRuntimeFailure = 4
)

type UpOptions struct {
	Name    string
	Version string
}

func ParseUpArgs(args []string) (*UpOptions, error) {
	opts := &UpOptions{
		Version: config.DefaultVersion,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--name requires a value")
			}
			i++
			opts.Name = args[i]
		case "--version":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--version requires a value")
			}
			i++
			opts.Version = args[i]
		default:
			if len(args[i]) > 2 && args[i][:2] == "--" {
				return nil, fmt.Errorf("unknown flag %q", args[i])
			}
			return nil, fmt.Errorf("unexpected argument %q", args[i])
		}
	}

	if opts.Name == "" {
		return nil, fmt.Errorf("--name is required")
	}

	if !isValidName(opts.Name) {
		return nil, fmt.Errorf("--name must be alphanumeric with hyphens (got %q)", opts.Name)
	}

	return opts, nil
}

func RunUp(ctx context.Context, out io.Writer, errw io.Writer, opts *UpOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	_, _ = fmt.Fprintf(out, "Starting Canton LocalNet %q (version %s)...\n", opts.Name, opts.Version)

	dataDir := config.DataDir(opts.Name)

	_, _ = fmt.Fprintf(out, "Running preflight checks...\n")
	report := docker.RunPreflight(ctx, docker.Options{
		RequiredPorts:  []int{5011, 5012, 5432},
		DataDir:        dataDir,
		MinDiskBytes:   10 * 1024 * 1024 * 1024, // 10 GB
		MinMemoryBytes: 4 * 1024 * 1024 * 1024,  // 4 GB
	})
	report.Write(out)
	if !report.OK() {
		_, _ = fmt.Fprintf(errw, "\nPreflight failed. Address the items above and re-run.\n")
		return ExitPreflightFail
	}

	cfg := &config.LocalNetConfig{
		Name:    opts.Name,
		Version: opts.Version,
		DataDir: dataDir,
	}

	_, _ = fmt.Fprintf(out, "Generating configs and identities in %s...\n", dataDir)
	if err := config.Generate(cfg); err != nil {
		_, _ = fmt.Fprintf(errw, "Config generation failed: %s\n", err)
		return ExitRuntimeFailure
	}

	runner := &docker.ComposeRunner{
		ProjectName: "canton-" + opts.Name,
		ComposeFile: docker.ComposeFilePath(dataDir),
		WorkDir:     dataDir,
		LogWriter:   out,
	}

	_, _ = fmt.Fprintf(out, "Starting services...\n")
	if err := runner.Up(ctx); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintf(errw, "Interrupted while starting services\n")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "Failed to start services: %s\n", err)
		return ExitRuntimeFailure
	}

	_, _ = fmt.Fprintf(out, "Waiting for services to become healthy...\n")
	if err := runner.WaitForHealthy(ctx); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintf(errw, "Timed out waiting for services\n")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "Services failed health check: %s\n", err)
		return ExitRuntimeFailure
	}

	_, _ = fmt.Fprintf(out, "\nCanton LocalNet %q is ready.\n\n", opts.Name)

	_, _ = fmt.Fprintf(out, "Endpoints:\n")
	_, _ = fmt.Fprintf(out, "  Ledger API:    grpc://localhost:5011\n")
	_, _ = fmt.Fprintf(out, "  Admin API:     grpc://localhost:5012\n")
	_, _ = fmt.Fprintf(out, "  PostgreSQL:    postgresql://canton:canton@localhost:5432\n")
	_, _ = fmt.Fprintf(out, "\nCredentials:\n")
	_, _ = fmt.Fprintf(out, "  Identity:      %s/identity.txt\n", dataDir)
	_, _ = fmt.Fprintf(out, "  Keys:          %s/keys/\n", dataDir)
	_, _ = fmt.Fprintf(out, "  Canton config: %s/canton/canton.conf\n", dataDir)

	return ExitSuccess
}

func isValidName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	return true
}
