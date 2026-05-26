package localnet

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/spf13/cobra"
)

// `localnet env --name <name>` prints shell-exportable KEY=value lines
// for the named instance. Usage:
//
//	eval $(canton-devkit localnet env --name pebble)
//
// This is the v1 wedge (registry-only, no live docker probe). PR #34
// (BIT-125) ships the richer version with fish + powershell formats,
// dotenv quoting, the EnvExport types-package move, and the security-
// hardening I flagged in that PR's reviews. When PR #34 lands, this
// file is replaced wholesale.
//
// # Quoting (PR #34 #1 lesson re-applied)
//
// Every value goes through shellQuote — single-quoted with embedded
// `'` escaped as `'\''`. Round-trip tested: a value containing
// `$(rm -rf ~)` lands inside the quotes inert. The user reported
// regression in PR #34's review on this exact issue; we don't ship
// the wedge without the same defence.
//
// # JWT redaction (PR #34 #2 / PR #38 lesson re-applied)
//
// JWTs are excluded by default. `--include-jwt` opts in. The redaction
// pattern mirrors PR #38's `--include-jwt` flag so the two surfaces
// stay consistent for the user.
func buildEnv() *cobra.Command {
	var (
		name       string
		includeJWT bool
	)
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print shell-exportable env vars for an instance",
		Long: "Emits KEY=value lines suitable for `eval $(canton-devkit " +
			"localnet env --name <name>)`. Every value is single-quoted " +
			"and `'`-escaped so adversarial values in the registry " +
			"(unlikely but defended) can't reach eval as code. JWTs " +
			"are excluded unless --include-jwt is set.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			st, err := registry.Read(name)
			if err != nil {
				return fmt.Errorf("read %s: %w", name, err)
			}
			writeEnv(cmd.OutOrStdout(), st, includeJWT)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance name (required)")
	cmd.Flags().BoolVar(&includeJWT, "include-jwt", false,
		"Include CANTON_*_JWT vars. WARNING: tokens go through argv "+
			"to the parent shell — visible in shell history and `ps`. "+
			"Prefer reading the auth file: `cat $(canton-devkit localnet "+
			"status --name <name> --json | jq -r '.data_dir')/auth.json`.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// writeEnv produces sorted, shell-quoted KEY=value lines. Sorted so
// diff output (`localnet env --name a | diff localnet env --name b`)
// is stable across map iteration orders, and so the file ends up
// reviewable when piped to `tee env.sh`.
func writeEnv(out io.Writer, st *registry.State, includeJWT bool) {
	pairs := map[string]string{
		"CANTON_DEVKIT_INSTANCE":      st.Name,
		"CANTON_DEVKIT_SPLICE_VERSION": st.SpliceVersion,
		"CANTON_DEVKIT_DATA_DIR":       st.DataDir,
	}

	// Per-endpoint URL vars. Names match the convention used inside
	// Splice's compose env (APP_USER_UI_PORT etc) but prefixed with
	// CANTON_DEVKIT_ so they don't collide with what Splice already
	// writes into the container env.
	for key, port := range st.Ports {
		envKey := "CANTON_DEVKIT_" + strings.ToUpper(key) + "_URL"
		scheme := "http"
		if key == "postgres" {
			scheme = "postgresql"
		}
		pairs[envKey] = fmt.Sprintf("%s://localhost:%d", scheme, port)
	}

	// Per-role party + JWT vars. PARTY always; JWT only when the
	// user opts in via --include-jwt (BIT-125 + PR #38 redact-default
	// pattern). The "redacted" placeholder string is deliberately
	// readable so `eval` doesn't fail on something cryptic — pasting
	// `<redacted>` as a Bearer token gets an obvious 401 from the
	// participant.
	for role, cred := range st.Credentials {
		roleKey := strings.ReplaceAll(strings.ToUpper(role), "-", "_")
		pairs["CANTON_DEVKIT_"+roleKey+"_PARTY"] = cred.User
		if includeJWT {
			pairs["CANTON_DEVKIT_"+roleKey+"_JWT"] = cred.JWT
		}
	}

	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Fprintf(out, "# canton-devkit · %s · splice %s\n",
		st.Name, st.SpliceVersion)
	if !includeJWT {
		fmt.Fprintln(out,
			"# JWTs omitted by default. Re-run with --include-jwt to include them.")
	}
	for _, k := range keys {
		fmt.Fprintf(out, "export %s=%s\n", k, shellQuote(pairs[k]))
	}
}

// shellQuote wraps s in single quotes and escapes any embedded `'`
// using the POSIX `'\''` idiom. The result round-trips through
// `eval` byte-for-byte for any input that doesn't contain a NUL —
// which the registry can't produce because state.json is JSON.
//
// Empty input yields `''` (a valid empty shell string).
//
// Same shape as the helper PR #34 reviewers asked for in round 1;
// kept identical so the proper PR #34 impl is a drop-in replacement.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
