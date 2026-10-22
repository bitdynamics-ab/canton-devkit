package localnet

import (
	"bytes"
	"context"
	"strings"
	"testing"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func forEachPrefix(t *testing.T, fn func(t *testing.T, prefix, other string)) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			fn(t, tc.prefix, tc.other)
		})
	}
}

func assertNamesPrefix(t *testing.T, got, want, other string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q:\n%s", want, got)
	}
	if strings.Contains(got, other) {
		t.Errorf("output must not mention %q:\n%s", other, got)
	}
}

func TestCLIHints_ObservabilityFollowPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
		seedObsInstance(t, "demo", registry.StatusRunning, map[string]int{})
		seedObsInstance(t, "idle", registry.StatusStopped, map[string]int{})

		cmd := buildObservability()
		var out, errBuf bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{"status", "--name", "demo"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v (stderr=%q)", err, errBuf.String())
		}
		assertNamesPrefix(t, out.String(), "`"+prefix+" observability enable --name demo`", other)

		cmd = buildObservability()
		out.Reset()
		errBuf.Reset()
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{"enable", "--name", "idle"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("enable on a stopped instance must fail")
		}
		assertNamesPrefix(t, errBuf.String(), "`"+prefix+" up idle`", other)
	})
}

func TestCLIHints_MetricsAndContainerFollowPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

		_, err := resolveMetricsInstance("")
		if err == nil {
			t.Fatal("empty registry must fail")
		}
		assertNamesPrefix(t, err.Error(), "`"+prefix+" up <name> --profile observability`", other)

		_, err = resolveInstance("ghost")
		if err == nil {
			t.Fatal("unknown instance must fail")
		}
		assertNamesPrefix(t, err.Error(), "`"+prefix+" list`", other)

		old := metricsContainersList
		t.Cleanup(func() { metricsContainersList = old })
		state := &registry.State{Name: "demo", ComposeProject: "demo", Ports: map[string]int{}}

		metricsContainersList = func(context.Context, string) ([]containers.Info, error) { return nil, nil }
		_, _, _, err = resolvePrometheusEndpoint(context.Background(), state, "127.0.0.1", 0)
		if err == nil {
			t.Fatal("no prometheus must fail")
		}
		assertNamesPrefix(t, err.Error(), "`"+prefix+" observability enable --name demo`", other)

		metricsContainersList = func(context.Context, string) ([]containers.Info, error) {
			return []containers.Info{{Service: "prometheus", State: "running"}}, nil
		}
		_, _, _, err = resolvePrometheusEndpoint(context.Background(), state, "127.0.0.1", 0)
		if err == nil {
			t.Fatal("unrecorded prometheus port must fail")
		}
		assertNamesPrefix(t, err.Error(), "`"+prefix+" restart demo`", other)
	})
}

func TestCLIHints_EnvShellHeaderFollowsPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		var out bytes.Buffer
		if err := writeEnvShell(&out, apitypes.EnvExport{Instance: "demo", Vars: map[string]string{"A": "1"}}); err != nil {
			t.Fatal(err)
		}
		assertNamesPrefix(t, out.String(), `#   eval "$(`+prefix+` env 'demo')"`, other)
	})
}
