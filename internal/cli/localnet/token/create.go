package token

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildCreate returns `token create`: an interactive six-step wizard by
// default, or fully flag-driven with --non-interactive (for CI and the
// Web UI's POST /api/tokens handler, which has no stdin). Both paths
// converge on token.RunCreate.
func buildCreate() *cobra.Command {
	var (
		nonInteractive bool
		format         string
		instance       string
		name           string
		symbol         string
		decimals       int
		initialSupply  string
		issuer         string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new V2 token instrument on the selected LocalNet",
		Long: `Interactively create a new V2 token instrument: pick a name, symbol,
decimal precision, initial supply, and issuer party. The instrument is
recorded in the instance's registry under its symbol so subsequent
` + "`token mint/transfer/burn/balance`" + ` commands can resolve it.

Use --non-interactive (with all the per-field flags) to run from CI
or from a script. The Web UI uses the same orchestration via
POST /api/tokens.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			errw := cmd.ErrOrStderr()

			endpoint, _ := cmd.Flags().GetString("endpoint")
			role, _ := cmd.Flags().GetString("role")
			insecure, _ := cmd.Flags().GetBool("insecure")
			opts := token.CreateOptions{
				Instance:      instance,
				Name:          name,
				Symbol:        symbol,
				Decimals:      decimals,
				InitialSupply: initialSupply,
				Issuer:        issuer,
				Endpoint:      endpoint,
				Role:          role,
				Insecure:      insecure,
			}
			if !nonInteractive {
				if err := runWizard(cmd.InOrStdin(), out, &opts); err != nil {
					return err
				}
			}

			// Match the Web UI: when the instance is up, resolve its ledger
			// port so create lands on-ledger; when it can't be resolved,
			// leave it empty and fall through to a registry-only record —
			// exactly what the UI's create does with liveLedgerEndpoint.
			if opts.Endpoint == "" {
				opts.Endpoint = token.ResolveLedgerEndpoint(opts.Instance, opts.Role)
			}

			res, err := token.RunCreate(out, opts)
			if err != nil {
				if errors.Is(err, token.ErrSymbolInUse) {
					_, _ = fmt.Fprintln(errw, err.Error())
					return errUserAbort
				}
				_, _ = fmt.Fprintln(errw, "create failed:", err)
				return err
			}

			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(res.TokenRef); err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Skip the wizard prompts and take every field via flags (required in CI).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json (json prints the resulting TokenRef).")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&name, "name", "", "Human-readable instrument name.")
	cmd.Flags().StringVar(&symbol, "symbol", "", "Short symbol (letters/digits/_, up to 16 chars).")
	cmd.Flags().IntVar(&decimals, "decimals", 6, "Decimal precision (0..18).")
	cmd.Flags().StringVar(&initialSupply, "initial-supply", "", "Initial supply as a decimal string (e.g. \"1000000\" or \"1.5\").")
	cmd.Flags().StringVar(&issuer, "issuer", "", "Issuer party ID (the V2 instrument admin).")
	cmd.Flags().String("endpoint", "", "Participant gRPC endpoint (host:port). When set, create the instrument on-ledger (TokenRules for the issuer); otherwise record locally only.")
	cmd.Flags().String("role", "app-user", "Role whose JWT authenticates the on-ledger create.")
	cmd.Flags().Bool("insecure", true, "Use plaintext gRPC (LocalNet default).")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

// errUserAbort exits cobra non-zero while suppressing the usual
// usage-and-error dump (the user already saw a focused message).
var errUserAbort = errors.New("create aborted")

// runWizard does the six-step interactive prompt. Refuses non-tty input
// so a piped invocation is pointed at --non-interactive rather than
// silently hanging.
func runWizard(in io.Reader, out io.Writer, opts *token.CreateOptions) error {
	if f, ok := in.(*os.File); ok {
		st, err := f.Stat()
		if err == nil && (st.Mode()&os.ModeCharDevice) == 0 {
			return errors.New(
				"create wizard requires an interactive terminal; " +
					"use --non-interactive with --name/--symbol/--decimals/--initial-supply/--issuer flags")
		}
	}

	r := bufio.NewReader(in)
	_, _ = fmt.Fprintln(out, "Create a new V2 token instrument. Six fields; Ctrl-C to abort.")
	_, _ = fmt.Fprintln(out, "")

	steps := []struct {
		label  string
		assign func(string) error
	}{
		{"Instrument name", func(s string) error { opts.Name = s; return nil }},
		{"Symbol", func(s string) error { opts.Symbol = s; return nil }},
		{"Decimals (0..18)", func(s string) error {
			n, err := strconv.Atoi(s)
			if err != nil {
				return fmt.Errorf("decimals must be an integer (got %q)", s)
			}
			opts.Decimals = n
			return nil
		}},
		{"Initial supply (decimal)", func(s string) error { opts.InitialSupply = s; return nil }},
		{"Issuer party id", func(s string) error { opts.Issuer = s; return nil }},
	}
	for _, st := range steps {
		_, _ = fmt.Fprintf(out, "  %s: ", st.label)
		raw, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read %s: %w", st.label, err)
		}
		v := strings.TrimSpace(raw)
		if v == "" {
			return fmt.Errorf("%s is required", st.label)
		}
		if err := st.assign(v); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Confirm:")
	_, _ = fmt.Fprintf(out,
		"  name=%q symbol=%q decimals=%d supply=%s issuer=%s\n",
		opts.Name, opts.Symbol, opts.Decimals, opts.InitialSupply, opts.Issuer)
	_, _ = fmt.Fprint(out, "  Create? [y/N]: ")
	raw, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	confirm := strings.ToLower(strings.TrimSpace(raw))
	if confirm != "y" && confirm != "yes" {
		return errUserAbort
	}
	_, _ = fmt.Fprintln(out, "")
	return nil
}
