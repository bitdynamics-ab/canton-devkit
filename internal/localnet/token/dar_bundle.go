package token

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// DAR auto-bundling. The splice-test-token-v2 instrument needs its
// upstream DARs vetted before `token create` can anchor a TokenRules
// contract. Rather than make the developer run `dar upload` by hand,
// `token create --endpoint` fetches the prebuilt DARs pinned to the
// instance's Splice commit and uploads any not already vetted.

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
	{"splice-api-token-transfer-events-v2", "splice-api-token-transfer-events-v2-1.0.0.dar"},
	{"splice-api-token-allocation-v2", "splice-api-token-allocation-v2-1.0.0.dar"},
	{"splice-api-token-allocation-instruction-v2", "splice-api-token-allocation-instruction-v2-1.0.0.dar"},
	{"splice-api-token-allocation-request-v2", "splice-api-token-allocation-request-v2-1.0.0.dar"},
	{"splice-util-token-standard-wallet", "splice-util-token-standard-wallet-1.1.0.dar"},
}

const darFetchMaxBytes = 64 << 20 // 64 MiB — these DARs are well under 1 MiB

// darBundleBaseURL is the raw.githubusercontent.com base for the upstream
// splice repo's prebuilt DARs. A package var so tests can point it at a
// local httptest server.
var darBundleBaseURL = "https://raw.githubusercontent.com/canton-network/splice"

// errDARNotPublished is returned by fetchDAR for an HTTP 404 — the DAR
// isn't committed at that commit. The splice-test-token-v2 example only
// lives on the token-standard-v2 stream, not on a release tag like 0.6.4.
var errDARNotPublished = errors.New("DAR not published at this commit")

// ErrTokenDARUnavailable is returned when a required test-token DAR isn't
// published for the instance's Splice version — on-ledger V2 token create
// needs a token-standard-v2 instance. An actionable precondition rather
// than a raw GitHub 404.
var ErrTokenDARUnavailable = errors.New("test-token DAR not available for this Splice version")

// ensureTokenDARs uploads any test-token DAR not already vetted, fetching
// it from the upstream repo pinned to the instance's Splice commit.
// Idempotent: a fully-vetted participant is a no-op (no network).
func ensureTokenDARs(ctx context.Context, client *ledger.Client, instance string, out io.Writer) error {
	missing := tokenBundleDARs[:0:0]
	for _, d := range tokenBundleDARs {
		if _, err := resolvePackageID(ctx, client, d.pkg); err != nil {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	commit, err := tokenBundleCommit(instance)
	if err != nil {
		return err
	}
	for _, d := range missing {
		emit(out, "dar bundle: fetching", map[string]any{"package": d.pkg, "commit": commit[:12]})
		dar, err := fetchDAR(ctx, commit, d.file)
		if err != nil {
			if errors.Is(err, errDARNotPublished) {
				return darUnavailableError(d.pkg, instanceSpliceVersion(instance))
			}
			return fmt.Errorf("fetch %s: %w", d.file, err)
		}
		if _, err := client.UploadDarFile(ctx, &adminv2.UploadDarFileRequest{DarFile: dar}); err != nil {
			return fmt.Errorf("upload %s: %w", d.file, err)
		}
		emit(out, "dar bundle: vetted", map[string]any{"package": d.pkg})
	}
	return nil
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
// message: the test-token DAR is only published on the token-standard-v2
// stream, so on-ledger V2 tokens need such an instance.
func darUnavailableError(pkg, version string) error {
	return fmt.Errorf("%w: %q is not published for Splice %q — on-ledger V2 tokens "+
		"need a token-standard-v2 instance (`dpm localnet up --version token-standard-v2 "+
		"--profile tokens-v2`), or upload the DAR manually with `dpm localnet dar upload`",
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
