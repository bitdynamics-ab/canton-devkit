package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
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
	Ports         string `json:"ports"`
	StartedAgo    string `json:"started_ago"`
	CreatedAt     string `json:"created_at"`
}

// RunList prints every known instance.
func RunList(ctx context.Context, out io.Writer, errw io.Writer, opts *ListOptions) int {
	if opts == nil {
		opts = &ListOptions{Format: "text"}
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read registry index: %s\n", err)
		return ExitRuntimeFailure
	}

	if len(idx.Entries) == 0 {
		if opts.Format == "json" {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(types.ListResponse{
				SchemaVersion: types.SchemaVersion,
				Instances:     []types.InstanceSummary{},
			})
			return ExitSuccess
		}
		_, _ = fmt.Fprintln(out, "No LocalNet instances registered.")
		_, _ = fmt.Fprintln(out, "Start one with: canton-devkit localnet up <name>")
		return ExitSuccess
	}

	rows, resp, warning := collectListRows(ctx, idx, opts)

	if opts.Format == "json" {
		if warning != "" {
			resp.Warning = warning
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		return ExitSuccess
	}

	writeListText(out, rows, warning)
	return ExitSuccess
}

func collectListRows(ctx context.Context, idx *registry.Index, opts *ListOptions) ([]listRow, types.ListResponse, string) {
	rows := make([]listRow, 0, len(idx.Entries))
	resp := types.ListResponse{
		SchemaVersion: types.SchemaVersion,
		Instances:     make([]types.InstanceSummary, 0, len(idx.Entries)),
	}

	unreadable := make([]string, 0)
	for _, e := range idx.Entries {
		if !opts.All && e.Status != registry.StatusRunning && e.Status != registry.StatusCreating {
			continue
		}
		ports := "—"
		if s, err := registry.Read(e.Name); err == nil {
			ports = formatPortRange(s.Ports)
		} else {
			unreadable = append(unreadable, e.Name)
		}

		startedAgo := ""
		if e.Status == registry.StatusRunning {
			startedAgo = humanSince(e.CreatedAt)
		}
		row := listRow{
			Name:          e.Name,
			SpliceVersion: e.SpliceVersion,
			Status:        string(e.Status),
			Live:          liveContainerCheck(ctx, "canton-"+e.Name),
			Ports:         ports,
			StartedAgo:    startedAgo,
			CreatedAt:     e.CreatedAt,
		}
		rows = append(rows, row)
		resp.Instances = append(resp.Instances, types.InstanceSummary{
			Name:          e.Name,
			Status:        string(e.Status),
			SpliceVersion: e.SpliceVersion,
			Ports:         ports,
			StartedAgo:    startedAgo,
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	sort.Slice(resp.Instances, func(i, j int) bool { return resp.Instances[i].Name < resp.Instances[j].Name })

	warning := ""
	if len(unreadable) > 0 {
		warning = fmt.Sprintf("warning: %d instance(s) had unreadable state files: %v", len(unreadable), unreadable)
	}
	return rows, resp, warning
}

func writeListText(out io.Writer, rows []listRow, warning string) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No matching LocalNet instances.")
		_, _ = fmt.Fprintln(out, "Use --all to include stopped/failed instances.")
		return
	}

	tableRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		tableRows = append(tableRows, []string{
			term.Bold(r.Name),
			r.SpliceVersion,
			statusGlyph(r.Status),
			r.Live,
			term.Textc(r.Ports),
			term.Dimc(r.StartedAgo),
		})
	}
	_, _ = fmt.Fprintln(out, term.Table([]term.Column{
		{Label: "name"},
		{Label: "splice"},
		{Label: "status"},
		{Label: "live"},
		{Label: "ports"},
		{Label: "started"},
	}, tableRows))
	if warning != "" {
		_, _ = fmt.Fprintln(out, term.Warnc(warning))
	}
}

func statusGlyph(state string) string {
	switch state {
	case string(registry.StatusRunning):
		return term.Successc("● running")
	case string(registry.StatusCreating):
		return term.Brandc("◐ creating")
	case string(registry.StatusStopping):
		return term.Warnc("◐ stopping")
	case string(registry.StatusStopped):
		return term.Dimc("○ stopped")
	case string(registry.StatusFailed):
		return term.Errorc("⊗ failed")
	case string(registry.StatusPartial):
		return term.Warnc("◐ partial")
	default:
		return term.Dimc(state)
	}
}

var portRangeKeys = []string{
	"app_user_ui",
	"app_provider_ui",
	"sv_ui",
	"swagger_ui",
}

func formatPortRange(ports map[string]int) string {
	if len(ports) == 0 {
		return "—"
	}
	var lo, hi int
	first := true
	for _, k := range portRangeKeys {
		p, ok := ports[k]
		if !ok || p <= 0 {
			continue
		}
		if first || p < lo {
			lo = p
		}
		if first || p > hi {
			hi = p
		}
		first = false
	}
	if first {
		return "—"
	}
	if lo == hi {
		return fmt.Sprintf("%d", lo)
	}
	return fmt.Sprintf("%d–%d", lo, hi)
}

func humanSince(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh ago", h)
		}
		return fmt.Sprintf("%dh %dm ago", h, m)
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
