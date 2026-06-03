package dar

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// BIT-50: dar upload — push a DAR to one or all participants of a
// named LocalNet via PackageService.UploadDar.
//
// `--all-participants` is the proposal-mandated fan-out: when set
// alongside `--instance`, DevKit reaches every known participant role
// (sv, app-provider, app-user) on that instance and uploads in
// sequence. Errors on one role don't abort the others; the command's
// final exit code reflects the worst observed outcome.
func buildUpload() *cobra.Command {
	var (
		conn   connectFlags
		vet    bool
		dry    bool
		desc   string
		sync   bool
		allPpt bool
	)
	cmd := &cobra.Command{
		Use:   "upload <dar-path>",
		Short: "Upload a DAR to one or all participants of a LocalNet",
		Long: `Upload a DAR file to a Canton participant via its Admin API
(PackageService.UploadDar). Optionally vet all packages so they're
ready for use immediately.

Targeting modes:
  --admin-host <h:p> --token <jwt>      → direct, single participant
  --instance <name> --role <role>       → DevKit registry, single
                                          participant
  --instance <name> --all-participants  → fan out to sv,
                                          app-provider, app-user

For Splice LocalNet on default ports, app-user is localhost:2902,
app-provider localhost:3902, sv localhost:4902.

Exit codes:
  0  All targeted uploads succeeded (or dry run validated)
  1  Invalid arguments
  4  At least one RPC failed (auth, network, server-side rejection)`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			// BIT-127 review fix: bounded read (replaces
			// os.ReadFile) — refuses DARs over MaxDARBytes
			// before allocating the buffer.
			data, err := cdkdar.ReadDARFile(path)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar upload: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			roles, err := targetRoles(&conn, allPpt)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			worst := 0
			for _, role := range roles {
				rconn := conn
				if role != "" {
					rconn.Role = role
				}
				header := "participant"
				if role != "" {
					header = "participant=" + role
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s]\n", header)

				code := uploadOnce(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					&rconn, path, data, desc, vet, sync, dry)
				if code > worst {
					worst = code
				}
			}
			if worst > 0 {
				return localnet.AsExitError(worst)
			}
			return nil
		},
	}
	conn.register(cmd)
	cmd.Flags().BoolVar(&allPpt, "all-participants", false,
		"Fan out to every known participant role on --instance (sv, app-provider, app-user). Requires --instance.")
	cmd.Flags().BoolVar(&vet, "vet", true, "Vet all packages in the DAR after upload (default true).")
	cmd.Flags().BoolVar(&sync, "synchronize", false, "Wait for the vetting transaction to be observed before returning.")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "Validate without uploading. Calls ValidateDar instead of UploadDar.")
	cmd.Flags().StringVar(&desc, "description", "", "Optional human-readable description recorded with the upload.")
	return cmd
}

// uploadOnce performs one upload (or validate) against the configured
// target. Returns 0 on success or one of the localnet.Exit* codes on
// failure. Errors are written to errw; success messages to out.
//
// Separating this out makes the fan-out loop in RunE simple: call
// uploadOnce per role, accumulate the worst exit code, exit with that.
func uploadOnce(
	ctx context.Context,
	out, errw io.Writer,
	conn *connectFlags,
	path string,
	data []byte,
	desc string,
	vet, sync, dry bool,
) int {
	client, err := conn.connect(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "  dar upload: %s\n", err)
		return localnet.ExitUserError
	}
	defer func() { _ = client.Close() }()

	if dry {
		resp, err := client.Package.ValidateDar(ctx, &adminproto.ValidateDarRequest{
			Data:     data,
			Filename: filepath.Base(path),
		})
		if err != nil {
			_, _ = fmt.Fprintf(errw, "  dry-run failed: %s\n", err)
			return localnet.ExitRuntimeFailure
		}
		_, _ = fmt.Fprintf(out, "  validated — main package id: %s\n", resp.GetMainPackageId())
		return 0
	}

	resp, err := client.Package.UploadDar(ctx, &adminproto.UploadDarRequest{
		Dars: []*adminproto.UploadDarRequest_UploadDarData{{
			Bytes:       data,
			Description: optString(desc),
		}},
		VetAllPackages:     vet,
		SynchronizeVetting: sync,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "  upload failed: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	for _, id := range resp.GetDarIds() {
		_, _ = fmt.Fprintf(out, "  uploaded: %s\n", id)
	}
	return 0
}

// targetRoles returns the list of roles to iterate over. When
// --all-participants is set, returns the full Splice LocalNet topology
// (sv, app-provider, app-user). Otherwise returns a single empty
// string sentinel meaning "use whatever conn already targets".
//
// Surfaces a clear error if --all-participants is set without
// --instance, since fan-out only makes sense in registry mode.
func targetRoles(conn *connectFlags, allPpt bool) ([]string, error) {
	if !allPpt {
		return []string{""}, nil
	}
	if conn.Instance == "" {
		return nil, fmt.Errorf("--all-participants requires --instance")
	}
	return []string{"sv", "app-provider", "app-user"}, nil
}

// optString returns a *string for proto3 `optional string` fields.
// nil means "field not set"; a non-nil empty string means "set to empty",
// which is rarely what we want — so we treat "" as unset.
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
