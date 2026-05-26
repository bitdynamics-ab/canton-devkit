package localnet

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// `localnet status --name <name>` surfaces the registry-backed view
// of one instance: lifecycle status, allocated ports, instance files,
// captured credentials (redacted by default).
//
// This is the registry-only quick-look — the richer live-docker probe
// lives in PR #38 (BIT-144). When that PR lands, this implementation
// gets promoted to call into the live prober; until then it answers
// from the JSON state file alone, which is honest about being a
// cached view but always succeeds without needing Docker reachable.
//
// JWT redaction: per PR #38 / #43, full tokens stay hidden unless
// --include-jwt is set. Default-redacted prevents shell-history leaks
// for the most common diagnostic command.
func buildStatus() *cobra.Command {
	var (
		name       string
		jsonOut    bool
		includeJWT bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Canton LocalNet status",
		Long: "Print the registry-backed view of a LocalNet instance: " +
			"lifecycle status, ports, instance files, and captured " +
			"credentials. JWTs redacted by default; pass --include-jwt " +
			"to surface them.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required (run `localnet list` to see available instances)")
			}
			st, err := registry.Read(name)
			if err != nil {
				return fmt.Errorf("read %s: %w", name, err)
			}
			if jsonOut {
				return writeStatusJSON(cmd.OutOrStdout(), st, includeJWT)
			}
			writeStatusText(cmd.OutOrStdout(), st, includeJWT)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance name (required)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Machine-readable output")
	cmd.Flags().BoolVar(&includeJWT, "include-jwt", false,
		"Surface full JWTs in output (default: redacted with last 8 chars only)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func writeStatusText(out io.Writer, st *registry.State, includeJWT bool) {
	hdr := fmt.Sprintf("%s · %s · %s", st.Name, st.SpliceVersion, statusBadge(st.Status))
	fmt.Fprintln(out)
	fmt.Fprintln(out, term.Section(hdr, "", "", 0))

	if len(st.Ports) > 0 {
		fmt.Fprintln(out, term.Dimc("Endpoints"))
		for _, e := range statusEndpointOrder {
			port, ok := st.Ports[e.key]
			if !ok {
				continue
			}
			url := fmt.Sprintf("%s://localhost:%d", e.scheme, port)
			fmt.Fprintln(out, term.KV(e.label, url, 26))
		}
		fmt.Fprintln(out)
	}

	if len(st.Credentials) > 0 {
		fmt.Fprintln(out, term.Dimc("Credentials"))
		for role, cred := range st.Credentials {
			tok := redactJWT(cred.JWT)
			if includeJWT {
				tok = cred.JWT
			}
			fmt.Fprintln(out, term.KV(role+" JWT", tok, 26))
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, term.Dimc("Instance files"))
	fmt.Fprintln(out, term.KV("State", registry.PathFor(st.Name), 26))
	fmt.Fprintln(out, term.KV("Data dir", st.DataDir, 26))
	fmt.Fprintln(out, term.KV("Project dir", st.ProjectDir, 26))
	fmt.Fprintln(out, term.KV("Compose project", st.ComposeProject, 26))
	if st.CreatedAt != "" {
		fmt.Fprintln(out, term.KV("Created", st.CreatedAt, 26))
	}
	fmt.Fprintln(out)
}

// statusEndpointOrder is the deterministic pretty-print order for the
// status endpoint listing. Duplicates the slice in internal/localnet/up.go's
// orderedEndpointKeys (which is in a DIFFERENT Go package — same name,
// different import path — and exporting it would close a small import
// cycle). Keep these two in lockstep until the consolidating refactor
// in PR #38 lands a shared types package.
var statusEndpointOrder = []struct {
	key, label, scheme string
}{
	{"app_user_ui", "Wallet (app-user)", "http"},
	{"app_provider_ui", "Wallet (app-provider)", "http"},
	{"sv_ui", "Wallet (super-validator)", "http"},
	{"swagger_ui", "Swagger (OpenAPI)", "http"},
	{"postgres", "Postgres", "postgresql"},
}

func statusBadge(s registry.Status) string {
	switch s {
	case registry.StatusRunning:
		return term.Successc("● running")
	case registry.StatusFailed:
		return term.Errorc("✗ failed")
	case registry.StatusPartial:
		return term.Warnc("◐ partial")
	case registry.StatusStopped:
		return term.Dimc("○ stopped")
	case registry.StatusCreating:
		return term.Brandc("◐ creating")
	case registry.StatusStopping:
		return term.Dimc("◐ stopping")
	default:
		return term.Dimc(string(s))
	}
}

// redactJWT keeps the last 8 chars so users can disambiguate which
// role they're looking at without revealing full signing material.
// Symmetric with PR #38 / PR #43's redaction discipline.
func redactJWT(jwt string) string {
	if len(jwt) <= 8 {
		return "<redacted>"
	}
	return "<redacted …" + jwt[len(jwt)-8:] + ">"
}

type statusJSON struct {
	SchemaVersion int                            `json:"schema_version"`
	Name          string                         `json:"name"`
	SpliceVersion string                         `json:"splice_version"`
	Status        string                         `json:"status"`
	CreatedAt     string                         `json:"created_at,omitempty"`
	Ports         map[string]int                 `json:"ports,omitempty"`
	Credentials   map[string]statusCredentialOut `json:"credentials,omitempty"`
	State         string                         `json:"state_path"`
	DataDir       string                         `json:"data_dir,omitempty"`
	ProjectDir    string                         `json:"project_dir,omitempty"`
}

type statusCredentialOut struct {
	Role     string `json:"role"`
	User     string `json:"user"`
	Audience string `json:"audience"`
	JWT      string `json:"jwt"`
}

func writeStatusJSON(out io.Writer, st *registry.State, includeJWT bool) error {
	creds := make(map[string]statusCredentialOut, len(st.Credentials))
	for role, c := range st.Credentials {
		tok := redactJWT(c.JWT)
		if includeJWT {
			tok = c.JWT
		}
		creds[role] = statusCredentialOut{Role: c.Role, User: c.User, Audience: c.Audience, JWT: tok}
	}
	payload := statusJSON{
		SchemaVersion: registry.SchemaVersion,
		Name:          st.Name,
		SpliceVersion: st.SpliceVersion,
		Status:        string(st.Status),
		CreatedAt:     st.CreatedAt,
		Ports:         st.Ports,
		Credentials:   creds,
		State:         registry.PathFor(st.Name),
		DataDir:       st.DataDir,
		ProjectDir:    st.ProjectDir,
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
