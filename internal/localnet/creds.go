package localnet

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// CredsOptions captures `localnet creds` flags.
type CredsOptions struct {
	Name   string
	Role   string // empty = all
	Format string // "table" (default), "env", "json", "raw"
}

// ValidateCredsOptions enforces the format whitelist + the raw-needs-role
// rule. Called by the Cobra builder after flag parsing.
func ValidateCredsOptions(opts *CredsOptions) error {
	if err := ValidateFormat(opts.Format, "table", "env", "json", "raw"); err != nil {
		return err
	}
	if opts.Role != "" && !validCredsRole(opts.Role) {
		return fmt.Errorf("--role must be one of [sv app-provider app-user] (got %q)", opts.Role)
	}
	if opts.Format == "raw" && opts.Role == "" {
		return fmt.Errorf("--format raw requires --role")
	}
	return nil
}

func validCredsRole(role string) bool {
	switch role {
	case "sv", "app-provider", "app-user":
		return true
	default:
		return false
	}
}

// RunCreds prints captured JWTs for the named instance. Exit codes:
//   - 0 success
//   - 1 invalid args, instance not registered, no creds captured, or invalid role
func RunCreds(out io.Writer, errw io.Writer, opts *CredsOptions) int {
	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(errw, "No instance named %q is registered.\n", opts.Name)
		return ExitUserError
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}
	if len(state.Credentials) == 0 {
		_, _ = fmt.Fprintf(errw, "No credentials captured for instance %q.\n", opts.Name)
		_, _ = fmt.Fprintln(errw, "(JWT capture runs at `localnet up` time — re-run if the instance pre-dates this feature.)")
		return ExitUserError
	}

	selected := filterCredentials(state.Credentials, opts.Role)
	if len(selected) == 0 {
		_, _ = fmt.Fprintf(errw, "No credentials for role %q on instance %q.\n", opts.Role, opts.Name)
		return ExitUserError
	}

	switch opts.Format {
	case "json":
		printCredsJSON(out, selected)
	case "env":
		printCredsEnv(out, selected)
	case "raw":
		// Single role guaranteed by ValidateCredsOptions.
		for _, c := range selected {
			_, _ = fmt.Fprintln(out, c.JWT)
		}
	default:
		printCredsTable(out, selected)
	}
	return ExitSuccess
}

// filterCredentials returns the slice of creds matching role (or all if
// role == ""), sorted by role for deterministic output.
func filterCredentials(in map[string]registry.Credential, role string) []registry.Credential {
	out := make([]registry.Credential, 0, len(in))
	for k, c := range in {
		if role == "" || role == k {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func printCredsTable(out io.Writer, creds []registry.Credential) {
	_, _ = fmt.Fprintf(out, "%-15s %-25s %s\n", "ROLE", "USER", "AUDIENCE")
	for _, c := range creds {
		_, _ = fmt.Fprintf(out, "%-15s %-25s %s\n", c.Role, c.User, c.Audience)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Use --format env to export tokens, --format raw --role <r> for a single JWT.")
}

func printCredsEnv(out io.Writer, creds []registry.Credential) {
	for _, c := range creds {
		// Stable env var name: AUTH_<UPPER_ROLE>_TOKEN, with hyphens
		// becoming underscores. Matches Splice's own console
		// entrypoint naming.
		envName := "AUTH_" + strings.ReplaceAll(strings.ToUpper(c.Role), "-", "_") + "_TOKEN"
		_, _ = fmt.Fprintf(out, "export %s=%q\n", envName, c.JWT)
	}
}

func printCredsJSON(out io.Writer, creds []registry.Credential) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(creds)
}
