package localnet

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// fakeSpliceProjectDir builds a tempdir with the env/<role>-auth-on.env
// files captureCredentials reads, so a fake-driven RunUp reaches the
// success path.
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

// adapterBaseProfiles resolves the adapter's base --profile set for the
// default test version (the services enabledProfiles always re-adds).
func adapterBaseProfiles(t *testing.T) []string {
	t.Helper()
	version, err := splice.Resolve(splice.LatestAlias)
	if err != nil {
		t.Fatalf("resolve version: %v", err)
	}
	adapter, err := adapterFor(version)
	if err != nil {
		t.Fatalf("adapterFor: %v", err)
	}
	return adapter.Profiles()
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

// TestRunUp_InheritsOptInProfilesOnReup: a plain `down` -> `up` with no
// --profile re-enables the opt-in profiles the instance was last brought
// up with. state.Profiles persists the FULL set (adapter base +
// opt-ins); the re-up recovers just the opt-ins by subtracting the base,
// and enabledProfiles re-adds the base — so the persisted set is the same
// full set, with no double-counting.
func TestRunUp_InheritsOptInProfilesOnReup(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	projectDir := fakeSpliceProjectDir(t)
	base := adapterBaseProfiles(t)

	const name = "survivor"
	// Seed a prior STOPPED state recording the FULL set (base + observability),
	// as `up --profile observability` then `down` persists it.
	seeded := registry.NewState(name, splice.LatestAlias)
	seeded.Status = registry.StatusStopped
	seeded.Profiles = append(append([]string(nil), base...), ObservabilityProfileName)
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
	if !slices.Contains(state.Profiles, ObservabilityProfileName) {
		t.Errorf("re-up dropped the inherited observability profile: state.Profiles = %v", state.Profiles)
	}
	for _, b := range base {
		if !slices.Contains(state.Profiles, b) {
			t.Errorf("re-up lost adapter base profile %q: state.Profiles = %v", b, state.Profiles)
		}
	}
	if got, want := len(state.Profiles), len(base)+1; got != want {
		t.Errorf("state.Profiles len = %d, want %d (base + observability, no doubling): %v", got, want, state.Profiles)
	}
	combined := out.String() + errBuf.String()
	if !strings.Contains(combined, "re-enabling profiles") || !strings.Contains(combined, ObservabilityProfileName) {
		t.Errorf("expected a re-enabling-profiles notice naming the opt-in; got:\n%s", combined)
	}
}

// TestRunUp_ExplicitProfilesOverrideStoredOnReup: an explicit --profile on
// re-up REPLACES the stored set (doesn't merge / inherit), so a user can
// deliberately change or drop the opt-ins.
func TestRunUp_ExplicitProfilesOverrideStoredOnReup(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	projectDir := fakeSpliceProjectDir(t)
	base := adapterBaseProfiles(t)

	const name = "override"
	seeded := registry.NewState(name, splice.LatestAlias)
	seeded.Status = registry.StatusStopped
	seeded.Profiles = append(append([]string(nil), base...), ObservabilityProfileName)
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
	if !slices.Contains(state.Profiles, TokensV2ProfileName) {
		t.Errorf("explicit profile not applied: state.Profiles = %v", state.Profiles)
	}
	if slices.Contains(state.Profiles, ObservabilityProfileName) {
		t.Errorf("explicit --profile must override the stored set, not merge it: state.Profiles = %v", state.Profiles)
	}
}

// TestSubtractProfiles pins the opt-in recovery: full set minus the adapter
// base yields the opt-ins; an old opts-only persisted set (no base) passes
// through unchanged so it still re-enables.
func TestSubtractProfiles(t *testing.T) {
	base := []string{"sv", "app-provider", "app-user", "swagger-ui"}
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"full set minus base", append(append([]string(nil), base...), "observability"), base, []string{"observability"}},
		{"multiple opt-ins", append(append([]string(nil), base...), "prometheus", "tokens-v2"), base, []string{"prometheus", "tokens-v2"}},
		{"old opts-only set passes through", []string{"observability"}, base, []string{"observability"}},
		{"base only -> nil", base, base, nil},
		{"empty -> nil", nil, base, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subtractProfiles(tc.a, tc.b); !slices.Equal(got, tc.want) {
				t.Errorf("subtractProfiles(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
