package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// ListOptions captures `localnet list` flags.
type ListOptions struct {
	All    bool
	Format string // "text" (default) or "json"
}

// listRow is one row of the output table, enriched with live status.
type listRow struct {
	Name          string `json:"name"`
	SpliceVersion string `json:"splice_version"`
	Status        string `json:"status"` // from index.json
	Live          string `json:"live"`   // "yes"/"no" — does docker actually have containers
	CreatedAt     string `json:"created_at"`
}

// RunList prints every known instance.
func RunList(ctx context.Context, out io.Writer, errw io.Writer, opts *ListOptions) int {
	idx, err := registry.ReadIndex()
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read registry index: %s\n", err)
		return ExitRuntimeFailure
	}

	if len(idx.Entries) == 0 {
		if opts.Format == "json" {
			_, _ = fmt.Fprintln(out, "[]")
			return ExitSuccess
		}
		_, _ = fmt.Fprintln(out, "No LocalNet instances registered.")
		_, _ = fmt.Fprintln(out, "Start one with: canton-devkit localnet up --name <name>")
		return ExitSuccess
	}

	rows := make([]listRow, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if !opts.All && e.Status != registry.StatusRunning && e.Status != registry.StatusCreating {
			continue
		}
		row := listRow{
			Name:          e.Name,
			SpliceVersion: e.SpliceVersion,
			Status:        string(e.Status),
			CreatedAt:     e.CreatedAt,
		}
		row.Live = liveContainerCheck(ctx, "canton-"+e.Name)
		rows = append(rows, row)
	}

	if opts.Format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return ExitSuccess
	}

	// Text table
	_, _ = fmt.Fprintf(out, "%-20s %-10s %-10s %-6s %s\n", "NAME", "SPLICE", "STATUS", "LIVE", "CREATED")
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%-20s %-10s %-10s %-6s %s\n",
			r.Name, r.SpliceVersion, r.Status, r.Live, r.CreatedAt)
	}
	return ExitSuccess
}

// liveContainerCheck returns "yes" if at least one running container
// carries the compose project label for the named instance, "no"
// otherwise. Fails-soft: "?" on any docker error so the table still prints.
func liveContainerCheck(ctx context.Context, project string) string {
	cmd := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.ID}}")
	out, err := cmd.Output()
	if err != nil {
		return "?"
	}
	if strings.TrimSpace(string(out)) == "" {
		return "no"
	}
	return "yes"
}
