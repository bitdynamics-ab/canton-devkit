package environment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bootSmokeTopology is the Gate-1 fixture: two generated participants on
// deterministic port blocks; the upstream topology is otherwise unchanged.
func bootSmokeTopology() Topology {
	return Topology{
		Name: "participant-boot-smoke",
		Participants: []Participant{
			{Name: "project-a", PortBase: 21000},
			{Name: "project-b", PortBase: 22000},
		},
	}
}

// TestGate1_Boot boots the REAL pinned upstream LocalNet with the generator's
// participant HOCON + compose override applied, and asserts the Gate-1 pass
// condition: canton + splice reach healthy (so the generated HOCON parsed and
// every participant instantiated — Canton is all-or-nothing on config), the
// generated Postgres databases exist, and each generated participant's gRPC
// health endpoint reports SERVING. It intentionally does NOT require a usable
// Ledger API/party: generated participants start in identity.type=manual,
// awaiting the Validator App/bootstrap onboarding proven by later gates.
//
// Heavy (boots the full LocalNet). Gated behind DEVKIT_ENV_SPIKE_BOOT, a cached
// upstream tree, and docker. No other LocalNet may be running — the upstream
// compose hardcodes container_name canton/splice/postgres. Run with:
//
//	DEVKIT_ENV_SPIKE_BOOT=1 go test ./internal/environment/... -run Gate1 -timeout 20m
func TestGate1_Boot(t *testing.T) {
	if os.Getenv("DEVKIT_ENV_SPIKE_BOOT") == "" {
		t.Skip("set DEVKIT_ENV_SPIKE_BOOT=1 to run the Gate-1 real boot")
	}
	cache := findCachedUpstreamTree()
	if cache == "" {
		t.Skip("no cached upstream Splice tree; run `dpm localnet up` once")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}

	topo := bootSmokeTopology()
	envDir := t.TempDir()
	if out, err := exec.Command("cp", "-R",
		filepath.Join(cache, "conf"), filepath.Join(cache, "docker"),
		filepath.Join(cache, "env"), filepath.Join(cache, "compose.env"),
		envDir).CombinedOutput(); err != nil {
		t.Fatalf("seed env dir: %v\n%s", err, out)
	}
	appConf := filepath.Join(envDir, "conf", "canton", "app.conf")
	f, err := os.OpenFile(appConf, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + topo.CantonInclude()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(envDir, "compose.override.yaml"),
		[]byte(topo.ComposeOverride()), 0o644); err != nil {
		t.Fatal(err)
	}

	const proj = "envgate1boot"
	bootEnv := append(os.Environ(),
		"LOCALNET_DIR="+envDir,
		"LOCALNET_ENV_DIR="+filepath.Join(envDir, "env"),
		"IMAGE_TAG="+spliceTagFromCache(cache),
		"IMAGE_REPO=ghcr.io/digital-asset/decentralized-canton-sync/docker/",
		"DOCKER_NETWORK="+proj,
		"PARTY_HINT=bootsmoke-localparty-1",
		"ALPHA_PROTOCOL_VERSION_ENV="+filepath.Join(envDir, "env", "alpha-protocol-version.env"),
		"TEST_PORT=",
		"COMPOSE_PROFILES=sv,app-provider,app-user,swagger-ui",
	)
	dc := func(args ...string) *exec.Cmd {
		base := []string{"compose", "-p", proj,
			"--env-file", filepath.Join(envDir, "compose.env"),
			"--env-file", filepath.Join(envDir, "env", "common.env"),
			"-f", filepath.Join(cache, "compose.yaml"),
			"-f", filepath.Join(cache, "resource-constraints.yaml"),
			"-f", filepath.Join(envDir, "compose.override.yaml"),
		}
		cmd := exec.Command("docker", append(base, args...)...)
		cmd.Env = bootEnv
		return cmd
	}
	t.Cleanup(func() { _ = dc("down", "-v", "--remove-orphans").Run() })

	// up --wait exit 0 ⇒ canton + splice + postgres all reached healthy.
	if out, err := dc("up", "-d", "--wait", "--wait-timeout", "600").CombinedOutput(); err != nil {
		t.Fatalf("boot did not reach healthy: %v\n%s", err, tailLines(out, 40))
	}

	dbOut, err := dc("exec", "-T", "postgres", "psql", "-U", "cnadmin", "-d", "postgres", "-tAc",
		"SELECT datname FROM pg_database WHERE datname IN ('participant-project-a','participant-project-b')").CombinedOutput()
	if err != nil {
		t.Fatalf("db query: %v\n%s", err, dbOut)
	}
	for _, p := range topo.Participants {
		if !strings.Contains(string(dbOut), p.Database()) {
			t.Errorf("generated database %q missing", p.Database())
		}
		addr := "localhost:" + strconv.Itoa(p.GRPCHealthPort())
		if out, err := dc("exec", "-T", "canton", "grpc-health-probe", "-addr="+addr).CombinedOutput(); err != nil {
			t.Errorf("participant %s gRPC health %s not SERVING: %v\n%s", p.Name, addr, err, out)
		}
	}
}

// spliceTagFromCache recovers the Splice tag from a cache dir named
// splice-<tag>-<commit> (or splice-<tag>).
func spliceTagFromCache(dir string) string {
	b := strings.TrimPrefix(filepath.Base(dir), "splice-")
	if i := strings.LastIndex(b, "-"); i > 0 {
		return b[:i]
	}
	return b
}

func tailLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
