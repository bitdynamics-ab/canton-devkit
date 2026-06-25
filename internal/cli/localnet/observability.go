package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// observabilityToggleTimeout caps how long enable/disable will wait on
// docker compose. Mirrors the Web UI handler's budget: Prometheus +
// Grafana cold-start in <10s typically; 90s leaves room for the first
// image pull on a fresh machine.
const observabilityToggleTimeout = 90 * time.Second

// buildObservability wires `dpm localnet observability <verb>` — the
// CLI mirror of the Web UI's POST /api/instances/{name}/observability
// toggle (and a read-only `status`). Per AGENTS.md "CLI ↔ Web UI
// parity": the runtime observability toggle is a per-instance operation
// a user wants from either surface. Both this command and the HTTP
// handler call the SAME neutral localnet.SetObservability — no
// duplicated docker orchestration.
//
// Sub-verbs:
//
//	enable   → turn Prometheus / Grafana on for a running instance,
//	           mirror of POST .../observability {enabled:true}
//	disable  → turn them off, mirror of {enabled:false}
//	status   → report which sidecars are on + their URLs (read-only;
//	           works on a stopped instance too)
//
// `--prometheus` / `--grafana` let a user flip each sidecar
// independently (matching the per-component HTTP body); with neither
// flag, enable/disable act on BOTH (the legacy umbrella semantics).
func buildObservability() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "observability",
		Aliases: []string{"obs"},
		Short:   "Toggle the Prometheus + Grafana sidecars on a running instance",
		Long: "Enable, disable, or inspect Prometheus and Grafana sidecars " +
			"for a LocalNet instance without restarting Canton. " +
			"The enabled set is persisted across down/up.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(buildObservabilityEnable())
	cmd.AddCommand(buildObservabilityDisable())
	cmd.AddCommand(buildObservabilityStatus())
	return cmd
}

// obsComponentFlags binds the optional per-component selectors shared by
// the enable + disable verbs and resolves them into the (prom, graf)
// target booleans. With neither flag set, both are selected (umbrella).
type obsComponentFlags struct {
	prometheus bool
	grafana    bool
}

func (f *obsComponentFlags) bind(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.prometheus, "prometheus", false,
		"Act on the Prometheus sidecar only (default: both Prometheus and Grafana)")
	cmd.Flags().BoolVar(&f.grafana, "grafana", false,
		"Act on the Grafana sidecar only (default: both Prometheus and Grafana)")
}

// selected reports which components the flags target. Neither flag set
// means "both" (umbrella); otherwise only the flagged ones.
func (f *obsComponentFlags) selected() (prom, graf bool) {
	if !f.prometheus && !f.grafana {
		return true, true
	}
	return f.prometheus, f.grafana
}

func buildObservabilityEnable() *cobra.Command {
	var (
		name   string
		format string
		flags  obsComponentFlags
	)
	cmd := &cobra.Command{
		Use:           "enable",
		Short:         "Enable Prometheus + Grafana on a running instance",
		Long:          "Start the observability overlay and bring up sidecars. Prints Grafana and Prometheus URLs. Requires a running instance. Use --prometheus or --grafana to enable just one.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			prom, graf := flags.selected()
			return runObservabilityToggle(cmd, name, format, prom, graf)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance to enable observability on (defaults to the only registered instance)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	flags.bind(cmd)
	return cmd
}

func buildObservabilityDisable() *cobra.Command {
	var (
		name   string
		format string
		flags  obsComponentFlags
	)
	cmd := &cobra.Command{
		Use:           "disable",
		Short:         "Disable Prometheus + Grafana on a running instance",
		Long:          "Stops and removes the observability sidecars (Canton + Splice are untouched) and clears their recorded ports. Use --prometheus or --grafana to disable just one side; with neither flag both are turned off.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// For disable, the selected components are the ones turned
			// OFF; everything else keeps its current state. We resolve
			// the *target* (prom, graf) by reading current state and
			// flipping only the selected ones to false.
			selProm, selGraf := flags.selected()
			state, err := resolveMetricsInstance(name)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			cur := localnet.ReadObservabilityState(state)
			wantProm := cur.Prometheus && !selProm
			wantGraf := cur.Grafana && !selGraf
			return runObservabilityToggleResolved(cmd, state, format, wantProm, wantGraf)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance to disable observability on (defaults to the only registered instance)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	flags.bind(cmd)
	return cmd
}

func buildObservabilityStatus() *cobra.Command {
	var (
		name   string
		format string
	)
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show whether observability is enabled for an instance",
		Long:          "Read-only report of which observability sidecars are running for the instance, plus the Prometheus / Grafana URLs. Works on a stopped instance (reports the persisted state).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			state, err := resolveMetricsInstance(name)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			cur := localnet.ReadObservabilityState(state)
			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(observabilityStatusJSON(state, cur))
			}
			renderObservabilityStatus(cmd.OutOrStdout(), state, cur)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance to inspect (defaults to the only registered instance)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

// runObservabilityToggle resolves the instance by name, validates the
// running precondition, then applies the (prom, graf) target. Shared by
// the enable verb (disable resolves its own target first, then calls
// runObservabilityToggleResolved directly).
func runObservabilityToggle(cmd *cobra.Command, name, format string, wantProm, wantGraf bool) error {
	state, err := resolveMetricsInstance(name)
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
		return localnet.AsExitError(localnet.ExitUserError)
	}
	return runObservabilityToggleResolved(cmd, state, format, wantProm, wantGraf)
}

// runObservabilityToggleResolved is the shared tail of enable/disable:
// validate format + running precondition, hold the per-instance lock,
// call the neutral localnet.SetObservability, and render the result.
func runObservabilityToggleResolved(cmd *cobra.Command, state *registry.State, format string, wantProm, wantGraf bool) error {
	if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
		return err
	}
	if state.Status != registry.StatusRunning {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"instance %q is not running (status=%s) — bring it up first with "+
				"`dpm localnet up --name %s` (the observability profile is re-enabled automatically once it has been set once)\n",
			state.Name, state.Status, state.Name)
		return localnet.AsExitError(localnet.ExitUserError)
	}

	// Per-instance lock — same CAS the UI handler and `up` hold so we
	// can't race a concurrent down/up or another toggle.
	release, lerr := registry.Lock(state.Name)
	if lerr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"instance %q is busy — another operation holds the lock; retry once it finishes\n", state.Name)
		return localnet.AsExitError(localnet.ExitUserError)
	}
	defer release()

	// Re-read under the lock so we operate on fresh state.
	fresh, err := registry.Read(state.Name)
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "re-read state: "+err.Error())
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	state = fresh

	ctx, cancel := context.WithTimeout(cmd.Context(), observabilityToggleTimeout)
	defer cancel()

	res, err := localnet.SetObservability(ctx, state, wantProm, wantGraf, cmd.ErrOrStderr())
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "observability toggle failed: "+err.Error())
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}

	if format == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"instance":      state.Name,
			"prometheus":    res.Prometheus,
			"grafana":       res.Grafana,
			"prometheus_ui": res.PrometheusPort,
			"grafana_ui":    res.GrafanaPort,
			"warning":       res.Warning,
		})
	}
	renderObservabilityResult(cmd.OutOrStdout(), state.Name, res)
	return nil
}

