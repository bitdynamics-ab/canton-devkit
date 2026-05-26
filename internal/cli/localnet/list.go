package localnet

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// `localnet list` enumerates every instance the registry knows about.
// Backed by registry.ReadIndex (a small JSON file) — does NOT walk
// docker, so it runs fast and works when Docker is down. The
// trade-off: status reflects what the registry was last told, which
// can drift if a container was killed externally. Run `localnet
// refresh` to re-sync.
//
// This is the lightweight v1; PR #32 (BIT-146) has the richer view
// with port-range, age formatting, and partial-state-tolerance. When
// that lands, this file gets replaced wholesale. Until then, having
// SOME working `list` beats the "command not found" the user just
// hit.
func buildList() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Canton LocalNet instances in the registry",
		Long: "Walks ~/.canton-devkit/localnet/index.json and prints " +
			"one row per registered instance. Status reflects the " +
			"registry's last write; use `localnet refresh` to " +
			"re-sync from live docker truth before reading.",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			idx, err := registry.ReadIndex()
			if err != nil {
				return fmt.Errorf("read registry index: %w", err)
			}
			if jsonOut {
				return writeListJSON(cmd.OutOrStdout(), idx)
			}
			writeListText(cmd.OutOrStdout(), idx)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Machine-readable output")
	return cmd
}

func writeListText(out io.Writer, idx *registry.Index) {
	if len(idx.Entries) == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, term.Dimc("No LocalNet instances registered."))
		fmt.Fprintln(out, term.Dimc("Run `canton-devkit localnet up --name <name>` to create one."))
		fmt.Fprintln(out)
		return
	}
	cols := []term.Column{
		{Label: "NAME"},
		{Label: "STATUS"},
		{Label: "VERSION"},
		{Label: "CREATED"},
	}
	rows := make([][]string, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		rows = append(rows, []string{
			e.Name,
			string(e.Status),
			e.SpliceVersion,
			humanSince(e.CreatedAt),
		})
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, term.Table(cols, rows))
	fmt.Fprintln(out)
}

// humanSince parses an RFC3339 timestamp and returns a short ago-style
// string ("2m ago", "5h ago", "3d ago"). Returns the raw timestamp on
// parse failure so we never lose data from a malformed registry write.
func humanSince(ts string) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

type listJSON struct {
	SchemaVersion int                   `json:"schema_version"`
	Instances     []registry.IndexEntry `json:"instances"`
}

func writeListJSON(out io.Writer, idx *registry.Index) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(listJSON{
		SchemaVersion: registry.SchemaVersion,
		Instances:     idx.Entries,
	})
}
