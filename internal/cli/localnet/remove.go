package localnet

import (
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
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
state. A RUNNING instance is refused unless --force (which tears it
down first). Use --dry-run to preview what would be removed.`,
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
