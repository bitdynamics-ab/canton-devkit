// — cobra wrappers for `dpm localnet snapshot` + `restore`.
//
// The orchestrators live in internal/localnet/snapshot. They were
// extracted from this package as part of so the UI handlers
// (internal/ui/handlers/snapshots.go) can call the same code paths —
// the import cycle ui→handlers→cli/localnet was blocking parity.
//
// Keep this file thin: flag parsing + delegate. Anything more
// belongs in the snapshot package alongside the streaming/tar code.
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
		Use:   "snapshot",
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

Consistency caveat: pg_dumpall is consistent per-database but not atomic
across the node's several databases, so a snapshot of a busy instance
can catch cross-database skew. Quiesce activity for a fully-consistent
capture; the command warns when it runs.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	cmd.Flags().StringVar(&name, "name", "", "Required. Instance to snapshot.")
	cmd.Flags().StringVar(&to, "to", "", "Required. Destination archive path (.tgz).")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func buildRestore() *cobra.Command {
	var name, from string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a LocalNet's database + registry state from a snapshot tarball",
		Long: `Loads the database dump from the named snapshot archive back
into the instance's Postgres AND re-registers the instance via the
state.json embedded in the archive. The header is validated before
anything is touched. The load runs against a throwaway Postgres on the
instance's data volume — no nodes, so no open connections block it.

If the snapshot's Splice version differs from an existing local
instance's, restore refuses unless --force is set.

The instance must NOT be running (a restore drops and recreates every
database) — run 'localnet down --name X' first. If the instance does
not exist locally, it is registered from the embedded state.json. Either
way the restore leaves it stopped; run 'localnet up --name X' to start
it on the restored database.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	cmd.Flags().StringVar(&name, "name", "", "Required. Instance to restore into.")
	cmd.Flags().StringVar(&from, "from", "", "Required. Source archive path (.tgz).")
	cmd.Flags().BoolVar(&force, "force", false,
		"Override Splice-version mismatch refusal. Use with care.")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}
