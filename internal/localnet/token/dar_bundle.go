package token

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/darops"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// DAR auto-bundling for splice-test-token-v2. Mint/transfer to a party on
// another participant fails unless every LocalNet participant has the
// package; token create fetches and uploads the bundle instead of
// `dar upload --all-participants`.

// tokenBundleDARs are the prebuilt DARs the test token needs, pinned by
// (name, version) — the pair Canton reports in ListDars.
//
// Verified against the daml/dars/ tree at the 0.6.12 commit. The wallet
// DAR ships three versions; pin 1.1.0, which carries BatchingUtilityV2
// with the full V2 allocation action set.
var tokenBundleDARs = []darops.DARRef{
	{Name: "splice-api-token-burn-mint-v1", Version: "1.0.0"},
	{Name: "splice-test-token-v2", Version: "1.0.0"},

	// V2 foundation packages for EventLog history, allocations/DvP and the
	// BatchingUtilityV2 wallet.
	{Name: "splice-api-token-transfer-instruction-v2", Version: "1.0.0"},
	{Name: "splice-api-token-allocation-v2", Version: "1.0.0"},
	{Name: "splice-api-token-allocation-instruction-v2", Version: "1.0.0"},
	{Name: "splice-api-token-allocation-request-v2", Version: "1.0.0"},
	{Name: "splice-util-token-standard-wallet", Version: "1.1.0"},
}

const darFetchMaxBytes = 64 << 20 // 64 MiB — these DARs are well under 1 MiB

// Leading dot keeps the cache dir out of ValidateName's instance namespace.
const darCacheDirName = ".dar-cache"

// darUploadDescription is recorded on each uploaded DAR so `dar list`
// attributes it to token create rather than a manual upload.
const darUploadDescription = "canton-devkit token create"

// darBundleBaseURL is the raw.githubusercontent.com base for the upstream
// splice repo's prebuilt DARs. A package var so tests can point it at a
// local httptest server.
var darBundleBaseURL = "https://raw.githubusercontent.com/canton-network/splice"

// errDARNotPublished is returned by fetchDAR for an HTTP 404 — the DAR
// isn't committed at that commit. Stable releases before 0.6.11 do not
// publish the splice-test-token-v2 example.
var errDARNotPublished = errors.New("DAR not published at this commit")

// ErrTokenDARUnavailable is returned when a required test-token DAR isn't
// published for the instance's Splice version. An actionable precondition
// is more useful than surfacing a raw GitHub 404.
var ErrTokenDARUnavailable = errors.New("test-token DAR not available for this Splice version")

// Package var so tests swap in per-role fakes.
var dialTokenDARAdmin darops.AdminDialer = darops.DialPackageAdmin

// darFileName maps a pinned ref to its filename in the upstream
// daml/dars/ tree.
func darFileName(d darops.DARRef) string { return d.String() + ".dar" }

func tokenDARRoles() []string { return splice.AllRoleNames() }

// ensureTokenDARs vets the test-token bundle on every LocalNet
// participant and returns the roles it vetted. Any unreachable
// participant or upload failure fails create — skipping a role leaves
// counterparty participants without the package, which only surfaces
// later as an opaque mint/transfer error.
func ensureTokenDARs(ctx context.Context, opts CreateOptions, out io.Writer) ([]string, error) {
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
	}
	commit, err := tokenBundleCommit(state)
	if err != nil {
		return nil, err
	}
	short := commit
	if len(short) > 12 {
		short = commit[:12]
	}

	fetched := map[string][]byte{}
	load := func(d darops.DARRef) ([]byte, error) {
		file := darFileName(d)
		if b, ok := fetched[file]; ok {
			return b, nil
		}
		emit(out, "dar bundle: fetching", map[string]any{
			"package": d.Name, "version": d.Version, "commit": short,
		})
		b, err := loadDAR(ctx, commit, file)
		if err != nil {
			if errors.Is(err, errDARNotPublished) {
				return nil, darUnavailableError(d.Name, state.SpliceVersion)
			}
			return nil, fmt.Errorf("fetch %s: %w", file, err)
		}
		fetched[file] = b
		return b, nil
	}

	vetted, err := darops.EnsureVetted(ctx, dialTokenDARAdmin, darops.VetRequest{
		State:       state,
		Roles:       tokenDARRoles(),
		DARs:        tokenBundleDARs,
		Load:        load,
		Description: darUploadDescription,
		OnEvent: func(ev darops.VetEvent) {
			emit(out, "dar bundle: "+string(ev.Stage), map[string]any{
				"package": ev.DAR.Name, "version": ev.DAR.Version,
				"role": ev.Role, "endpoint": ev.Host,
			})
		},
	})
	if err != nil {
		return nil, fmt.Errorf("vet test-token DARs on instance %q: %w", opts.Instance, err)
	}
	return vetted, nil
}

// loadDAR returns the DAR bytes, populating a content cache under the
// registry root. The cache is written via a temp file and renamed so a
// process killed mid-write cannot leave a truncated DAR that every
// later run would read back as valid. Cache write failures are ignored;
// the in-memory bytes suffice for this upload.
func loadDAR(ctx context.Context, commit, file string) ([]byte, error) {
	path := darCachePath(commit, file)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	b, err := fetchDAR(ctx, commit, file)
	if err != nil {
		return nil, err
	}
	writeDARCache(path, b)
	return b, nil
}

func writeDARCache(path string, b []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// Same directory as the target so the rename stays within one
	// filesystem and is therefore atomic.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
	}
}

func darCachePath(commit, file string) string {
	return filepath.Join(registry.Root(), darCacheDirName, filepath.Base(commit), filepath.Base(file))
}

// tokenBundleCommit resolves the instance's Splice version to the git
// commit the prebuilt DARs are pinned to (curated catalogue first, then
// the resolved-uncurated cache for ad-hoc tags).
func tokenBundleCommit(state *registry.State) (string, error) {
	tag := state.SpliceVersion
	if v, ok := splice.SupportedVersions[tag]; ok && v.Commit != "" {
		return v.Commit, nil
	}
	if cache, err := splice.ReadResolvedCache(); err == nil {
		if rv, ok := cache.LookupResolved(tag); ok && rv.Commit != "" {
			return rv.Commit, nil
		}
	}
	return "", fmt.Errorf("cannot resolve a git commit for Splice version %q — "+
		"upload the test-token DARs manually with `localnet dar upload`", tag)
}

// darUnavailableError wraps ErrTokenDARUnavailable with an actionable
// message: the test-token DAR is published in stable Splice 0.6.11 and newer.
func darUnavailableError(pkg, version string) error {
	return fmt.Errorf("%w: %q is not published for Splice %q — on-ledger V2 tokens "+
		"need Splice 0.6.11 or newer (`dpm localnet up --version 0.6.12`), "+
		"or upload the DAR manually with `dpm localnet dar upload`",
		ErrTokenDARUnavailable, pkg, version)
}

// fetchDAR downloads a prebuilt DAR from the upstream repo at a pinned
// commit. Size-capped; the DARs are tiny but we never trust a remote
// Content-Length.
func fetchDAR(ctx context.Context, commit, file string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/daml/dars/%s", darBundleBaseURL, commit, file)
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		// 404 = the DAR isn't committed at this commit; the caller turns
		// it into the actionable ErrTokenDARUnavailable.
		return nil, fmt.Errorf("%w (GET %s)", errDARNotPublished, url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, darFetchMaxBytes))
}
