// Cobra wrappers for `dpm localnet snapshot` + `restore`.
//
// The orchestrators live in internal/localnet/snapshot so the UI
// handlers (internal/ui/handlers/snapshots.go) can call the same code
// paths; keeping them here would force an ui→handlers→cli/localnet
// import cycle.
//
// Keep this file thin: flag parsing + delegate. Anything more belongs
// in the snapshot package alongside the streaming/tar code.
package localnet

import (
	"fmt"

	corelocalnet "github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/snapshot"
	"github.com/spf13/cobra"
)

func buildSnapshot() *cobra.Command {
	var name, to string
	cmd := &cobra.Command{
		Use:   "snapshot [name]",
		Short: "Capture a LocalNet's database + registry state to a tarball",
		Long: `Captures a logical PostgreSQL dump (pg_dumpall) of the named
LocalNet's database, plus the instance's registry.State, into a single
.tgz archive. A LocalNet keeps all of its state — ledger, contracts,
parties, node identities/keys — in that one Postgres, so the dump is the
whole instance. This is the same backup method Canton and Splice
document for their nodes, and the header (snapshot.json) is written
FIRST so 'restore' can stream-validate schema + Splice version before
loading anything.

The instance MUST be running — pg_dumpall reads from a live Postgres.

For a consistent capture, the node containers are paused (docker pause)
for the duration of the dump and resumed afterwards, so no component
writes to the database while the backup runs — Canton's documented rule
for a single-step whole-cluster backup. With every database frozen at
one instant, the cross-database ordering invariant holds automatically,
so the snapshot is application-consistent, not merely crash-consistent.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveName(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			name = resolved
			if err := corelocalnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			if to == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--to is required")
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			return corelocalnet.AsExitError(
				snapshot.RunSnapshot(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), name, to))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance to snapshot. Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&to, "to", "", "Required. Destination archive path (.tgz).")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func buildRestore() *cobra.Command {
	var name, from string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore [name]",
		Short: "Restore a LocalNet's database + registry state from a snapshot tarball",
		Long: `Loads the database dump from the named snapshot archive back
into the instance's Postgres AND re-registers the instance via the
state.json embedded in the archive. The header is validated before
anything is touched. The load runs against a throwaway Postgres on the
instance's data volume — no nodes, so no open connections block it.

If the snapshot's Splice version differs from an existing local
instance's, restore refuses unless --force is set.

The instance must NOT be running (a restore drops and recreates every
database) — run 'localnet down X' first. If the instance does
not exist locally, it is registered from the embedded state.json. Either
way the restore leaves it stopped; run 'localnet up X' to start
it on the restored database.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveName(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			name = resolved
			if err := corelocalnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			if from == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--from is required")
				return corelocalnet.AsExitError(corelocalnet.ExitUserError)
			}
			return corelocalnet.AsExitError(
				snapshot.RunRestore(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					name, from, force))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance to restore into. Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&from, "from", "", "Required. Source archive path (.tgz).")
	cmd.Flags().BoolVar(&force, "force", false,
		"Override Splice-version mismatch refusal. Use with care.")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}
