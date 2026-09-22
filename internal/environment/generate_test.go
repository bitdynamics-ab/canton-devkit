package environment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func spikeTopology() Topology {
	return Topology{
		Name: "consortium-lab",
		Participants: []Participant{
			{Name: "project-a", PortBase: 21000},
			{Name: "project-b", PortBase: 22000},
		},
	}
}

func TestParticipantHOCON(t *testing.T) {
	got := ParticipantHOCON(Participant{Name: "project-a", PortBase: 21000})
	for _, want := range []string{
		"canton.participants.project-a = ${_participant}",
		"databaseName = participant-project-a",
		"ledger-api.port      = 21001",
		"admin-api.port       = 21002",
		"http-ledger-api.port = 21003",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("participant HOCON missing %q\n---\n%s", want, got)
		}
	}
}

func TestComposeOverride(t *testing.T) {
	got := spikeTopology().ComposeOverride()
	for _, want := range []string{
		`"21001:21001"`, `"21006:21006"`, `"22001:22001"`, `"22006:22006"`,
		"CREATE_DATABASE_participant_project_a: \"participant-project-a\"",
		"CREATE_DATABASE_participant_project_b: \"participant-project-b\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("compose override missing %q\n---\n%s", want, got)
		}
	}
}

// findCachedUpstreamTree returns a cached upstream LocalNet tree written by a
// prior `dpm localnet up`, or "" if none is present.
func findCachedUpstreamTree() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".canton-devkit", "cache", "splice-*"))
	for _, m := range matches {
		if _, err := os.Stat(filepath.Join(m, "compose.yaml")); err == nil {
			if _, err := os.Stat(filepath.Join(m, "conf", "canton", "app.conf")); err == nil {
				return m
			}
		}
	}
	return ""
}

// TestComposeConfigValidates is the end-to-end spike proof: the generated
// override, layered over the real upstream compose.yaml, produces a valid
// merged config. Gated behind DEVKIT_ENV_SPIKE_DOCKER (and a cached tree +
// docker) so it never runs in normal CI. Run it with:
//
//	DEVKIT_ENV_SPIKE_DOCKER=1 go test ./internal/environment/...
func TestComposeConfigValidates(t *testing.T) {
	if os.Getenv("DEVKIT_ENV_SPIKE_DOCKER") == "" {
		t.Skip("set DEVKIT_ENV_SPIKE_DOCKER=1 to run the docker compose config validation")
	}
	cache := findCachedUpstreamTree()
	if cache == "" {
		t.Skip("no cached upstream Splice tree; run `dpm localnet up` once")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}

	envDir := t.TempDir()
	// Generated env dir = copy of the upstream mount sources; the cache is
	// never patched.
	if out, err := exec.Command("cp", "-R",
		filepath.Join(cache, "conf"), filepath.Join(cache, "docker"),
		filepath.Join(cache, "env"), filepath.Join(cache, "compose.env"),
		envDir).CombinedOutput(); err != nil {
		t.Fatalf("seed env dir: %v\n%s", err, out)
	}

	topo := spikeTopology()
	appConf := filepath.Join(envDir, "conf", "canton", "app.conf")
	f, err := os.OpenFile(appConf, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + topo.CantonInclude()); err != nil {
		t.Fatal(err)
	}
	f.Close()

	override := filepath.Join(envDir, "compose.override.yaml")
	if err := os.WriteFile(override, []byte(topo.ComposeOverride()), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("docker", "compose",
		"--env-file", filepath.Join(envDir, "compose.env"),
		"--env-file", filepath.Join(envDir, "env", "common.env"),
		"-f", filepath.Join(cache, "compose.yaml"),
		"-f", filepath.Join(cache, "resource-constraints.yaml"),
		"-f", override, "config")
	cmd.Env = append(os.Environ(),
		"LOCALNET_DIR="+envDir,
		"LOCALNET_ENV_DIR="+filepath.Join(envDir, "env"),
		"IMAGE_REPO=ghcr.io/x/", "IMAGE_TAG=spike",
		"COMPOSE_PROFILES=sv,app-provider,app-user,console,multi-sync,swagger-ui",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config failed: %v\n%s", err, out)
	}
	for _, want := range []string{"21001", "22001", "CREATE_DATABASE_participant_project_a"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("merged compose config missing %q", want)
		}
	}
}
