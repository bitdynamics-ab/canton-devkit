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
// here in the future), and concurrent up/down on the same instance is
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
	// StatusStopping is the transitional state RunDown persists BEFORE
	// it invokes `docker compose down`. Without it, a crash or SIGKILL
	// mid-teardown would leave the registry reporting `running` while
	// the containers may be partially torn down — `localnet status`
	// would then lie to the user. Lives here so list.go's statusGlyph
	// and the types projection test can both reference the constant.
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
	// enabled at `up` time (adapter base profiles + the user's
	// `--profile` opt-ins). Every Splice LocalNet service is
	// profile-gated, so any compose subcommand that operates on the
	// service model (`restart`, `pause`, `ps`) MUST replay this set or
	// it targets zero services. Teardown (`down`/`clean`) does NOT need
	// it — it tears down by project label (`-p`-only). Additive: older
	// state.json files without this key decode with Profiles == nil;
	// callers fall back to re-deriving from the version adapter.
	Profiles []string `json:"profiles,omitempty"`

	// Filesystem locations
	ProjectDir string `json:"project_dir"` // ~/.canton-devkit/cache/splice-<tag>/
	DataDir    string `json:"data_dir"`    // ~/.canton-devkit/localnet/<name>/

	// Port allocation
	PortBlockStart int            `json:"port_block_start"` // 0 = no offset
	Ports          map[string]int `json:"ports"`            // logical name → host port

	// Feature flags from the version adapter
	AlphaProtocolEnabled bool `json:"alpha_protocol_enabled"`

	// Captured JWTs (empty for now).
	Credentials map[string]Credential `json:"credentials,omitempty"`

	// Tokens is the per-instance V2 instrument registry.
	// Each entry is a TokenRef indexed by symbol — created via
	// `localnet token create`, consumed by `mint/transfer/burn/
	// balance` and the Web UI Tokens screen. Persisted so users
	// don't have to retype the (admin, instrument_id) pair on every
	// command. Keyed by symbol; symbols must be unique within an
	// instance. Additive — older state.json files without this key
	// decode cleanly with Tokens == nil.
	Tokens map[string]TokenRef `json:"tokens,omitempty"`

	// Parties is the per-instance party alias registry.
	// Keyed by alias (a human-readable name like "bob"), each entry
	// maps to an allocated on-ledger party id. On LocalNet there is no
	// trust boundary between parties — the `unsafe` dev secret signs for
	// every role — so the token tooling treats the instance as one
	// god-mode workspace and lets developers refer to parties by alias
	// anywhere a party id is accepted. Auto-seeded with the role parties
	// on first scan; extended via `localnet party new <alias>`.
	// Additive — older state.json files without this key decode cleanly
	// with Parties == nil.
	Parties map[string]PartyRef `json:"parties,omitempty"`

	// ImageDigests records the content digest (image ID, sha256:…) of
	// each Splice container image that was actually pulled and run, keyed
	// by "<repository>:<tag>". Captured post-up from `docker compose
	// images`. The catalogue pins the source TREE (commit + ContentSHA)
	// but the ghcr image tags are mutable; recording the resolved digests
	// lets a later `up`/`restart` WARN if a republished tag changed what
	// actually runs — the strongest verification otherwise covers only
	// the least-dangerous artifact. Additive — older state.json files
	// without this key decode cleanly with ImageDigests == nil.
	ImageDigests map[string]string `json:"image_digests,omitempty"`

	// Current lifecycle status.
	Status            Status `json:"status"`
	LastHealthCheckAt string `json:"last_health_check_at,omitempty"`
}

// TokenRef is the on-disk shape of a registered V2 token instrument.
// MIRRORS internal/api/types.TokenRef byte-for-byte (same JSON tags);
// we redeclare it here rather than import api/types so the registry
// package stays free of an upward dependency on api/types (which
// could otherwise cause cycles via shared-types growth). The
// single-source-of-truth is the api/types declaration — registry
// just round-trips it through state.json. Adding a field requires
// updating both.
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

