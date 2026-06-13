// Package darops holds the neutral DAR-management business logic
// shared by both the CLI (`internal/cli/localnet/dar`) and the Web UI
// handlers (`internal/ui/handlers/dar.go`).
//
// Before this package existed the two surfaces hand-rolled the same
// three things independently and drifted:
//
//   - the role → "participant_admin_<role>" port lookup + per-role JWT
//     resolution (CLI connect.go vs the handler's inline copy, twice),
//   - the "is this DAR vetted on participant X?" probe (the UI faked
//     it; the CLI had no surface at all — issues #18/#53/#79),
//   - the upload fan-out shape.
//
// Centralising them here means the guards and wire shapes can't drift
// (AGENTS.md "Mirror the guards" / "Share the business logic"). The
// shared wire structs live in internal/api/types.
package darops

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"google.golang.org/grpc"
)

// Roles is the Splice LocalNet participant topology, in the stable
// order both surfaces iterate. Exported so callers don't re-spell the
// literals (the prior cause of drift between the CLI's
// {sv, app-provider, app-user} order and the handler's).
var Roles = []string{"app-user", "app-provider", "sv"}

// validRole is the membership set behind ValidateRole. Mirrors the
// Web UI handler's `validRole` map and the CLI's --role default so a
// typo'd role is rejected identically on both surfaces.
var validRole = func() map[string]bool {
	m := make(map[string]bool, len(Roles))
	for _, r := range Roles {
		m[r] = true
	}
	return m
}()

// ValidateRole rejects any role outside the Splice LocalNet topology.
// Both surfaces call this so the guard can't drift.
func ValidateRole(role string) error {
	if !validRole[role] {
		return fmt.Errorf("invalid role %q: must be one of app-user, app-provider, sv", role)
	}
	return nil
}

// ErrPortNotRecorded is returned by ResolveParticipant when the
// instance state lacks the per-role participant admin port — the
// instance was brought up before that capture landed and needs a
// down→up. Callers map it to their surface's "re-up to capture ports"
// remediation (the CLI prints a hint; the UI returns 503
// PARTICIPANT_PORT_NOT_RECORDED).
type ErrPortNotRecorded struct {
	Role string
}

func (e *ErrPortNotRecorded) Error() string {
	return "participant_admin port not recorded for role " + e.Role +
		" — restart the instance to capture Canton API ports"
}

// ErrNoCredential is returned when state has a port for the role but
// no captured JWT. For a healthy instance captureCredentials fills
// every recorded role, so this signals genuine state corruption.
type ErrNoCredential struct {
	Role string
}

func (e *ErrNoCredential) Error() string {
	return "no JWT recorded for role " + e.Role
}

// ResolveParticipant derives the admin.Config (host + bearer JWT) for
// one participant role from registry state. This is the single
// implementation of the port/JWT lookup both surfaces previously
// duplicated.
//
// insecure is passed through to the returned Config; Splice LocalNet
// is plaintext so callers pass true. A non-empty token argument
// overrides the captured JWT (the CLI's --token escape hatch); pass ""
// to use the registry credential.
func ResolveParticipant(state *registry.State, role, token string, insecure bool) (admin.Config, error) {
	if err := ValidateRole(role); err != nil {
		return admin.Config{}, err
	}
	portKey := "participant_admin_" + role
	port, ok := state.Ports[portKey]
	if !ok || port == 0 {
		return admin.Config{}, &ErrPortNotRecorded{Role: role}
	}
	if token == "" {
		cred, ok := state.Credentials[role]
		if !ok {
			return admin.Config{}, &ErrNoCredential{Role: role}
		}
		token = cred.JWT
	}
	return admin.Config{
		Host:     "localhost:" + strconv.Itoa(port),
		Token:    token,
		Insecure: insecure,
	}, nil
}

// PackageLister is the subset of the Canton admin PackageService that
// the vetting probe needs. adminproto.PackageServiceClient (and hence
// *admin.Client.Package) satisfies it; tests inject a stub so the
// vetting logic is exercised without a network. The signature matches
// the generated client exactly so the real client is assignable.
type PackageLister interface {
	ListDars(ctx context.Context, in *adminproto.ListDarsRequest, opts ...grpc.CallOption) (*adminproto.ListDarsResponse, error)
}

// Compile-time check that the real generated client satisfies the
// narrow interface — DialAdmin returns client.Package and would only
// fail at runtime otherwise.
var _ PackageLister = adminproto.PackageServiceClient(nil)

// Dialer opens a PackageLister for a resolved admin.Config. Production
// callers pass DialAdmin (real gRPC); tests pass a stub dialer.
type Dialer func(ctx context.Context, cfg admin.Config) (PackageLister, func() error, error)

// DialAdmin is the production Dialer: it dials the Canton admin API
// and returns the client's PackageService plus its Close func.
func DialAdmin(ctx context.Context, cfg admin.Config) (PackageLister, func() error, error) {
	client, err := admin.Connect(ctx, cfg)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return client.Package, client.Close, nil
}

// isVetted reports whether mainID appears in the participant's
// ListDars response. We have no native "is this DAR vetted?" RPC; a
// DAR uploaded with vet_all_packages=true is present in ListDars iff
// it's vetted (the participant prunes unvetted DARs), so membership is
// a faithful dev-flow signal. This is the same heuristic the UI's
// handleDARVettingList used inline — now shared so the CLI column and
// the UI agree.
func isVetted(dars []*adminproto.DarDescription, mainID string) bool {
	for _, d := range dars {
		if d.GetMain() == mainID {
			return true
		}
	}
	return false
}

// ListVetting probes every participant role for whether the given DAR
// (by main package id) is vetted. Per-role failures are recorded in
// the row's Error field rather than aborting — a 2-of-3 answer is more
// useful than none, and "unknown" is honest where the old UI claimed
// "vetted" unconditionally.
//
// Roles are probed in the stable Roles order so output is
// deterministic regardless of map iteration.
func ListVetting(ctx context.Context, dial Dialer, state *registry.State, mainID string) []types.DARVettingRow {
	rows := make([]types.DARVettingRow, 0, len(Roles))
	for _, role := range Roles {
		row := types.DARVettingRow{Role: role}
		cfg, err := ResolveParticipant(state, role, "", true)
		if err != nil {
			row.Error = shortResolveError(err)
			rows = append(rows, row)
			continue
		}
		lister, closeFn, err := dial(ctx, cfg)
		if err != nil {
			row.Error = "dial: " + err.Error()
			rows = append(rows, row)
			continue
		}
		resp, lerr := lister.ListDars(ctx, &adminproto.ListDarsRequest{})
		_ = closeFn()
		if lerr != nil {
			row.Error = "list: " + lerr.Error()
			rows = append(rows, row)
			continue
		}
		row.Vetted = isVetted(resp.GetDars(), mainID)
		rows = append(rows, row)
	}
	return rows
}

// shortResolveError maps the typed resolve errors to the terse
// per-row strings the vetting UI expects ("port not recorded" / "no
// JWT"), falling back to the full message for anything else.
func shortResolveError(err error) string {
	var portErr *ErrPortNotRecorded
	if errors.As(err, &portErr) {
		return "port not recorded"
	}
	var credErr *ErrNoCredential
	if errors.As(err, &credErr) {
		return "no JWT"
	}
	return err.Error()
}
