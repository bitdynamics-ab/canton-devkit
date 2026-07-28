package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

var execLookPath = exec.LookPath

func seedStatusInstance(t *testing.T, name string, status registry.Status) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = status
	s.Ports = map[string]int{
		"app_user_ui":     4485,
		"swagger_ui":      9090,
		"postgres":        5432,
		"some_random_key": 12345,
	}
	s.Credentials = map[string]registry.Credential{
		"sv": {Role: "sv", User: "sv-user", Audience: "sv-aud", JWT: "eyJ.svsig"},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

func installFakeStatusProber(t *testing.T, fn func(ctx context.Context, st *registry.State) ([]types.ServiceStatus, error)) {
	t.Helper()
	prev := statusProberFn
	statusProberFn = fn
	t.Cleanup(func() { statusProberFn = prev })
}

func TestStatus_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "ghost", Format: "table"})
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if !strings.Contains(errBuf.String(), `"ghost"`) || !strings.Contains(errBuf.String(), "dpm localnet list") {
		t.Errorf("stderr should name instance and hint list, got %q", errBuf.String())
	}
}

func TestStatus_TableRendersHeaderAndSections(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return []types.ServiceStatus{
			{Name: "canton-domain", State: "healthy", Image: "splice/canton:0.6.4", Ports: "4400, 4401"},
			{Name: "participant-alice", State: "syncing", Image: "splice/participant:0.6.4", Ports: "4441"},
		}, nil
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}
	body := out.String()
	for _, want := range []string{"Name", "demo", "Splice", "0.6.4", "SERVICES", "canton-domain", "participant-alice", "ENDPOINTS", "Wallet · app-user", "http://wallet.app-user.demo.localhost:4485", "IDENTITIES", "sv-user"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestStatus_SoftFailsOnProberError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusStopped)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, errors.New("docker daemon unreachable")
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"})
	if code != ExitSuccess {
		t.Fatalf("status should soft-fail with exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "docker query failed") || !strings.Contains(out.String(), "ENDPOINTS") {
		t.Errorf("registry view should render with docker hint, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "docker compose ps failed") {
		t.Errorf("soft-fail should warn on stderr, got %q", errBuf.String())
	}
}

func TestStatus_JSONShape(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return []types.ServiceStatus{{Name: "canton-domain", State: "healthy", Image: "splice/canton:0.6.4"}}, nil
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "json"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}
	var got types.Instance
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.Name != "demo" || got.SpliceVersion != "0.6.4" {
		t.Errorf("instance shape wrong: %+v", got)
	}
	if len(got.Services) != 1 || got.Services[0].Name != "canton-domain" {
		t.Errorf("Services round-trip: got %+v", got.Services)
	}
	if len(got.Endpoints) == 0 {
		t.Error("Endpoints empty")
	}
	if got.Credentials["sv"].JWT != "eyJ.svsig" {
		t.Errorf("JWT = %q, want raw JWT", got.Credentials["sv"].JWT)
	}
}

func TestStatus_AlwaysIncludesJWT(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) { return nil, nil })

	got, err := CollectStatus(context.Background(), "demo", true)
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	if got.Credentials["sv"].JWT != "eyJ.svsig" {
		t.Errorf("expected raw JWT, got %q", got.Credentials["sv"].JWT)
	}
}

func TestStatus_NoLiveSkipsProber(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusStopped)
	called := false
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		called = true
		return nil, nil
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table", NoLive: true})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	if called {
		t.Error("--no-live should skip the docker query")
	}
	if !strings.Contains(errBuf.String(), "no-live") {
		t.Errorf("--no-live should warn on stderr, got %q", errBuf.String())
	}
	if strings.Contains(out.String(), "docker query failed") {
		t.Errorf("--no-live should not claim docker failed, got %q", out.String())
	}
	if !strings.Contains(out.String(), "registry view only") {
		t.Errorf("--no-live table should explain registry-only mode, got %q", out.String())
	}
}

func TestStatus_NoLiveWarning_JSONStdoutClean(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "json", NoLive: true})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	var got types.Instance
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Errorf("stdout is not valid JSON: %v\nstdout=%q", err, out.String())
	}
	if errBuf.Len() == 0 {
		t.Errorf("--no-live should emit warning on stderr in JSON mode")
	}
	if got.LiveProbeFailed {
		t.Errorf("LiveProbeFailed = true under --no-live")
	}
}

