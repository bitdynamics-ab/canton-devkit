package localnet

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
	"github.com/spf13/cobra"
)

// buildTelemetry wires `localnet telemetry on|off|status` — the user's
// control + audit surface for the anonymous, aggregate, opt-out usage
// telemetry (Milestone 4).
func buildTelemetry() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Control and audit anonymous usage telemetry",
		Long: `canton-devkit collects anonymous, aggregate usage data (the command
you ran, its exit code, a coarse duration, tool + OS version, and a
random install id) to guide development. It NEVER collects instance
names, party ids, DAR contents, ports, credentials, or file paths.

It is on by default (opt-out). Disable it with 'telemetry off', or set
CANTON_DEVKIT_TELEMETRY=0 / DO_NOT_TRACK=1. 'telemetry status' shows
exactly what is queued and where (if anywhere) it is sent.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "on",
			Short: "Enable anonymous usage telemetry",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := telemetry.SetEnabled(true); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Telemetry enabled.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "off",
			Short: "Disable anonymous usage telemetry",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := telemetry.SetEnabled(false); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Telemetry disabled. No further usage data will be collected.")
				return nil
			},
		},
		buildTelemetryStatus(),
	)
	return cmd
}

func buildTelemetryStatus() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show telemetry state + exactly what is queued to be sent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := telemetry.LoadConfig()
			events, _ := telemetry.RecentEvents()
			endpoint := telemetry.EffectiveEndpoint()
			out := cmd.OutOrStdout()

			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"enabled":       telemetry.Enabled(),
					"install_id":    cfg.InstallID,
					"endpoint":      endpoint,
					"queued_events": events,
				})
			}

			state := "enabled"
			if !telemetry.Enabled() {
				state = "disabled"
			}
			_, _ = fmt.Fprintf(out, "Telemetry:   %s\n", state)
			_, _ = fmt.Fprintf(out, "Install id:  %s\n", cfg.InstallID)
			if endpoint == "" {
				_, _ = fmt.Fprintf(out, "Endpoint:    (none — events stay on this machine)\n")
			} else {
				_, _ = fmt.Fprintf(out, "Endpoint:    %s\n", endpoint)
			}
			_, _ = fmt.Fprintf(out, "Queued:      %d event(s)\n", len(events))
			if len(events) > 0 {
				_, _ = fmt.Fprintln(out, "\nExactly what would be sent (newest last):")
				for _, e := range events {
					_, _ = fmt.Fprintf(out, "  %s  %-22s exit=%d  %s  %s/%s\n",
						e.Timestamp, e.Command, e.ExitCode, e.DurationBucket, e.OS, e.Arch)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	return cmd
}
