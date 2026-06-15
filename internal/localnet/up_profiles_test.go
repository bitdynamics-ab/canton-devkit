package localnet

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

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
