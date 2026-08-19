package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
	"github.com/spf13/cobra"
)

// buildTelemetryCmd is the root-level `canton-devkit telemetry …` command
// — telemetry is tool-wide, not a LocalNet concern.
func buildTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Control and audit anonymous usage telemetry",
		Long: `canton-devkit sends anonymous, aggregate usage counters (command name,
OS, Docker engine, exit status — counted daily, no paths, no party ids,
no JWTs, no error messages) to help prioritize fixes. Uploads also carry
one random token (a UUID, not derived from your machine) used only to
count distinct installs; rotate it with 'telemetry reset-id'.

It is ON by default (opt-out). Turn it off with 'telemetry off', or set
DPM_TELEMETRY=off / DO_NOT_TRACK=1. 'telemetry preview' shows exactly the
counters queued for today; 'telemetry status' shows the on/off state
and which rule decided it.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "on", Short: "Enable anonymous usage telemetry", Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				if err := telemetry.SetEnabled(true); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(c.OutOrStdout(), "Telemetry enabled.")
				return nil
			},
		},
		&cobra.Command{
			Use: "off", Short: "Disable anonymous usage telemetry", Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				if err := telemetry.SetEnabled(false); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(c.OutOrStdout(), "Telemetry disabled. No further usage counters will be recorded.")
				return nil
			},
		},
		buildTelemetryStatus(),
		buildTelemetryPreview(),
		buildTelemetryFlush(),
		buildTelemetryResetID(),
	)
	return cmd
}

func buildTelemetryResetID() *cobra.Command {
	return &cobra.Command{
		Use:   "reset-id",
		Short: "Rotate the anonymous install id used to de-duplicate installs",
		Long: `canton-devkit carries one anonymous, random token (a UUID, not
derived from any hardware detail) so the collector can count distinct
installs without ever identifying your machine. This rotates it: a fresh
random token is minted on the next upload, breaking any linkage between
past and future counter submissions.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := telemetry.ResetInstallID(); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(c.OutOrStdout(), "Anonymous install id rotated. A new random token will be used on the next upload.")
			return nil
		},
	}
}

func buildTelemetryFlush() *cobra.Command {
	return &cobra.Command{
		Use:   "flush",
		Short: "Send all queued counters to the collector now (skip the daily window)",
		Long: `Uploads every queued counter file — including today's still-open
period — to the configured collector immediately, instead of waiting for
the normal daily ship. Useful when testing the telemetry pipeline so you
can see counters land in the dashboard right away.

Requires a collector endpoint (CANTON_DEVKIT_TELEMETRY_ENDPOINT or a
release build with one baked in) and telemetry enabled.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			out := c.OutOrStdout()
			if on, _ := telemetry.Enabled(); !on {
				_, _ = fmt.Fprintln(out, "Telemetry is off — nothing to send. Enable with `telemetry on`.")
				return nil
			}
			n, err := telemetry.FlushNow()
			if err != nil {
				_, _ = fmt.Fprintf(c.ErrOrStderr(), "flush failed: %s\n", err)
				return err
			}
			if n == 0 {
				_, _ = fmt.Fprintln(out, "Nothing queued to send.")
				return nil
			}
			_, _ = fmt.Fprintf(out, "Flushed %d period(s) to %s\n", n, telemetry.EffectiveEndpoint())
			return nil
		},
	}
}

func buildTelemetryStatus() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show telemetry state + the rule that decided it", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			on, src := telemetry.Enabled()
			state := "disabled"
			if on {
				state = "enabled"
			}
			out := c.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Telemetry:  %s\n", state)
			_, _ = fmt.Fprintf(out, "Decided by: %s\n", src)
			_, _ = fmt.Fprintf(out, "Channel:    %s\n", telemetry.Channel())
			ep := telemetry.EffectiveEndpoint()
			if ep == "" {
				_, _ = fmt.Fprintln(out, "Collector:  (none — counters stay on this machine)")
			} else {
				_, _ = fmt.Fprintf(out, "Collector:  %s\n", ep)
			}
			_, _ = fmt.Fprintln(out, "\nToday's queued counters: run `telemetry preview`.")
			return nil
		},
	}
}

func buildTelemetryPreview() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use: "preview", Short: "Print this period's local counter file (exactly what would be sent)", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			agg, err := telemetry.PreviewCurrentPeriod()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"schema_version": agg.SchemaVersion,
					"period":         agg.Period,
					"granularity":    agg.Granularity,
					"counters":       agg.Counters,
				})
			}
			_, _ = fmt.Fprintf(out, "%s %s (file: %s)\n", agg.Granularity, agg.Period, telemetry.CurrentPeriodFile())
			if len(agg.Counters) == 0 {
				_, _ = fmt.Fprintln(out, "  (no counters recorded yet this period)")
				return nil
			}
			charts := make([]string, 0, len(agg.Counters))
			for ch := range agg.Counters {
				charts = append(charts, ch)
			}
			sort.Strings(charts)
			for _, ch := range charts {
				_, _ = fmt.Fprintf(out, "  %s\n", ch)
				buckets := agg.Counters[ch]
				keys := make([]string, 0, len(buckets))
				for b := range buckets {
					keys = append(keys, b)
				}
				sort.Strings(keys)
				for _, b := range keys {
					_, _ = fmt.Fprintf(out, "    %-26s %d\n", b, buckets[b])
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	return cmd
}
