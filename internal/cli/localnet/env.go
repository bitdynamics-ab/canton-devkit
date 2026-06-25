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
//	eval "$(dpm localnet env hubble)"
//
// Four formats:
//
//	shell (default)  export KEY='value' with POSIX quoting
//	dotenv           KEY="value" with dotenv escaping
//	github-env       bare KEY=value for $GITHUB_ENV (>> in a workflow step)
//	json             api/types.EnvExport for scripted consumers
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
		Use:   "env [name]",
		Short: "Export LocalNet endpoints and credentials as env vars",
		Long: `Prints an exported KEY=value block summarising the named LocalNet
instance -- every host port from the Ports map (Ledger / JSON /
admin APIs per role, wallet UIs, scan UI, postgres) plus the
user/audience for each captured credential and the real on-ledger
party ids. Use with:

  eval "$(dpm localnet env hubble)"

Formats:

  shell       POSIX single-quoted export KEY='value'  (default; eval-safe)
  dotenv      double-quoted KEY="value" with $/\/" escapes (dotenv spec)
  github-env  bare KEY=value lines for the GitHub Actions environment file:
                dpm localnet env --name ci --format github-env >> "$GITHUB_ENV"
              (shell/dotenv carry comments + quotes that $GITHUB_ENV rejects)
  json        machine-readable shape; matches the Web UI handler

JWTs are REDACTED by default (CANTON_<ROLE>_JWT=<redacted>) so
CI logs / shared terminals don't leak the dev-only signing
secret. Pass --include-jwt to opt in before piping or sourcing
raw token values.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveName(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			name = resolved
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
			case "github-env":
				return writeEnvGithub(cmd.OutOrStdout(), ex)
			case "json":
				return writeEnvJSON(cmd.OutOrStdout(), ex)
			default:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"--format must be shell, dotenv, github-env, or json (got %q)\n", format)
				return localnet.AsExitError(localnet.ExitUserError)
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "",
		"Identifier of the LocalNet instance to export. Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&format, "format", "shell",
		"Output format: shell | dotenv | github-env | json")
	cmd.Flags().BoolVar(&includeJWT, "include-jwt", false,
		"Emit raw CANTON_<ROLE>_JWT values. Default is <redacted>.")
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
	if _, err := fmt.Fprintf(w, "#   eval \"$(dpm localnet env %s)\"\n", shellQuote(ex.Instance)); err != nil {
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

// writeEnvGithub prints bare KEY=value lines for the GitHub Actions
// environment file. A workflow step appends them with
//
//	dpm localnet env --name ci --format github-env >> "$GITHUB_ENV"
//
// and every subsequent step sees them as env vars. This is a distinct
// format because the runner parses $GITHUB_ENV itself, NOT through a
// shell: it rejects the `#` comment header writeEnvShell/writeEnvDotenv
// emit ("Invalid format"), and any surrounding quotes (export KEY='v',
// KEY="v") become literal characters in the value. So we emit no
// header, no `export`, and no quoting -- the value is taken verbatim to
// end of line.
//
// A value containing a newline (a hostile DataDir can) cannot use the
// single-line form -- the runner would treat the second line as a new
// KEY=value. GitHub documents a heredoc form for that case:
//
//	KEY<<delimiter
//	<value, possibly multi-line>
//	delimiter
//
// We pick a delimiter that does not occur in the value so it can't be
// closed early (a documented injection vector for the multi-line form).
func writeEnvGithub(w io.Writer, ex apitypes.EnvExport) error {
	for _, k := range sortedKeys(ex.Vars) {
		v := ex.Vars[k]
		if !strings.ContainsAny(v, "\r\n") {
			if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
				return err
			}
			continue
		}
		delim := githubEnvDelimiter(v)
		if _, err := fmt.Fprintf(w, "%s<<%s\n%s\n%s\n", k, delim, v, delim); err != nil {
			return err
		}
	}
	return nil
}

// githubEnvDelimiter returns a heredoc delimiter guaranteed not to
// appear as a line in value, so the GitHub Actions multi-line env
// syntax (KEY<<delim ... delim) cannot be terminated early by hostile
// content. Starts from a fixed token and widens until it's absent.
func githubEnvDelimiter(value string) string {
	delim := "CANTON_DEVKIT_EOF"
	for strings.Contains(value, delim) {
		delim += "_"
	}
	return delim
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
