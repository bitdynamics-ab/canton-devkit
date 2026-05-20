// Package registry is the source of truth for canton-devkit's
// LocalNet instances. Every long-running subcommand (`down`, `status`,
// `list`, `logs`, `creds`) reads from here; `up` writes to it.
//
// On-disk layout under ~/.canton-devkit/localnet/:
//
//	index.json                    — directory of every instance (for `list`)
//	<name>/state.json             — full per-instance metadata
//	<name>/state.json.lock        — flock advisory lock for concurrent ops
//	<name>/overlay.env            — generated env-file overlay (written by up)
//	<name>/containers.yaml        — generated container-rename overlay
//
// All writes are atomic (tmp + rename), state.json is mode 0600 (JWTs land
// here once BIT-109 is in), and concurrent up/down on the same instance is
// rejected by the lock.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the on-disk schema version. Increment when the State
// struct gains/changes fields in a way that requires migration.
const SchemaVersion = 1

// Status enumerates the lifecycle states of an instance.
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
	StatusPartial  Status = "partial"
)

// State is the persisted record for a single LocalNet instance.
type State struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	SpliceVersion string `json:"splice_version"`
	CreatedAt     string `json:"created_at"`

	// Docker / compose wiring captured at `up` time so `down`, `logs`,
	// `status` don't have to re-derive them.
	ComposeProject  string   `json:"compose_project"`
	ComposeFiles    []string `json:"compose_files"` // absolute paths
	DockerNetwork   string   `json:"docker_network"`
	ContainerPrefix string   `json:"container_prefix"`

	// Filesystem locations
	ProjectDir string `json:"project_dir"` // ~/.canton-devkit/cache/splice-<tag>/
	DataDir    string `json:"data_dir"`    // ~/.canton-devkit/localnet/<name>/

	// Port allocation
	PortBlockStart int            `json:"port_block_start"` // 0 = no offset
	Ports          map[string]int `json:"ports"`            // logical name → host port

	// Feature flags from the version adapter
	AlphaProtocolEnabled bool `json:"alpha_protocol_enabled"`

	// Captured JWTs (BIT-109; empty for now).
	Credentials map[string]Credential `json:"credentials,omitempty"`

	// Current lifecycle status.
	Status            Status `json:"status"`
	LastHealthCheckAt string `json:"last_health_check_at,omitempty"`
}

// Credential is a single role's auth token + the metadata needed to
// re-derive or re-issue it.
type Credential struct {
	Role     string `json:"role"`
	User     string `json:"user"`
	Audience string `json:"audience"`
	JWT      string `json:"jwt"`
}

// ErrNotFound is returned by Read when no state file exists.
var ErrNotFound = errors.New("instance not registered")

// Root returns the base directory under which instances are stored.
// Defaults to ~/.canton-devkit/localnet/. Overridable for tests via
// CANTON_DEVKIT_REGISTRY.
//
// We deliberately use ~/.canton-devkit (not ~/.canton): the latter is
// reserved by upstream Canton tooling, and we don't want DevKit state
// to collide with — or be mistaken for — official Canton state.
func Root() string {
	if env := os.Getenv("CANTON_DEVKIT_REGISTRY"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".canton-devkit", "localnet")
	}
	return filepath.Join(home, ".canton-devkit", "localnet")
}

// PathFor returns the state.json path for the named instance.
func PathFor(name string) string {
	return filepath.Join(Root(), name, "state.json")
}

// DataDirFor returns the per-instance data directory (parent of state.json).
func DataDirFor(name string) string {
	return filepath.Join(Root(), name)
}

// NewState builds a fresh State with defaults applied. Callers fill in the
// rest before the first Write.
func NewState(name, spliceVersion string) *State {
	return &State{
		SchemaVersion: SchemaVersion,
		Name:          name,
		SpliceVersion: spliceVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Status:        StatusCreating,
		Ports:         map[string]int{},
		Credentials:   map[string]Credential{},
	}
}

// Read loads the state file for the named instance.
func Read(name string) (*State, error) {
	path := PathFor(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.SchemaVersion == 0 {
		return nil, fmt.Errorf("%s: missing schema_version", path)
	}
	if s.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("%s: schema_version %d newer than this DevKit's %d — please upgrade canton-devkit",
			path, s.SchemaVersion, SchemaVersion)
	}
	return &s, nil
}

// Write persists the state atomically. Creates the per-instance directory
// if missing. Updates the index. Mode 0600 because future versions of the
// struct hold JWTs.
func Write(s *State) error {
	if s.Name == "" {
		return fmt.Errorf("registry: cannot write state with empty Name")
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	dir := DataDirFor(s.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create instance dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	path := filepath.Join(dir, "state.json")
	if err := atomicWrite(path, data, 0o600); err != nil {
		return err
	}

	if err := indexAdd(s); err != nil {
		// Index failure doesn't undo the state write — the index is a
		// derived view, recoverable via `localnet list --rebuild`.
		return fmt.Errorf("update index: %w", err)
	}
	return nil
}

// Delete removes the instance directory and index entry. Idempotent.
func Delete(name string) error {
	dir := DataDirFor(name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	if err := indexRemove(name); err != nil {
		return fmt.Errorf("index remove: %w", err)
	}
	return nil
}

// atomicWrite writes data to a temp sibling then renames over path so a
// crashed write never produces a half-written state file.
//
// On any error path, the temp file is removed in the deferred cleanup;
// on the success path, `committed = true` short-circuits the cleanup
// because the rename has already promoted the temp to `path`.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-state-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	committed = true
	return nil
}
