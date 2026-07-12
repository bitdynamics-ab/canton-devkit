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
// All writes are atomic (tmp + rename), state.json is mode 0600 (it
// holds captured JWTs), and concurrent up/down on the same instance is
// rejected by the lock.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	// StatusStopping is persisted BEFORE `docker compose down` runs, so
	// a crash or SIGKILL mid-teardown can't leave the registry reporting
	// `running` while containers are partially torn down.
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
	StatusPartial  Status = "partial"
	// StatusPaused is set by `localnet pause` (docker compose pause):
	// containers are frozen (SIGSTOP) but alive, holding state and ports.
	// `localnet resume` (unpause) returns to running with no boot cost.
	StatusPaused Status = "paused"
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

	// Profiles is the exact set of `docker compose --profile` names
	// enabled at `up` time (adapter base profiles + user opt-ins).
	// Every Splice LocalNet service is profile-gated, so compose
	// subcommands that operate on the service model (`restart`,
	// `pause`, `ps`) MUST replay this set or they target zero services;
	// teardown (`down`/`remove`) works by project label and does not
	// need it. Additive: older state.json files decode with
	// Profiles == nil and callers re-derive from the version adapter.
	Profiles []string `json:"profiles,omitempty"`

	// ObservabilityMode records the shared/per-instance sidecar choice so a
	// re-up preserves it. Empty on older state.json files == auto.
	ObservabilityMode string `json:"observability_mode,omitempty"`

	// Filesystem locations
	ProjectDir string `json:"project_dir"` // ~/.canton-devkit/cache/splice-<tag>/
	DataDir    string `json:"data_dir"`    // ~/.canton-devkit/localnet/<name>/

	// Port allocation
	PortBlockStart int            `json:"port_block_start"` // 0 = no offset
	Ports          map[string]int `json:"ports"`            // logical name → host port

	// Feature flags from the version adapter
	AlphaProtocolEnabled bool `json:"alpha_protocol_enabled"`

	// Credentials holds the per-role JWTs captured at `up` time.
	Credentials map[string]Credential `json:"credentials,omitempty"`

	// Tokens is the per-instance V2 instrument registry, keyed by
	// symbol (unique within an instance). Created via `localnet token
	// create`; consumed by mint/transfer/burn/balance and the Web UI
	// Tokens screen. Additive — older state.json files without this
	// key decode cleanly with Tokens == nil.
	Tokens map[string]TokenRef `json:"tokens,omitempty"`

	// Parties maps a human-readable alias (e.g. "bob") to an allocated
	// on-ledger party id. On LocalNet the `unsafe` dev secret signs for
	// every role, so there is no trust boundary between parties and the
	// tooling accepts an alias anywhere a party id is accepted.
	// Auto-seeded with the role parties on first scan; extended via
	// `localnet party new <alias>`. Additive — older state.json files
	// without this key decode cleanly with Parties == nil.
	Parties map[string]PartyRef `json:"parties,omitempty"`

	// ImageDigests records the content digest (sha256:…) of each Splice
	// container image actually pulled and run, keyed by
	// "<repository>:<tag>" and captured post-up from `docker compose
	// images`. The catalogue pins the source tree but ghcr image tags
	// are mutable; recording resolved digests lets a later up/restart
	// WARN if a republished tag changed what actually runs. Additive —
	// older state.json files decode cleanly with ImageDigests == nil.
	ImageDigests map[string]string `json:"image_digests,omitempty"`

	// Current lifecycle status.
	Status            Status `json:"status"`
	LastHealthCheckAt string `json:"last_health_check_at,omitempty"`
}

// TokenRef is the on-disk shape of a registered V2 token instrument.
// MIRRORS internal/api/types.TokenRef (same JSON tags); redeclared here
// rather than imported so registry stays free of an upward api/types
// dependency (a cycle risk). api/types is the source of truth — adding
// a field requires updating both.
type TokenRef struct {
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      int    `json:"decimals"`
	InitialSupply string `json:"initial_supply"`
	IssuerParty   string `json:"issuer_party"`
	InstrumentID  string `json:"instrument_id"`
	CreatedAt     string `json:"created_at"`
	Status        string `json:"status"`
}

