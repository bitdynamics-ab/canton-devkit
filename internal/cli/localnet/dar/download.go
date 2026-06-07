package dar

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// dar download — fetch a DAR back from a participant via
// PackageService.GetDar.
func buildDownload() *cobra.Command {
	var (
		conn connectFlags
		out  string
	)
	cmd := &cobra.Command{
		Use:   "download <main-package-id>",
		Short: "Download a DAR from a participant by main package id",
		Long: `Fetch a previously-uploaded DAR back from a participant's
package store. Useful for verifying what's actually deployed, or for
copying a DAR between participants.

Output filename:
  --out <path>   explicit path
  (default)      "<name>-<version>.dar" derived from the participant's
                 DarDescription metadata, falling back to "<id>.dar"

Exit codes:
  0  DAR written
  1  Invalid arguments
  4  RPC failed (or package id unknown to the participant)`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := conn.connect(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar download: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer func() { _ = client.Close() }()

			resp, err := client.Package.GetDar(cmd.Context(), &adminproto.GetDarRequest{MainPackageId: id})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar download: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}

			outPath := out
			if outPath == "" {
				// the participant-supplied
				// name/version are untrusted — a compromised or
				// hostile participant could return
				// name="../../etc/passwd" and we'd happily write
				// the DAR bytes there. Sanitize via
				// safeBasename before composing the output path
				// so any path separators get stripped and the
				// caller's CWD is the worst-case write target.
				name := safeBasename(resp.GetData().GetName())
				ver := safeBasename(resp.GetData().GetVersion())
				switch {
				case name != "" && ver != "":
					outPath = fmt.Sprintf("%s-%s.dar", name, ver)
				case name != "":
					outPath = name + ".dar"
				default:
					outPath = safeBasename(id) + ".dar"
				}
			}
			// Second defence layer: even when --out was passed
			// explicitly, resolve to an absolute path and refuse
			// if the result contains a `..` segment. Explicit
			// --out is mostly trusted (the user typed it) but a
			// shell-expansion mistake shouldn't silently land
			// the DAR in /etc.
			abs, err := filepath.Abs(outPath)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dar download: resolve output path %q: %s\n", outPath, err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if strings.Contains(filepath.ToSlash(abs), "/..") {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dar download: refusing suspicious output path %q\n", abs)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if err := os.WriteFile(abs, resp.GetPayload(), 0o644); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar download: write %s: %s\n", abs, err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %d bytes to %s\n", len(resp.GetPayload()), abs)
			return nil
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&out, "out", "", "Output path. Defaults to <name>-<version>.dar derived from the participant's metadata.")
	return cmd
}
