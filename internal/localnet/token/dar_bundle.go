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

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// DAR auto-bundling. The splice-test-token-v2 instrument needs its
// upstream DARs vetted on every LocalNet participant before `token
// create` can anchor a TokenRules contract — mint/transfer to a party
// on app-user fails if only app-provider has the package. Rather than
// make the developer run `dar upload --all-participants` by hand,
// `token create` fetches the prebuilt DARs pinned to the instance's
// Splice commit and uploads any not already vetted on sv, app-provider,
// and app-user.

// tokenBundleDARs are the prebuilt DARs the test token needs, keyed by
// package name (what resolvePackageID checks) → the DAR filename.
//
// Verified against the daml/dars/ tree at the 0.6.12 commit. The wallet
// DAR ships three versions; pin 1.1.0, which carries BatchingUtilityV2
// with the full V2 allocation action set.
var tokenBundleDARs = []struct{ pkg, file string }{
	{"splice-api-token-burn-mint-v1", "splice-api-token-burn-mint-v1-1.0.0.dar"},
	{"splice-test-token-v2", "splice-test-token-v2-1.0.0.dar"},

	// V2 foundation packages for EventLog history, allocations/DvP and the
	// BatchingUtilityV2 wallet.
	{"splice-api-token-transfer-instruction-v2", "splice-api-token-transfer-instruction-v2-1.0.0.dar"},
	{"splice-api-token-allocation-v2", "splice-api-token-allocation-v2-1.0.0.dar"},
	{"splice-api-token-allocation-instruction-v2", "splice-api-token-allocation-instruction-v2-1.0.0.dar"},
	{"splice-api-token-allocation-request-v2", "splice-api-token-allocation-request-v2-1.0.0.dar"},
	{"splice-util-token-standard-wallet", "splice-util-token-standard-wallet-1.1.0.dar"},
}

const darFetchMaxBytes = 64 << 20 // 64 MiB — these DARs are well under 1 MiB

// darCacheDirName is a registry-root sibling of instance dirs. A leading
// dot cannot collide with an instance name (ValidateName rejects it).
const darCacheDirName = ".dar-cache"

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

// darClient is the narrow package-management slice ensureTokenDARs
// needs. *ledger.Client satisfies it; tests inject a fake per role.
type darClient interface {
	ListKnownPackages(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error)
	UploadDarFile(ctx context.Context, req *adminv2.UploadDarFileRequest) (*adminv2.UploadDarFileResponse, error)
}

// dialDARClient opens a darClient for a LedgerConn. Production dials
// the participant ledger; tests reassign this to return per-role fakes.
var dialDARClient = func(ctx context.Context, conn LedgerConn) (darClient, func(), error) {
	return dialLedger(ctx, conn)
}

