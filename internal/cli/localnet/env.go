package localnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/spf13/cobra"
)

// buildEnv wires `dpm localnet env --name <inst>`.
//
// Emits a block of exported KEY=value lines describing every endpoint and
// credential of the instance, designed for the common shell idiom:
//
//	eval "$(dpm localnet env --name hubble)"
//
// Three formats:
//
//	shell (default) export KEY='value' with POSIX quoting
//	dotenv KEY="value" with dotenv escaping
//	json api/types.EnvExport for scripted consumers
//
// The Web UI app-config handler reuses the same localnet.BuildEnvExport
// builder, so both surfaces emit an identical apitypes.EnvExport.
func buildEnv() *cobra.Command {
	var (
		name       string
		format     string
		includeJWT bool
	)
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Export LocalNet endpoints and credentials as env vars",
		Long: `Prints an exported KEY=value block summarising the named LocalNet
instance -- every host port from the Ports map (Ledger / JSON /
admin APIs per role, wallet UIs, scan UI, postgres) plus the
user/audience for each captured credential and the real on-ledger
party ids. Use with:

  eval "$(dpm localnet env --name hubble)"

Formats:

  shell    POSIX single-quoted export KEY='value'  (default; eval-safe)
  dotenv   double-quoted KEY="value" with $/\/" escapes (dotenv spec)
  json     machine-readable shape; matches the Web UI handler

JWTs are REDACTED by default (CANTON_<ROLE>_JWT=<redacted>) so
CI logs / shared terminals don't leak the dev-only signing
secret. Pass --include-jwt to opt in before piping or sourcing
raw token values.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			ex, err := collectEnv(name, includeJWT)
			if err != nil {
				if errors.Is(err, registry.ErrNotFound) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "no LocalNet instance named %q\n", name)
					return localnet.AsExitError(localnet.ExitUserError)
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			switch format {
			case "", "shell":
				return writeEnvShell(cmd.OutOrStdout(), ex)
			case "dotenv":
				return writeEnvDotenv(cmd.OutOrStdout(), ex)
			case "json":
				return writeEnvJSON(cmd.OutOrStdout(), ex)
			default:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"--format must be shell, dotenv, or json (got %q)\n", format)
				return localnet.AsExitError(localnet.ExitUserError)
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "",
		"Required. Identifier of the LocalNet instance to export.")
	cmd.Flags().StringVar(&format, "format", "shell",
		"Output format: shell | dotenv | json")
	cmd.Flags().BoolVar(&includeJWT, "include-jwt", false,
		"Emit raw CANTON_<ROLE>_JWT values. Default is <redacted>.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// collectEnv builds the export from the registry via the shared
// localnet.BuildEnvExport builder. The CLI keeps this thin wrapper so
// the existing callsites + tests read unchanged; the actual shape
// (ports incl. participant Ledger/Admin/JSON APIs + scan UI, per-role
// credentials, real party ids) is produced by the same function the
// Web UI app-config handler calls. See AGENTS.md "CLI ↔ Web UI
// parity".
func collectEnv(name string, includeJWT bool) (apitypes.EnvExport, error) {
	return localnet.BuildEnvExport(name, includeJWT)
}

// portEnvKey converts a logical port name into the canonical
// CANTON_<UPPER>_PORT env-var key. Delegates to the shared builder so
// the CLI and UI normalise identically.
func portEnvKey(logical string) string {
	return localnet.PortEnvKey(logical)
}

// writeEnvShell prints export KEY='value' POSIX-single-quoted so the
// output is safe for `eval "$(dpm localnet env ...)"` even when a
// value contains shell metacharacters ($ ` " \ space ;) -- which a
// hostile registry.State or DataDir can absolutely contain (e.g.
// CANTON_DEVKIT_REGISTRY pointed at a path with `$(rm -rf ~)` in
// it would otherwise be evaluated at source-time).
//
// Single quotes neutralise all shell expansion. The only character
// that needs escaping inside single quotes is the single quote
// itself; we do the standard dance for that.
//
// Header comments document the instance + the eval idiom so the
// output remains useful when piped to a file the user reads later.
func writeEnvShell(w io.Writer, ex apitypes.EnvExport) error {
	if _, err := fmt.Fprintf(w, "# Canton DevKit · localnet %s\n", shellQuote(ex.Instance)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# Source these in your app or CI step:"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "#   eval \"$(dpm localnet env --name %s)\"\n", shellQuote(ex.Instance)); err != nil {
		return err
	}
	for _, k := range sortedKeys(ex.Vars) {
		if _, err := fmt.Fprintf(w, "export %s=%s\n", k, shellQuote(ex.Vars[k])); err != nil {
			return err
		}
	}
	return nil
}

// writeEnvDotenv prints KEY="value" with $/\/`/" escapes per the
// dotenv specification (https://hexdocs.pm/dotenvy/dotenv-file-format.html).
// Distinct from writeEnvShell because dotenv-aware parsers (Node,
// Python, Vite) prefer double-quoted form so they can interpolate
// $VAR if the user opts in by writing it literally. We never emit
// $ ourselves -- interpolation is only possible for values that
// were already $-quoted in the registry, which our writer values
// never are.
func writeEnvDotenv(w io.Writer, ex apitypes.EnvExport) error {
	if _, err := fmt.Fprintf(w, "# Canton DevKit · localnet %s\n", ex.Instance); err != nil {
		return err
	}
	for _, k := range sortedKeys(ex.Vars) {
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, dotenvQuote(ex.Vars[k])); err != nil {
			return err
		}
	}
	return nil
}

// shellQuote returns a POSIX-safe single-quoted form of s. The
// only escape needed inside '...' is the single quote itself.
// Empty string is "”" so the value is unambiguously present.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dotenvQuote returns a dotenv-safe double-quoted form of s. We
// escape: backslash, double quote, dollar (interpolation token),
// and backtick (some parsers treat it as command substitution).
// Newlines stay literal -- dotenv-spec requires preserving them
// inside double quotes.
func dotenvQuote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
		"`", "\\`",
	)
	return `"` + r.Replace(s) + `"`
}

// writeEnvJSON serialises EnvExport. We use indented JSON so the
// `--format=json` output is a usable diff artefact when stashed in
// a fixture or CI log.
func writeEnvJSON(w io.Writer, ex apitypes.EnvExport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ex)
}

// sortedKeys returns map keys in stable lexical order so two calls
// produce identical output (essential for `git diff` and CI golden
// comparisons).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
