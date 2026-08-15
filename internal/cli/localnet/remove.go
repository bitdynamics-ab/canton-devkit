package localnet

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

func buildRemove() *cobra.Command {
	opts := &localnet.RemoveOptions{}
	cmd := &cobra.Command{
		Use:     "remove [name]",
		Aliases: []string{"clean"},
		Short:   "Remove Canton LocalNet state, containers, and volumes for an instance",
		Long: `Remove DevKit-managed state for a named instance (or all instances
with --all): the registry entry, the per-instance data directory, and
any lingering docker containers/volumes for the compose project.

remove (alias: clean) is the housekeeping / recovery verb — use it to
reclaim disk from stopped instances or to scrub orphaned/corrupted
state. For a RUNNING instance you are asked to confirm, and on yes it
is torn down and removed in one step; --force skips the prompt and is
required when stdin is not a terminal. Use --dry-run to preview what
would be removed.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hasPos := len(args) > 0 && strings.TrimSpace(args[0]) != ""
			hasFlag := cmd.Flags().Changed("name")
			switch {
			case opts.All && (hasPos || hasFlag):
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--all and an instance name are mutually exclusive")
				return localnet.AsExitError(localnet.ExitUserError)
			case opts.All:
				// name stays empty; RunRemove walks every instance.
			case !hasPos && !hasFlag:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"instance name required: `localnet %s <name>` (or --name), or --all for every instance\n", cmd.Name())
				return localnet.AsExitError(localnet.ExitUserError)
			default:
				name, err := resolveName(cmd, args)
				if err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
					return localnet.AsExitError(localnet.ExitUserError)
				}
				if err := localnet.ValidateName(name); err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
					return localnet.AsExitError(localnet.ExitUserError)
				}
				opts.Name = name
			}
			if !opts.Force {
				opts.ConfirmStop = confirmStopPrompt(cmd.InOrStdin(), cmd.ErrOrStderr())
			}
			return localnet.AsExitError(
				localnet.RunRemove(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "",
		"Name of the instance to remove. Can also be passed as a positional argument.")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Remove every registered instance.")
	cmd.Flags().BoolVar(&opts.Force, "force", false,
		"Tear down and remove even a running instance (runs `down` first).")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false,
		"Print what would be removed without removing anything.")
	return cmd
}

// confirmStopPrompt asks whether a running instance may be torn down as
// part of remove. Non-TTY stdin (piped, redirected, /dev/null — the CI
// shapes) returns an error pointing at --force instead of prompting
// where nobody can answer.
func confirmStopPrompt(in io.Reader, errw io.Writer) func(string) (bool, error) {
	return func(name string) (bool, error) {
		if !term.CanPrompt(in) {
			return false, errors.New(runningNonInteractiveMessage(name))
		}
		_, _ = fmt.Fprintf(errw,
			"%s is running. Removing it stops the instance and deletes its containers,\n"+
				"volumes, ledger data, and registry state. This cannot be undone.\n"+
				"Stop and remove %s? [y/N]: ", name, name)
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}

func runningNonInteractiveMessage(name string) string {
	return fmt.Sprintf(
		"%s is running and stdin is not a terminal — cannot ask for confirmation. "+
			"Run `localnet down %s` first, or pass --force to tear it down and remove in one step.",
		name, name)
}