// PartyRef is one registered party alias → its allocated on-ledger
// party id, plus the role whose participant hosts it. MIRRORS
// internal/api/types.PartyRef (same JSON tags); kept here so the
// registry package stays free of an upward api/types dependency. Adding
// a field requires updating both.
type PartyRef struct {
	Alias     string `json:"alias"`
	PartyID   string `json:"party_id"`
	Role      string `json:"role"`               // participant that hosts it (app-user/app-provider/sv)
	IsLocal   bool   `json:"is_local,omitempty"` // locally-hosted (can be granted/acted-as)
	CreatedAt string `json:"created_at"`
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

// ErrInvalidName is returned by every public registry entry point when
// the instance name is unsafe to use as a path component. Callers
// should treat this as a user-error condition (exit 1), not a system
// failure.
var ErrInvalidName = errors.New("invalid instance name")

// validInstanceName matches names that are safe both as a path
// component and as a DNS label (RFC 1123: 1-63 chars of [a-z0-9-],
// starting and ending with [a-z0-9]). DNS-label form matters because
// Splice's hostname-routing model publishes
// {service}.{instance}.localhost endpoints, which would break for
// names with uppercase or underscores. One rule shared by registry,
// CLI and hostname construction is cheaper than two policies that
// drift. See docs/limitations.md for the migration note.
var validInstanceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateName rejects instance names that could escape the registry
// root or break tooling. Called from every public entry point that
// accepts a name (PathFor, DataDirFor, Read, Write, Delete, Lock).
// The check is deliberately conservative — it's far easier to widen
// later than to narrow after users depend on quirky names.
//
// After the regex passes, belt-and-suspenders checks confirm
// filepath.Clean(name) == name and that Root()/name stays under Root()
// once joined, catching platform-specific edge cases the regex might
// miss (e.g. NTFS short names).
func ValidateName(name string) error {
	if !validInstanceName.MatchString(name) {
		return fmt.Errorf("%w: %q must be a DNS label: 1-63 chars of [a-z0-9-], starting and ending with [a-z0-9]",
			ErrInvalidName, name)
	}
	if filepath.Clean(name) != name {
		// Should be impossible after the regex; guards against the
		// regex being widened without re-thinking containment.
		return fmt.Errorf("%w: %q changes under filepath.Clean", ErrInvalidName, name)
	}
	root := Root()
	joined := filepath.Join(root, name)
	rel, err := filepath.Rel(root, joined)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return fmt.Errorf("%w: %q would escape registry root %s", ErrInvalidName, name, root)
	}
	return nil
}

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
//
// Panics on an invalid name rather than returning a "safe-looking but
// wrong" path outside the registry root. Read/Write/Delete validate
// before invoking PathFor, so the panic only catches programmer error;
// callers with user-supplied names should call ValidateName first.
func PathFor(name string) string {
	if err := ValidateName(name); err != nil {
		panic(fmt.Sprintf("registry.PathFor called with invalid name: %v", err))
	}
	return filepath.Join(Root(), name, "state.json")
}

// DataDirFor returns the per-instance data directory (parent of state.json).
// Same panic-on-invalid semantics as PathFor.
func DataDirFor(name string) string {
	if err := ValidateName(name); err != nil {
		panic(fmt.Sprintf("registry.DataDirFor called with invalid name: %v", err))
	}
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
		Parties:       map[string]PartyRef{},
	}
}

// LookupByComposeProject walks the index and returns the State for
// the instance whose ComposeProject matches. Returns ErrNotFound
// when no match is found.
//
// Walks the authoritative registry rather than reverse-engineering the
// project name, so it survives naming-convention changes and renames.
// Cost is O(N) reads over the handful of instances on a dev box; the
// metrics handler caches the result with a 5s TTL on top.
func LookupByComposeProject(project string) (*State, error) {
	idx, err := ReadIndex()
	if err != nil {
		return nil, err
	}
	for _, e := range idx.Entries {
		s, err := Read(e.Name)
		if err != nil {
			continue // skip unreadable entries; don't fail the lookup
		}
		if s.ComposeProject == project {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

// Read loads the state file for the named instance.
func Read(name string) (*State, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path := PathFor(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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
// if missing. Updates the index. Mode 0600 because the struct holds JWTs.
func Write(s *State) error {
	if err := ValidateName(s.Name); err != nil {
		return err
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
//
// Validates the name up-front so an attacker-controlled --name (e.g.
// `../../home/victim`) can never make os.RemoveAll target a path
// outside the registry root.
func Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dir := DataDirFor(name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	if err := indexRemove(name); err != nil {
		return fmt.Errorf("index remove: %w", err)
	}
	return nil
}

// forceFailBeforeRename is a test-only fault-injection seam, called
// inside atomicWrite between the temp file's Close() and the
// os.Rename(). Tests panic() inside the hook to simulate a process
// crash after the temp file is fully written but before it's promoted.
// Nil in production paths; tests set it per-call and clear on defer.
var forceFailBeforeRename func()

// atomicWrite writes data to a temp sibling then renames over path so a
// crashed write never produces a half-written state file. The deferred
// cleanup removes the temp file on any error path — including panic
// unwinding (see TestAtomicWriteSurvivesPanicBeforeRename); once the
// rename succeeds, `committed = true` short-circuits it.
//
// Durability: the temp file's contents are fsync'd BEFORE the rename
// and the containing directory AFTER. Without the data fsync, a power
// loss shortly after the rename can persist the directory entry before
// the file's data blocks (on ext4 and other journaled filesystems
// without strict ordering), leaving a truncated state.json — exactly
// the corruption the temp+rename dance exists to prevent. The dir
// fsync makes the rename itself durable.
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
	// Flush the file's data to stable storage before the rename so the
	// bytes are durable independent of the directory entry.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if forceFailBeforeRename != nil {
		forceFailBeforeRename() // may panic; defer above still cleans tmp
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	committed = true
	// Persist the rename itself: an fsync of the parent directory makes
	// the new directory entry survive a crash. Best-effort on platforms
	// where directory fsync isn't meaningful (see fsyncDir).
	if err := fsyncDir(dir); err != nil {
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return nil
}
