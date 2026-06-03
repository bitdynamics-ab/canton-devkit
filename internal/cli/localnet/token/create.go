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

// buildCreate returns the `dpm localnet token create` subcommand.
//
// Two modes:
//
//   - **Interactive** (default): prompts the user through the six wizard
//     steps using plain stdin/stdout reads. Suitable for terminals;
//     refuses non-tty input so it fails fast in a CI pipeline.
//
//   - **--non-interactive**: takes every wizard value as a flag and
//     skips the prompts. Required when called from CI or from the
//     Web UI's POST /api/tokens handler (which doesn't have a stdin).
//
// Both paths converge on token.RunCreate, which validates the inputs,
// persists the instrument under the per-instance flock, and prints the
// resulting TokenRef.
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

			opts := token.CreateOptions{
				Instance:      instance,
				Name:          name,
				Symbol:        symbol,
				Decimals:      decimals,
				InitialSupply: initialSupply,
				Issuer:        issuer,
			}
			if !nonInteractive {
				if err := runWizard(cmd.InOrStdin(), out, &opts); err != nil {
					return err
				}
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
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

// errUserAbort is sentinel so the cobra RunE call surface returns a
// non-zero exit code but suppresses the usual usage-and-stack dump
// (the user already saw a focused message).
var errUserAbort = errors.New("create aborted")

// runWizard does the six-step interactive prompt. Refuses non-tty
// input so a piped invocation falls back to (or surfaces) the
// --non-interactive flag rather than silently hanging.
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
