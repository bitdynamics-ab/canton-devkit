package token

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

func buildBurn() *cobra.Command {
	var opts token.BurnOptions
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "burn",
		Short: "Burn a holding of a V2 instrument",
		Long: `Burn --amount from --from's holding of --instrument. V2 reuses the V1
BurnMint choice for the burn side; this command wraps the same flow
the upstream Splice CLI uses.

Burning is IRREVERSIBLE — the holding is consumed and the supply is
reduced. The command asks for confirmation before proceeding; pass --yes
to skip the prompt (required when running non-interactively / in CI).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Confirmation gate: burn is destructive and irreversible.
			// Without --yes we require an interactive "yes"; in a
			// non-interactive context we refuse rather than silently
			// proceeding or hanging on a read that never returns.
			if !assumeYes {
				ok, err := confirmBurn(cmd.InOrStdin(), cmd.ErrOrStderr(), opts)
				if err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
					return errSilent
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "burn aborted — no changes made")
					return errSilent
				}
			}
			err := token.RunBurn(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or raw id. Required.")
	cmd.Flags().StringVar(&opts.From, "from", "", "Holder party id. Required.")
	cmd.Flags().StringVar(&opts.Amount, "amount", "", "Decimal amount to burn. Required.")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false,
		"Skip the irreversible-burn confirmation prompt (required for non-interactive / CI use)")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required for the live burn (on-ledger test-token instruments).")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the burn.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}

// confirmBurn gates the irreversible burn behind an explicit acknowledgement.
//
//   - Interactive (stdin is a TTY): prints what's about to be burned and
//     waits for the user to type "yes".
//   - Non-interactive (piped/redirected stdin, e.g. CI): refuses with a
//     clear error pointing at --yes, rather than reading EOF and treating
//     it as "no" silently or — worse — hanging.
//
// Mirrors the TTY guard the create wizard uses (create.go).
func confirmBurn(in io.Reader, errw io.Writer, opts token.BurnOptions) (bool, error) {
	if f, ok := in.(*os.File); ok {
		if st, err := f.Stat(); err == nil && (st.Mode()&os.ModeCharDevice) == 0 {
			return false, errors.New(
				"burn is irreversible and requires confirmation; re-run with --yes " +
					"to proceed non-interactively")
		}
	}
	_, _ = fmt.Fprintf(errw,
		"About to BURN %s of %q from %s on instance %q.\n"+
			"This permanently destroys the holding and reduces supply — it cannot be undone.\n"+
			"Type 'yes' to continue: ",
		opts.Amount, opts.Instrument, opts.From, opts.Instance)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}
