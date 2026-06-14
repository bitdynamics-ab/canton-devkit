package localnet

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// fakeSpliceProjectDir builds a tempdir with the env/<role>-auth-on.env
// files captureCredentials expects so a fake-driven RunUp reaches the
// success path. Mirrors the setup in the happy-path test.
func fakeSpliceProjectDir(t *testing.T) string {
	t.Helper()
	projectDir := t.TempDir()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	for name, body := range map[string]string{
		"sv-auth-on.env":           "AUTH_SV_VALIDATOR_USER_NAME=u\nAUTH_SV_AUDIENCE=a\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=u\nAUTH_APP_PROVIDER_AUDIENCE=a\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_VALIDATOR_USER_NAME=u\nAUTH_APP_USER_AUDIENCE=a\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return projectDir
}

// TestValidateProfiles is the unit-level guard for the shared
// allowlist both surfaces use.
func TestValidateProfiles(t *testing.T) {
	cases := []struct {
		name     string
		profiles []string
		wantErr  bool
	}{
		{"nil ok", nil, false},
		{"empty ok", []string{}, false},
		{"observability ok", []string{ObservabilityProfileName}, false},
		{"per-component ok", []string{PrometheusProfileName, GrafanaProfileName}, false},
		{"tokens-v2 ok", []string{TokensV2ProfileName}, false},
		{"all known ok", KnownProfiles(), false},
		{"typo rejected", []string{"observabilty"}, true},
		{"arbitrary rejected", []string{"production"}, true},
		{"underscore variant rejected", []string{"tokens_v2"}, true},
		{"one bad among good", []string{ObservabilityProfileName, "nope"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProfiles(tc.profiles)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateProfiles(%v) err=%v, wantErr=%v", tc.profiles, err, tc.wantErr)
			}
			// On the error path the message must name the offending
			// profile and list the supported set so a CLI user can
			// self-correct.
			if tc.wantErr {
				msg := err.Error()
				if !strings.Contains(msg, "unknown profile") ||
					!strings.Contains(msg, ObservabilityProfileName) {
					t.Errorf("error message not actionable: %q", msg)
				}
			}
		})
	}
}