// tokenDARRoles is the LocalNet participant topology ensureTokenDARs
// always uploads and vets on — the same set as `dar upload --all-participants`.
func tokenDARRoles() []string {
	roles := splice.AllRoles()
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

// ensureTokenDARs uploads any test-token DAR not already vetted on every
// LocalNet participant (sv, app-provider, app-user), fetching each file
// once from the upstream repo pinned to the instance's Splice commit
// (or from the shared on-disk cache). Idempotent: a fully-vetted
// topology is a no-op (no network).
//
// createClient is the already-open create connection; it is reused for
// the matching role so TokenRules create does not dial twice. A missing
// ledger port, dial failure, or upload/vet failure on any role fails
// the whole call — silent skip is how app-user was left unvetted.
func ensureTokenDARs(ctx context.Context, createClient darClient, opts CreateOptions, out io.Writer) ([]string, error) {
	roles := tokenDARRoles()
	createRole := roleOrDefault(opts.Role)
	targets := make([]struct {
		role     string
		endpoint string
	}, 0, len(roles))
	for _, role := range roles {
		endpoint := ResolveLedgerEndpoint(opts.Instance, role)
		if endpoint == "" {
			return nil, fmt.Errorf("no live ledger endpoint for role %q on instance %q — "+
				"start the instance so participant_ledger_%s is captured; "+
				"the test-token DAR must be vetted on every participant",
				role, opts.Instance, role)
		}
		targets = append(targets, struct {
			role     string
			endpoint string
		}{role: role, endpoint: endpoint})
	}

	commit, err := tokenBundleCommit(opts.Instance)
	if err != nil {
		return nil, err
	}

	fetched := map[string][]byte{}
	load := func(file string) ([]byte, error) {
		if b, ok := fetched[file]; ok {
			return b, nil
		}
		b, err := loadDAR(ctx, commit, file)
		if err != nil {
			return nil, err
		}
		fetched[file] = b
		return b, nil
	}

	vetted := make([]string, 0, len(targets))
	for _, t := range targets {
		client := createClient
		cleanup := func() {}
		reuse := createClient != nil && t.role == createRole && t.endpoint == opts.Endpoint
		if !reuse {
			var err error
			client, cleanup, err = dialDARClient(ctx, LedgerConn{
				Endpoint: t.endpoint,
				Insecure: opts.Insecure,
				Instance: opts.Instance,
				Role:     t.role,
			})
			if err != nil {
				return nil, fmt.Errorf("dial %s (%s): %w", t.role, t.endpoint, err)
			}
		}
		err := vetTokenDARsOn(ctx, client, t.role, t.endpoint, opts.Instance, commit, load, out)
		cleanup()
		if err != nil {
			return nil, err
		}
		vetted = append(vetted, t.role)
	}
	return vetted, nil
}

// vetTokenDARsOn uploads any missing bundle DAR on one participant and
// confirms each package is known afterward.
func vetTokenDARsOn(
	ctx context.Context,
	client darClient,
	role, endpoint, instance, commit string,
	load func(string) ([]byte, error),
	out io.Writer,
) error {
	for _, d := range tokenBundleDARs {
		if packageKnown(ctx, client, d.pkg) {
			continue
		}
		short := commit
		if len(short) > 12 {
			short = commit[:12]
		}
		emit(out, "dar bundle: fetching", map[string]any{
			"package": d.pkg, "commit": short, "role": role, "endpoint": endpoint,
		})
		dar, err := load(d.file)
		if err != nil {
			if errors.Is(err, errDARNotPublished) {
				return darUnavailableError(d.pkg, instanceSpliceVersion(instance))
			}
			return fmt.Errorf("fetch %s: %w", d.file, err)
		}
		if _, err := client.UploadDarFile(ctx, &adminv2.UploadDarFileRequest{DarFile: dar}); err != nil {
			return fmt.Errorf("upload %s on %s (%s): %w", d.file, role, endpoint, err)
		}
		if !packageKnown(ctx, client, d.pkg) {
			return fmt.Errorf("vet %s on %s (%s): package %q still not known after upload",
				d.file, role, endpoint, d.pkg)
		}
		emit(out, "dar bundle: vetted", map[string]any{
			"package": d.pkg, "role": role, "endpoint": endpoint,
		})
	}
	return nil
}

// packageKnown reports whether the named package is already on the
// participant. List errors are treated as "not known" so the caller
// still attempts upload; a subsequent list after upload is the confirm.
func packageKnown(ctx context.Context, client darClient, name string) bool {
	resp, err := client.ListKnownPackages(ctx)
	if err != nil {
		return false
	}
	for _, p := range resp.GetPackageDetails() {
		if p.GetName() == name {
			return true
		}
	}
	return false
}

// loadDAR returns DAR bytes from the shared on-disk cache, or fetches
// from GitHub and writes the cache. Cache write failures are ignored:
// the in-memory bytes are enough to upload.
func loadDAR(ctx context.Context, commit, file string) ([]byte, error) {
	path := darCachePath(commit, file)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	b, err := fetchDAR(ctx, commit, file)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
	return b, nil
}

func darCachePath(commit, file string) string {
	return filepath.Join(registry.Root(), darCacheDirName, filepath.Base(commit), filepath.Base(file))
}

// tokenBundleCommit resolves the instance's Splice version to the git
// commit the prebuilt DARs are pinned to (curated catalogue first, then
// the resolved-uncurated cache for ad-hoc tags).
func tokenBundleCommit(instance string) (string, error) {
	state, err := registry.Read(instance)
	if err != nil {
		return "", fmt.Errorf("read instance state: %w", err)
	}
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

// instanceSpliceVersion returns the instance's recorded Splice version
// tag for an error message; best-effort ("" on a read failure).
func instanceSpliceVersion(instance string) string {
	if st, err := registry.Read(instance); err == nil {
		return st.SpliceVersion
	}
	return ""
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

// Compile-time check that the production ledger client satisfies darClient.
var _ darClient = (*ledger.Client)(nil)