// observabilityStatusJSON is the stable shape `status --format json`
// emits. Keys mirror the toggle response so a script can read either.
func observabilityStatusJSON(state *registry.State, cur localnet.ObservabilityState) map[string]any {
	return map[string]any{
		"instance":      state.Name,
		"status":        string(state.Status),
		"prometheus":    cur.Prometheus,
		"grafana":       cur.Grafana,
		"prometheus_ui": cur.PrometheusPort,
		"grafana_ui":    cur.GrafanaPort,
		"grafana_url":   grafanaURLFor(state),
	}
}

// renderObservabilityResult prints the post-toggle summary in the
// CLI's Section style.
func renderObservabilityResult(out cobraWriter, instance string, res localnet.ObservabilityResult) {
	rows := []string{
		term.KV("Prometheus", onOff(res.Prometheus), 14),
		term.KV("Grafana", onOff(res.Grafana), 14),
	}
	if res.GrafanaPort != 0 {
		rows = append(rows, term.KV("Grafana URL",
			fmt.Sprintf("http://localhost:%d/d/%s", res.GrafanaPort, grafanaDashboardUID), 14))
	}
	if res.PrometheusPort != 0 {
		rows = append(rows, term.KV("Prometheus URL",
			fmt.Sprintf("http://localhost:%d", res.PrometheusPort), 14))
	}
	body := joinLines(rows)
	_, _ = fmt.Fprintln(out, term.Section("observability · "+instance, "applied", body, 0))
	if res.Warning != "" {
		_, _ = fmt.Fprintln(out, term.Warnc("warning: "+res.Warning))
	}
}

// renderObservabilityStatus prints the read-only status report.
func renderObservabilityStatus(out cobraWriter, state *registry.State, cur localnet.ObservabilityState) {
	rows := []string{
		term.KV("Instance status", string(state.Status), 16),
		term.KV("Prometheus", onOff(cur.Prometheus), 16),
		term.KV("Grafana", onOff(cur.Grafana), 16),
	}
	if url := grafanaURLFor(state); url != "" {
		rows = append(rows, term.KV("Grafana URL", url, 16))
	}
	if cur.PrometheusPort != 0 {
		rows = append(rows, term.KV("Prometheus URL",
			fmt.Sprintf("http://localhost:%d", cur.PrometheusPort), 16))
	}
	if !cur.Prometheus && !cur.Grafana {
		rows = append(rows, "",
			term.Dimc("enable with `dpm localnet observability enable --name "+state.Name+"`"))
	}
	body := joinLines(rows)
	_, _ = fmt.Fprintln(out, term.Section("observability · "+state.Name, "status", body, 0))
}

func onOff(b bool) string {
	if b {
		return term.Successc("on")
	}
	return term.Dimc("off")
}

// joinLines concatenates rows with newlines without pulling strings
// just for one Join — keeps this file's import list tight.
func joinLines(rows []string) string {
	out := ""
	for i, r := range rows {
		if i > 0 {
			out += "\n"
		}
		out += r
	}
	return out
}