func TestStatus_SoftFailSetsLiveProbeFailedInJSON(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, errors.New("docker daemon unreachable")
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "json"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	var got types.Instance
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if !got.LiveProbeFailed {
		t.Errorf("LiveProbeFailed = false after soft-fail")
	}
	if got.Services != nil {
		t.Errorf("Services should be nil on soft-fail, got %+v", got.Services)
	}
}

func TestStatus_UIUnreachableWarnsWithRemediation(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return []types.ServiceStatus{{Name: "nginx", State: "healthy", Image: "nginx"}}, nil
	})
	installFakeUIProbe(t, func(_ context.Context, rawURL, _ string) error {
		if rawURL == "http://localhost:4485" {
			return io.EOF
		}
		return nil
	})

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}
	body := out.String()
	for _, want := range []string{
		"unreachable",
		"not serving HTTP",
		"connection accepted but no HTTP response (empty reply)",
		"dpm localnet up demo",
		"Recreate in the Web UI",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("table missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestStatus_UIReachableRendersNoWarning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, nil
	})
	installFakeUIProbe(t, func(context.Context, string, string) error { return nil })

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	for _, forbid := range []string{"unreachable", "not serving HTTP"} {
		if strings.Contains(out.String(), forbid) {
			t.Errorf("healthy UI table should not contain %q\nfull:\n%s", forbid, out.String())
		}
	}
}

func TestStatus_JSONCarriesReachability(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, nil
	})
	installFakeUIProbe(t, func(context.Context, string, string) error { return io.EOF })

	var out, errBuf bytes.Buffer
	code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "json"})
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr=%q", code, errBuf.String())
	}
	var got types.Instance
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	byLabel := map[string]types.Endpoint{}
	for _, e := range got.Endpoints {
		byLabel[e.Label] = e
	}
	ui := byLabel["Wallet · app-user"]
	if ui.Reachability != types.ReachabilityUnreachable || ui.ReachabilityDetail == "" {
		t.Errorf("UI endpoint = %+v, want unreachable with detail", ui)
	}
	if pg := byLabel["Postgres"]; pg.Reachability != "" {
		t.Errorf("postgres should not be probed, got %+v", pg)
	}
}

func TestStatus_UIProbeSkippedWhenNotRunning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusStopped)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, nil
	})
	called := false
	installFakeUIProbe(t, func(context.Context, string, string) error { called = true; return nil })

	var out, errBuf bytes.Buffer
	if code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"}); code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	if called {
		t.Error("stopped instances must not be UI-probed")
	}
}

func TestStatus_UIProbeSkippedWhenDockerQueryFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedStatusInstance(t, "demo", registry.StatusRunning)
	installFakeStatusProber(t, func(context.Context, *registry.State) ([]types.ServiceStatus, error) {
		return nil, errors.New("docker daemon unreachable")
	})
	called := false
	installFakeUIProbe(t, func(context.Context, string, string) error { called = true; return nil })

	var out, errBuf bytes.Buffer
	if code := RunStatus(context.Background(), &out, &errBuf, &StatusOptions{Name: "demo", Format: "table"}); code != ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	if called {
		t.Error("UI probe must not run when the docker query failed")
	}
}

func TestCollapseState(t *testing.T) {
	cases := []struct{ state, health, want string }{
		{"running", "healthy", "healthy"},
		{"running", "", "healthy"},
		{"running", "starting", "syncing"},
		{"running", "unhealthy", "unhealthy"},
		{"paused", "", "paused"},
		{"exited", "", "exited"},
		{"dead", "", "exited"},
		{"removing", "", "exited"},
		{"created", "", "created"},
	}
	for _, c := range cases {
		if got := collapseState(c.state, c.health); got != c.want {
			t.Errorf("collapseState(%q,%q) = %q, want %q", c.state, c.health, got, c.want)
		}
	}
}

