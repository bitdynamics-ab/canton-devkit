package darops

import (
	"context"
	"errors"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"google.golang.org/grpc"
)

// stubLister is an in-memory PackageLister. Each role's dial returns a
// stubLister preloaded with that participant's ListDars response (or
// an error), so the vetting probe is exercised with no network.
type stubLister struct {
	dars    []*adminproto.DarDescription
	listErr error
	calls   int
}

func (s *stubLister) ListDars(_ context.Context, _ *adminproto.ListDarsRequest, _ ...grpc.CallOption) (*adminproto.ListDarsResponse, error) {
	s.calls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &adminproto.ListDarsResponse{Dars: s.dars}, nil
}

func noopClose() error { return nil }

// stateWith builds a registry.State carrying admin ports + JWTs for
// the given roles, in order from base port 2900. Roles not listed are
// absent (port-not-recorded).
func stateWith(roles ...string) *registry.State {
	st := &registry.State{
		Ports:       map[string]int{},
		Credentials: map[string]registry.Credential{},
	}
	for i, r := range roles {
		st.Ports["participant_admin_"+r] = 2900 + i
		st.Credentials[r] = registry.Credential{Role: r, JWT: "jwt-" + r}
	}
	return st
}

func TestValidateRole(t *testing.T) {
	for _, r := range []string{"app-user", "app-provider", "sv"} {
		if err := ValidateRole(r); err != nil {
			t.Errorf("ValidateRole(%q) = %v, want nil", r, err)
		}
	}
	for _, r := range []string{"", "observabilty", "App-User", "admin"} {
		if err := ValidateRole(r); err == nil {
			t.Errorf("ValidateRole(%q) = nil, want error", r)
		}
	}
}

func TestResolveParticipant_HappyPath(t *testing.T) {
	st := stateWith("app-user")
	cfg, err := ResolveParticipant(st, "app-user", "", true)
	if err != nil {
		t.Fatalf("ResolveParticipant: %v", err)
	}
	if cfg.Host != "localhost:2900" {
		t.Errorf("Host = %q, want localhost:2900", cfg.Host)
	}
	if cfg.Token != "jwt-app-user" {
		t.Errorf("Token = %q, want jwt-app-user", cfg.Token)
	}
	if !cfg.Insecure {
		t.Errorf("Insecure = false, want true")
	}
}

func TestResolveParticipant_TokenOverride(t *testing.T) {
	st := stateWith("sv")
	cfg, err := ResolveParticipant(st, "sv", "explicit-token", false)
	if err != nil {
		t.Fatalf("ResolveParticipant: %v", err)
	}
	if cfg.Token != "explicit-token" {
		t.Errorf("Token = %q, want explicit-token (override should win)", cfg.Token)
	}
	if cfg.Insecure {
		t.Errorf("Insecure = true, want false")
	}
}

func TestResolveParticipant_PortNotRecorded(t *testing.T) {
	st := stateWith("app-user") // no app-provider port
	_, err := ResolveParticipant(st, "app-provider", "", true)
	var portErr *ErrPortNotRecorded
	if !errors.As(err, &portErr) {
		t.Fatalf("want *ErrPortNotRecorded, got %v", err)
	}
	if portErr.Role != "app-provider" {
		t.Errorf("ErrPortNotRecorded.Role = %q, want app-provider", portErr.Role)
	}
}

func TestResolveParticipant_NoCredential(t *testing.T) {
	st := stateWith("app-user")
	delete(st.Credentials, "app-user") // port present, JWT missing
	_, err := ResolveParticipant(st, "app-user", "", true)
	var credErr *ErrNoCredential
	if !errors.As(err, &credErr) {
		t.Fatalf("want *ErrNoCredential, got %v", err)
	}
}

func TestResolveParticipant_InvalidRole(t *testing.T) {
	st := stateWith("app-user")
	if _, err := ResolveParticipant(st, "bogus", "", true); err == nil {
		t.Fatal("ResolveParticipant with bogus role: want error, got nil")
	}
}

