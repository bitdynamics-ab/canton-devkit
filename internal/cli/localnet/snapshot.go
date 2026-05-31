// BIT-147 — cobra wrappers for `dpm localnet snapshot` + `restore`.
//
// The orchestrators live in internal/localnet/snapshot. They were
// extracted from this package as part of BIT-184 so the UI handlers
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
		Short: "Capture a LocalNet's docker volumes + registry state to a tarball",
		Long: `Captures every docker volume owned by the named LocalNet's
compose project, plus the instance's registry.State, into a single
.tgz archive. The header (snapshot.json) is written FIRST so
'restore' can stream-validate schema + Splice version before
unpacking anything.

The instance does NOT need to be stopped. Volumes are read from
ephemeral alpine containers — services keep using their own copy.

Consistency caveat (BIT-207): a snapshot of a RUNNING instance is
crash-consistent, not application-consistent — in-flight ledger
transactions or unflushed database writes may be only partially
captured, so a restored copy can need crash recovery. For a
guaranteed-consistent capture, 'localnet pause --name X' (or 'down')
first; the command warns when run against a running instance.`,
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
		Short: "Restore docker volumes + registry state from a snapshot tarball",
		Long: `Restores every volume from the named snapshot archive AND
re-registers the instance via the state.json embedded in the
archive. Header is validated before any volume is touched.

If the snapshot's Splice version differs from an existing local
instance's, restore refuses unless --force is set — restoring
volumes formatted for a different binary is unsafe by default.

If the instance does not exist locally, it is registered from the
embedded state.json with Status=stopped. If it exists and is
running, restore refuses — run 'localnet down --name X' first.`,
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