// TestRunUp_RejectsUnknownProfile: a typo'd profile must fail
// RunUp with ExitUserError BEFORE any docker work — mirroring the Web
// UI's 400 guard so `dpm localnet up --profile observabilty` can't
// silently produce a metric-less instance.
func TestRunUp_RejectsUnknownProfile(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	fetchCalled := false
	var out, errBuf bytes.Buffer
	code := RunUp(context.Background(),
		&TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          "typo",
			Version:       splice.LatestAlias,
			Profiles:      []string{"observabilty"}, // missing 'i'
			SkipPreflight: true,
			FetchFn: func(context.Context, splice.Version, string, io.Writer) (string, error) {
				fetchCalled = true
				return "", nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitUserError {
		t.Fatalf("RunUp with unknown profile = %d, want ExitUserError (%d)\nstderr=%q",
			code, ExitUserError, errBuf.String())
	}
	if fetchCalled {
		t.Error("profile validation must fail BEFORE fetch — no side effects on a typo")
	}
	if !strings.Contains(errBuf.String(), "unknown profile") {
		t.Errorf("expected an unknown-profile error on stderr, got %q", errBuf.String())
	}
}

// TestRunUp_PersistsProfiles: the resolved profile set is written
// into state.json so a later re-up can re-enable it without --profile.
func TestRunUp_PersistsProfiles(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	projectDir := fakeSpliceProjectDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errBuf bytes.Buffer
	code := RunUp(ctx, &TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          "obs",
			Version:       splice.LatestAlias,
			Profiles:      []string{ObservabilityProfileName},
			SkipPreflight: true,
			FetchFn: func(context.Context, splice.Version, string, io.Writer) (string, error) {
				return projectDir, nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess\nstderr=%q", code, errBuf.String())
	}
	state, err := registry.Read("obs")
	if err != nil {
		t.Fatalf("registry.Read: %v", err)
	}
	if len(state.Profiles) != 1 || state.Profiles[0] != ObservabilityProfileName {
		t.Errorf("state.Profiles = %v, want [%s]", state.Profiles, ObservabilityProfileName)
	}
	// Observability ports were allocated through the stable-reuse path.
	if p, ok := state.Ports["prometheus_ui"]; !ok || p <= 0 {
		t.Errorf("prometheus_ui port = %d (ok=%v); expected non-zero with observability profile", p, ok)
	}
}

// TestRunUp_InheritsProfilesOnReup: when the caller passes NO
// profiles but a prior up recorded some, RunUp re-enables the stored
// set — the down → up survival contract. This is the regression test
// for "observability vanishes on restart".
func TestRunUp_InheritsProfilesOnReup(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	projectDir := fakeSpliceProjectDir(t)

	const name = "survivor"
	// Seed a prior STOPPED state that recorded the observability profile
	// (as if a previous `up --profile observability` then `down`).
	seeded := registry.NewState(name, splice.LatestAlias)
	seeded.Status = registry.StatusStopped
	seeded.Profiles = []string{ObservabilityProfileName}
	seeded.Ports = map[string]int{}
	if err := registry.Write(seeded); err != nil {
		t.Fatalf("seed prior state: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errBuf bytes.Buffer
	// NOTE: Profiles intentionally empty — the inheritance is the point.
	code := RunUp(ctx, &TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          name,
			Version:       splice.LatestAlias,
			SkipPreflight: true,
			FetchFn: func(context.Context, splice.Version, string, io.Writer) (string, error) {
				return projectDir, nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess\nstderr=%q", code, errBuf.String())
	}
	state, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry.Read: %v", err)
	}
	if len(state.Profiles) != 1 || state.Profiles[0] != ObservabilityProfileName {
		t.Fatalf("re-up dropped the inherited profile: state.Profiles = %v, want [%s]",
			state.Profiles, ObservabilityProfileName)
	}
	if p, ok := state.Ports["prometheus_ui"]; !ok || p <= 0 {
		t.Errorf("prometheus_ui port = %d (ok=%v); inherited observability should re-allocate it", p, ok)
	}
	// The progress stream should announce the inheritance so an
	// operator isn't surprised the overlay came back.
	combined := out.String() + errBuf.String()
	if !strings.Contains(combined, "re-enabling profiles") {
		t.Errorf("expected a re-enabling-profiles notice; got\n%s", combined)
	}
}

// TestRunUp_ExplicitProfilesOverrideStored: an explicit --profile
// on re-up REPLACES the stored set (doesn't merge), so a user can
// deliberately drop observability.
func TestRunUp_ExplicitProfilesOverrideStored(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	projectDir := fakeSpliceProjectDir(t)

	const name = "override"
	seeded := registry.NewState(name, splice.LatestAlias)
	seeded.Status = registry.StatusStopped
	seeded.Profiles = []string{ObservabilityProfileName}
	seeded.Ports = map[string]int{}
	if err := registry.Write(seeded); err != nil {
		t.Fatalf("seed prior state: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errBuf bytes.Buffer
	code := RunUp(ctx, &TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          name,
			Version:       splice.LatestAlias,
			Profiles:      []string{TokensV2ProfileName}, // explicit, different
			SkipPreflight: true,
			FetchFn: func(context.Context, splice.Version, string, io.Writer) (string, error) {
				return projectDir, nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess\nstderr=%q", code, errBuf.String())
	}
	state, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry.Read: %v", err)
	}
	if len(state.Profiles) != 1 || state.Profiles[0] != TokensV2ProfileName {
		t.Errorf("explicit profile did not override stored set: state.Profiles = %v, want [%s]",
			state.Profiles, TokensV2ProfileName)
	}
	if _, ok := state.Ports["prometheus_ui"]; ok {
		t.Error("observability should be dropped when explicit profiles override it")
	}
}
