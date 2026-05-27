package dar

import (
	"context"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	"github.com/spf13/cobra"
)

// connectFlags captures the connection-flag shape shared by every
// participant-touching dar subcommand (upload / list / download /
// remove / build-upload / watch). Embedded in each builder's struct
// so all six commands stay consistent.
type connectFlags struct {
	// AdminHost is the participant Admin API target ("host:port").
	// Primary flag; overrides any --instance resolution.
	AdminHost string
	// Token is the bearer JWT. Sent on every RPC.
	Token string
	// Insecure forces plaintext gRPC. Default true because Splice
	// LocalNet is plaintext; production should pass --insecure=false
	// plus --ca-cert.
	Insecure bool
	// CACertFile is a PEM path for the server's CA when not Insecure.
	CACertFile string
	// Instance is the DevKit registry instance name. When set
	// without --admin-host, we look up the admin port from
	// state.json's port map. When state lacks the participant admin
	// port (the current Splice LocalNet integration in PR #14 only
	// records UI ports), the command surfaces a clear error
	// pointing the user at --admin-host.
	Instance string
	// Role selects which participant within the named instance to
	// target. Splice LocalNet runs three participants (sv,
	// app-provider, app-user). Defaults to "app-user".
	Role string
}

// register adds the connection flags to a cobra command. Centralised
// so every dar-admin command exposes the same surface.
func (f *connectFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.AdminHost, "admin-host", "",
		"Participant Admin API target, host:port. Required unless --instance is set.")
	cmd.Flags().StringVar(&f.Token, "token", "",
		"Bearer JWT for the Admin API. For Splice LocalNet, use the per-role token from `localnet creds`.")
	cmd.Flags().BoolVar(&f.Insecure, "insecure", true,
		"Use plaintext gRPC (Splice LocalNet default). Disable for production.")
	cmd.Flags().StringVar(&f.CACertFile, "ca-cert", "",
		"PEM-encoded CA bundle for TLS verification (when --insecure=false).")
	cmd.Flags().StringVar(&f.Instance, "instance", "",
		"DevKit LocalNet instance name; resolves --admin-host + --token from the registry.")
	cmd.Flags().StringVar(&f.Role, "role", "app-user",
		"Participant role within --instance: sv, app-provider, or app-user.")
}

// resolve picks an effective (host, token) pair from the flags. If
// --admin-host is set explicitly it wins. Otherwise we look up
// --instance in the registry. Returns a connect.Config ready to pass
// to admin.Connect.
//
// Instance resolution caveats:
//   - The registry must contain a "participant_admin_<role>" port for
//     us to derive a host. Today's registry only persists UI ports
//     (PR #14 scope); when that's the case we error with a clear hint.
//   - The JWT is signed locally using splice.SignToken, derived from
//     the per-role auth env files in the cached compose project.
func (f *connectFlags) resolve() (admin.Config, error) {
	if f.AdminHost != "" {
		return admin.Config{
			Host:       f.AdminHost,
			Token:      f.Token,
			Insecure:   f.Insecure,
			CACertFile: f.CACertFile,
		}, nil
	}
	if f.Instance == "" {
		return admin.Config{}, fmt.Errorf("either --admin-host or --instance is required")
	}

	state, err := registry.Read(f.Instance)
	if err == registry.ErrNotFound {
		return admin.Config{}, fmt.Errorf("instance %q not registered (run `localnet up` first)", f.Instance)
	}
	if err != nil {
		return admin.Config{}, fmt.Errorf("read instance state: %w", err)
	}

	portKey := "participant_admin_" + f.Role
	port, ok := state.Ports[portKey]
	if !ok {
		// State doesn't yet record participant admin ports; surface a
		// clear next step rather than guess.
		return admin.Config{}, fmt.Errorf("instance %q has no recorded port for %q. "+
			"Pass --admin-host=localhost:<port> directly. "+
			"For Splice LocalNet on default ports, app-user is :2902, app-provider :3902, sv :4902",
			f.Instance, portKey)
	}

	// Token preference: explicit --token > captured creds from state.
	token := f.Token
	if token == "" {
		if cred, ok := state.Credentials[f.Role]; ok {
			token = cred.JWT
		}
	}

	return admin.Config{
		Host:       fmt.Sprintf("localhost:%d", port),
		Token:      token,
		Insecure:   f.Insecure,
		CACertFile: f.CACertFile,
	}, nil
}

// connect is shared by every RunE: resolve config, dial, return a
// closed-when-done Client. Caller is responsible for `defer
// client.Close()`.
func (f *connectFlags) connect(ctx context.Context) (*admin.Client, error) {
	cfg, err := f.resolve()
	if err != nil {
		return nil, err
	}
	return admin.Connect(ctx, cfg)
}

// signTokenForInstance derives a token from the per-role auth env
// files (`splice.SignToken`) when the registry doesn't already have
// one captured. Currently unused — RunUp captures these — but ready
// for future flows where the instance is bootstrapped externally.
//
//nolint:unused // intentional API surface for future re-use; see TODO.
func signTokenForInstance(state *registry.State, role string) (string, error) {
	inputs, err := splice.LoadCredentialInputs(state.ProjectDir)
	if err != nil {
		return "", err
	}
	for _, in := range inputs {
		if string(in.Role) == role {
			return splice.SignToken(in)
		}
	}
	return "", fmt.Errorf("no auth inputs found for role %q", role)
}