// TestListVetting_PerParticipant exercises the core vetting probe: the
// DAR is vetted on app-user (present in its ListDars), not on
// app-provider (absent), and sv can't be probed (port not recorded).
func TestListVetting_PerParticipant(t *testing.T) {
	const main = "abc123"
	st := stateWith("app-user", "app-provider") // sv intentionally missing

	dialer := func(_ context.Context, cfg admin.Config) (PackageLister, func() error, error) {
		switch cfg.Host {
		case "localhost:2900": // app-user — vetted
			return &stubLister{dars: []*adminproto.DarDescription{{Main: main}}}, noopClose, nil
		case "localhost:2901": // app-provider — not vetted
			return &stubLister{dars: []*adminproto.DarDescription{{Main: "other"}}}, noopClose, nil
		default:
			t.Fatalf("unexpected dial host %q", cfg.Host)
			return nil, noopClose, nil
		}
	}

	rows := ListVetting(context.Background(), dialer, st, main)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (one per role), got %d", len(rows))
	}
	// Stable order: app-user, app-provider, sv.
	if rows[0].Role != "app-user" || rows[1].Role != "app-provider" || rows[2].Role != "sv" {
		t.Fatalf("rows not in canonical order: %v %v %v", rows[0].Role, rows[1].Role, rows[2].Role)
	}
	if !rows[0].Vetted {
		t.Errorf("app-user: want vetted=true")
	}
	if rows[1].Vetted {
		t.Errorf("app-provider: want vetted=false (DAR absent)")
	}
	if rows[2].Vetted || rows[2].Error != "port not recorded" {
		t.Errorf("sv: want vetted=false error=%q, got vetted=%v error=%q",
			"port not recorded", rows[2].Vetted, rows[2].Error)
	}
}

// TestListVetting_DialError records a per-row dial failure rather than
// aborting the whole probe — a 2-of-3 answer beats none.
func TestListVetting_DialError(t *testing.T) {
	const main = "abc123"
	st := stateWith("app-user", "app-provider", "sv")
	dialer := func(_ context.Context, cfg admin.Config) (PackageLister, func() error, error) {
		if cfg.Host == "localhost:2901" { // app-provider down
			return nil, noopClose, errors.New("connection refused")
		}
		return &stubLister{dars: []*adminproto.DarDescription{{Main: main}}}, noopClose, nil
	}
	rows := ListVetting(context.Background(), dialer, st, main)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[1].Error == "" {
		t.Errorf("app-provider: want a dial error recorded, got none")
	}
	if !rows[0].Vetted || !rows[2].Vetted {
		t.Errorf("app-user/sv should still be probed: %+v", rows)
	}
}

// TestListVetting_ListError records a per-row ListDars RPC failure.
func TestListVetting_ListError(t *testing.T) {
	const main = "abc123"
	st := stateWith("app-user")
	dialer := func(_ context.Context, _ admin.Config) (PackageLister, func() error, error) {
		return &stubLister{listErr: errors.New("unavailable")}, noopClose, nil
	}
	rows := ListVetting(context.Background(), dialer, st, main)
	if rows[0].Error == "" {
		t.Errorf("want list error recorded for app-user, got none")
	}
	if rows[0].Vetted {
		t.Errorf("want vetted=false on list error")
	}
}

// TestListVetting_AllPresent confirms the happy path across all three
// participants when every role is recorded and the DAR is vetted
// everywhere.
func TestListVetting_AllPresent(t *testing.T) {
	const main = "deadbeef"
	st := stateWith("app-user", "app-provider", "sv")
	dialer := func(_ context.Context, _ admin.Config) (PackageLister, func() error, error) {
		return &stubLister{dars: []*adminproto.DarDescription{{Main: main}}}, noopClose, nil
	}
	rows := ListVetting(context.Background(), dialer, st, main)
	for _, r := range rows {
		if !r.Vetted || r.Error != "" {
			t.Errorf("role %s: want vetted=true error=\"\", got vetted=%v error=%q",
				r.Role, r.Vetted, r.Error)
		}
	}
}
