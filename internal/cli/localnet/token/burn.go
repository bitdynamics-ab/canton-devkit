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
		Long: `Burn --amount from --from's holding of --instrument.

The splice-test-token-v2 example has no protocol-level standalone burn,
so burn archives the holder's Holding contracts directly (their signatory
is the account parties + admin, all operator-controlled on LocalNet) and
returns any change. Supply = sum of holdings, so this removes the burned
amount from circulation. --from accepts an alias.

Burning is IRREVERSIBLE — the holding is consumed and the supply is
reduced. The command asks for confirmation before proceeding; pass --yes
to skip the prompt (required when running non-interactively / in CI).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Burn is destructive and irreversible: require explicit
			// confirmation unless --yes.
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
			resolved, err := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if err != nil {
				return err
			}
			opts.Endpoint = resolved.Endpoint
			opts.Role = resolved.Role
			err = token.RunBurn(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) || errors.Is(err, token.ErrUnresolvedLedgerEndpoint) {
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
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Defaults to the instance's captured ledger port; set to override which participant the live burn dials.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-provider", "Role whose JWT authenticates the burn.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}

// confirmBurn gates the irreversible burn behind an explicit typed "yes".
// Non-TTY stdin (piped/redirected, e.g. CI) is refused with an error
// pointing at --yes, rather than reading EOF as a silent "no" — or,
// worse, hanging. Same TTY guard as the create wizard.
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