// validInstanceName matches names that are safe to use as a path
// component AND as a DNS label. We require DNS label form (RFC 1123)
// because the future hostname-routing model — Splice publishes
// {service}.{instance}.localhost endpoints — would otherwise break
// for names containing uppercase or underscores. Keeping a single
// rule across registry, CLI and downstream hostname construction is
// cheaper than wiring two policies that drift.
//
// RFC 1123 label format:
//   - lowercase a-z, 0-9, hyphen
//   - must start AND end with [a-z0-9] (no leading/trailing hyphen)
//   - 1-63 characters
//
// Rejects: uppercase ("MyStack"), underscores ("my_stack"), leading
// hyphen ("-flag"), trailing hyphen ("name-"), empty, anything Unicode
// or with path separators / shell metacharacters.
//
// Migration: instances previously named with uppercase or underscores
// (e.g. "my_stack") need to be re-created under a DNS-label name.
// Documented in docs/limitations.md.
var validInstanceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateName rejects instance names that could escape the registry
// root or break tooling. Called from every public entry point that
// accepts a name (PathFor, DataDirFor, Read, Write, Delete, Lock).
//
// The check is conservative — it's far easier to widen later than to
// narrow after users depend on quirky names. Specifically rejected:
//
//   - empty string
//   - leading or trailing hyphen
//   - any byte outside [a-z0-9-] (uppercase and underscore rejected
//     to keep DNS-label compatibility for future hostname routing)
//   - longer than 63 bytes (DNS label maximum)
//
// After the regex passes, we belt-and-suspenders: confirm
// filepath.Clean(name) == name and that the resulting Root()/name path
// stays under Root() once joined. That catches platform-specific
// edge cases the regex might miss (e.g. NTFS short names).
func ValidateName(name string) error {
	if !validInstanceName.MatchString(name) {
		return fmt.Errorf("%w: %q must be a DNS label: 1-63 chars of [a-z0-9-], starting and ending with [a-z0-9]",
			ErrInvalidName, name)
	}
	if filepath.Clean(name) != name {
		// Should be impossible after the regex, but the cost of an
		// extra check is essentially zero and catches future
		// regressions (e.g. someone widening the regex without
		// re-thinking containment).
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
// Callers should call ValidateName first if `name` is user-supplied;
// PathFor itself panics on an invalid name rather than silently
// returning a path outside the registry root. The panic is intentional:
// returning a "safe-looking but wrong" path would defeat the purpose
// of validation. Read/Write/Delete (the on-disk entry points) validate
// before invoking PathFor so the panic path is unreachable in normal
// flow — it exists only to catch programmer error.
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
// Added for handlers/metrics.go: the previous reverse
// lookup did strings.TrimPrefix(project, "canton-"), which broke if
// the naming convention ever changed or an instance was renamed.
// This walks the authoritative ground truth instead.
//
// Cost: O(N) reads where N = number of instances. With at most a
// handful of instances per dev box this is trivial; metrics handler
// caches the result with a 5s TTL on top.
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
// if missing. Updates the index. Mode 0600 because future versions of the
// struct hold JWTs.
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
// outside the registry root. The defense-in-depth check inside
// ValidateName re-resolves the joined path via filepath.Rel; if the
// regex were ever widened by mistake, that check still catches
// containment violations.
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

// forceFailBeforeRename is a test-only fault-injection seam. When set,
// it is called inside atomicWrite between the temp file's Close() and
// the os.Rename(). A test can panic() inside the hook to simulate a
// process crash at the worst possible moment — after the temp file is
// fully written but before it's promoted to the canonical path.
//
// In production code paths this is nil (zero overhead). Tests set it
// per-call and clear it on defer.
//
// Together with the `committed` flag's deferred cleanup, this lets us
// prove the actual atomicity guarantee: a crash between write and
// rename leaves the on-disk state untouched and no temp files behind.
var forceFailBeforeRename func()

// atomicWrite writes data to a temp sibling then renames over path so a
// crashed write never produces a half-written state file.
//
// On any error path, the temp file is removed in the deferred cleanup;
// on the success path, `committed = true` short-circuits the cleanup
// because the rename has already promoted the temp to `path`.
//
// The defer runs through panic unwinding as well, so even a panic
// between write and rename (simulated via forceFailBeforeRename) leaves
// no leftover temp file — see TestAtomicWriteSurvivesPanicBeforeRename.
//
// Durability: the temp file's contents are fsync'd BEFORE the rename,
// and the containing directory is fsync'd AFTER the rename. Without
// the data fsync, a power loss shortly after the rename can persist
// the directory entry (the rename) before the file's data blocks on
// ext4 and other journaled filesystems without strict ordering —
// leaving a zero-length or partially-written state.json/index.json,
// exactly the corruption the temp+rename dance exists to prevent. The
// dir fsync makes the rename itself durable. The panic-injection test
// only proves process-crash safety; these barriers add power-loss
// safety on top.
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
