package dar

import (
	"fmt"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// dar remove — revoke vetting and/or delete a DAR.
//
// Two related Canton Admin API operations:
//
//	UnvetDar (safer) — revokes the vetting topology transaction so the
//	                    packages stop being usable on the synchronizer.
//	                    DAR bytes stay on disk; reversible with VetDar.
//	RemoveDar (final) — UNVETS + deletes. Upstream warns this is unsafe
//	                    if any contracts still reference the packages.
//
// The default is `--unvet-only` (the safer path). Pass `--purge` to
// delete the DAR entirely. Mirrors what's documented in Splice's docs.
func buildRemove() *cobra.Command {
	var (
		conn        connectFlags
		purge       bool
		synchronize bool
	)
	cmd := &cobra.Command{
		Use:   "remove <main-package-id>",
		Short: "Unvet (or fully remove) a DAR on a participant",
		Long: `Revoke the vetting of a DAR on the targeted participant.

By default this is a reversible "unvet". The bytes stay on the
participant, but the synchronizer stops accepting new uses of the
packages. Re-enable with a fresh upload (which auto-vets) or a manual
vet call.

Pass --purge to unvet the DAR and delete its bytes. This is unsafe if
any contracts still reference the packages, so DevKit defaults to the
reversible path.

Exit codes:
  0  Operation completed
  1  Invalid arguments
  4  RPC failed`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := conn.connect(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar remove: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer func() { _ = client.Close() }()

			if purge {
				_, err := client.Package.RemoveDar(cmd.Context(), &adminproto.RemoveDarRequest{MainPackageId: id})
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar remove --purge: %s\n", err)
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "purged: %s\n", id)
				return nil
			}

			_, err = client.Package.UnvetDar(cmd.Context(), &adminproto.UnvetDarRequest{MainPackageId: id})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar remove (unvet): %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "unvetted: %s (use --purge to delete the bytes)\n", id)
			_ = synchronize // reserved for future use if the unvet API gains it
			return nil
		},
	}
	conn.register(cmd)
	cmd.Flags().BoolVar(&purge, "purge", false,
		"Delete the DAR bytes too. Unsafe if any contracts still reference the packages.")
	cmd.Flags().BoolVar(&synchronize, "synchronize", false,
		"Reserved for future Canton versions.")
	return cmd
}
