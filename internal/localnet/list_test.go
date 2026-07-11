package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func seedListInstance(t *testing.T, name string, status registry.Status, ports map[string]int) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Ports = ports
	s.Status = status
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

func TestRunListEmptyJSONShape(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errw bytes.Buffer
	code := RunList(context.Background(), &out, &errw, &ListOptions{Format: "json"})
	if code != ExitSuccess {
		t.Fatalf("RunList exit = %d, want %d; stderr=%s", code, ExitSuccess, errw.String())
	}

	var got types.ListResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
	if len(got.Instances) != 0 {
		t.Fatalf("instances = %+v, want empty", got.Instances)
	}
}

func TestRunListEmptyTextHintUsesPositionalName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errw bytes.Buffer
	code := RunList(context.Background(), &out, &errw, &ListOptions{Format: "text"})
	if code != ExitSuccess {
		t.Fatalf("RunList exit = %d, want %d; stderr=%s", code, ExitSuccess, errw.String())
	}
	body := out.String()
	if !strings.Contains(body, "Start one with: canton-devkit localnet up <name>") {
		t.Fatalf("hint = %q, want positional up syntax", body)
	}
	if strings.Contains(body, "--name") {
		t.Fatalf("hint should not mention --name:\n%s", body)
	}
}

func TestRunListJSONSerialisesWarningAndPorts(t *testing.T) {
	regRoot := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", regRoot)
	seedListInstance(t, "good", registry.StatusRunning, map[string]int{
		"app_user_ui":       4441,
		"swagger_ui":        4487,
		"canton_admin":      31337,
		"postgres_internal": 5432,
	})
	seedListInstance(t, "bad", registry.StatusRunning, map[string]int{"app_user_ui": 5441})

	badPath := filepath.Join(regRoot, "bad", "state.json")
	if err := os.WriteFile(badPath, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}

	var out, errw bytes.Buffer
	code := RunList(context.Background(), &out, &errw, &ListOptions{Format: "json", All: true})
	if code != ExitSuccess {
		t.Fatalf("RunList exit = %d, want %d; stderr=%s", code, ExitSuccess, errw.String())
	}

	var got types.ListResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.Warning == "" || !strings.Contains(got.Warning, "bad") {
		t.Fatalf("Warning = %q, want warning naming bad instance", got.Warning)
	}
	byName := map[string]types.InstanceSummary{}
	for _, in := range got.Instances {
		byName[in.Name] = in
	}
	if byName["good"].Ports != "4441–4487" {
		t.Errorf("good ports = %q, want UI-only range 4441–4487", byName["good"].Ports)
	}
	if byName["bad"].Ports != "—" {
		t.Errorf("bad ports = %q, want unreadable placeholder", byName["bad"].Ports)
	}
}

func TestCollectListRowsStartedColumnUsesCreatedAt(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedListInstance(t, "demo", registry.StatusRunning, map[string]int{"app_user_ui": 4441})

	idx, err := registry.ReadIndex()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	idx.Entries[0].CreatedAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	idx.Entries[0].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	writeListIndexForTest(t, idx)

	idx, err = registry.ReadIndex()
	if err != nil {
		t.Fatalf("read rewritten index: %v", err)
	}
	_, resp, _ := collectListRows(context.Background(), idx, &ListOptions{Format: "json", All: true})
	if len(resp.Instances) != 1 {
		t.Fatalf("instances = %+v, want one", resp.Instances)
	}
	got := resp.Instances[0].StartedAgo
	if !strings.Contains(got, "h") || strings.Contains(got, "just now") {
		t.Fatalf("StartedAgo = %q, want hour bucket from CreatedAt", got)
	}
}

func TestStatusGlyphHandlesStopping(t *testing.T) {
	got := statusGlyph(string(registry.StatusStopping))
	if !strings.Contains(got, "stopping") || !strings.Contains(got, "◐") {
		t.Fatalf("statusGlyph(stopping) = %q, want glyph and label", got)
	}
}

func TestFormatPortRangeIgnoresNonUIKeys(t *testing.T) {
	got := formatPortRange(map[string]int{
		"canton_admin":      31337,
		"postgres":          5432,
		"postgres_internal": 5432,
		"grpc":              9999,
		"app_user_ui":       4441,
		"swagger_ui":        4487,
	})
	if got != "4441–4487" {
		t.Fatalf("formatPortRange = %q, want 4441–4487", got)
	}
}

func TestRunListTextIncludesWarning(t *testing.T) {
	regRoot := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", regRoot)
	seedListInstance(t, "bad", registry.StatusRunning, map[string]int{"app_user_ui": 5441})

	badPath := filepath.Join(regRoot, "bad", "state.json")
	if err := os.WriteFile(badPath, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}

	var out, errw bytes.Buffer
	code := RunList(context.Background(), &out, &errw, &ListOptions{Format: "text", All: true})
	if code != ExitSuccess {
		t.Fatalf("RunList exit = %d, want %d; stderr=%s", code, ExitSuccess, errw.String())
	}
	if !strings.Contains(out.String(), "bad") || !strings.Contains(out.String(), "warning:") {
		t.Fatalf("text output = %q, want row and warning", out.String())
	}
}

func writeListIndexForTest(t *testing.T, idx *registry.Index) {
	t.Helper()
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	path := filepath.Join(os.Getenv("CANTON_DEVKIT_REGISTRY"), "index.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}