func TestEndpointsFromPorts(t *testing.T) {
	got := endpointsFromPorts("localnet-2", map[string]int{"app_user_ui": 4485, "weird_service": 9999})
	if len(got) != 2 {
		t.Fatalf("got %d endpoints, want 2", len(got))
	}
	// Wallet UIs get the role-scoped wallet vhost URL.
	if got[0].Key != "app_user_ui" || got[0].Label != "Wallet · app-user" || got[0].URL != "http://wallet.app-user.localnet-2.localhost:4485" {
		t.Errorf("known endpoint mapping wrong: %+v", got[0])
	}
	if got[1].Key != "weird_service" || got[1].Label != "weird_service" || got[1].Scheme != "tcp" {
		t.Errorf("unknown endpoint should fall back to key+label+tcp: %+v", got[1])
	}
}

// Pins the per-role wallet endpoint keys and labels; the Wallet
// screen resolves its iframe URL by key.
func TestEndpointsFromPorts_WalletKeysStablePerRole(t *testing.T) {
	got := endpointsFromPorts("localnet-2", map[string]int{
		"app_user_ui":     4485,
		"app_provider_ui": 4486,
		"sv_ui":           4487,
	})
	byKey := map[string]types.Endpoint{}
	for _, e := range got {
		byKey[e.Key] = e
	}
	for key, want := range map[string]struct{ label, host string }{
		"app_user_ui":     {"Wallet · app-user", "wallet.app-user.localnet-2.localhost"},
		"app_provider_ui": {"Wallet · app-provider", "wallet.app-provider.localnet-2.localhost"},
		"sv_ui":           {"Wallet · sv", "wallet.sv.localnet-2.localhost"},
	} {
		e, ok := byKey[key]
		if !ok {
			t.Errorf("no endpoint with key %q: %+v", key, got)
			continue
		}
		if e.Label != want.label {
			t.Errorf("key %q label = %q, want %q", key, e.Label, want.label)
		}
		// Every wallet UI resolves to its role-scoped wallet vhost.
		if !strings.Contains(e.URL, "//"+want.host+":") {
			t.Errorf("key %q URL = %q, want host %q", key, e.URL, want.host)
		}
	}
}

func TestEndpointsFromPorts_SkipsZeroPorts(t *testing.T) {
	got := endpointsFromPorts("localnet-2", map[string]int{"app_user_ui": 0, "postgres": 5432})
	if len(got) != 1 || got[0].Label != "Postgres" {
		t.Errorf("zero ports should be skipped, got %+v", got)
	}
}

func TestStateGlyph_AllStatesRenderDistinctly(t *testing.T) {
	cases := []struct {
		in           string
		mustContain  string
		mustNotEqual []string
	}{
		{string(registry.StatusRunning), "healthy", nil},
		{string(registry.StatusCreating), "creating", []string{"syncing", "healthy"}},
		{string(registry.StatusStopped), "stopped", nil},
		{string(registry.StatusFailed), "exited", nil},
		{string(registry.StatusPartial), "partial", nil},
		{"healthy", "healthy", nil},
		{"unhealthy", "unhealthy", []string{"healthy"}},
		{"syncing", "syncing", []string{"healthy", "creating"}},
		{"exited", "exited", []string{"stopped"}},
		{"paused", "paused", nil},
	}
	rendered := make(map[string]string, len(cases))
	for _, c := range cases {
		got := stateGlyph(c.in)
		if got == "" || !strings.Contains(got, c.mustContain) {
			t.Errorf("stateGlyph(%q) = %q, want substring %q", c.in, got, c.mustContain)
		}
		rendered[c.in] = got
	}
	for _, c := range cases {
		for _, other := range c.mustNotEqual {
			if rendered[c.in] == rendered[other] {
				t.Errorf("stateGlyph(%q) and stateGlyph(%q) collide: both = %q", c.in, other, rendered[c.in])
			}
		}
	}
}

func TestStatusProber_UsesComposeRunnerSeam(t *testing.T) {
	if _, err := execLookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	s := &registry.State{ComposeProject: "definitely-does-not-exist-" + t.Name(), ProjectDir: t.TempDir()}
	_, err := defaultStatusProber(context.Background(), s)
	if err != nil && !strings.Contains(err.Error(), "docker compose ps") {
		t.Errorf("error should carry ComposeRunner.Ps's wrap prefix, got: %v", err)
	}
}
