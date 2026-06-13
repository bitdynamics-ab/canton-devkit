package token

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// DAR auto-bundling . The splice-test-token-v2 instrument
// needs two upstream DARs vetted on the participant before `token create`
// can anchor a TokenRules contract: the token itself and its burn-mint
// dependency. Rather than make the developer run `dar upload` by hand,
// `token create --endpoint` ensures they're present — fetching the
// prebuilt DARs pinned to the instance's Splice commit and uploading any
// that aren't already vetted.

// tokenBundleDARs are the prebuilt DARs (under the upstream repo's
// `daml/dars/`) the test token needs. Keyed by package name (what
// resolvePackageID checks) → the DAR filename at that path.
var tokenBundleDARs = []struct{ pkg, file string }{
	{"splice-api-token-burn-mint-v1", "splice-api-token-burn-mint-v1-1.0.0.dar"},
	{"splice-test-token-v2", "splice-test-token-v2-1.0.0.dar"},
}

const darFetchMaxBytes = 64 << 20 // 64 MiB — these DARs are well under 1 MiB

// ensureTokenDARs uploads any test-token DAR not already vetted on the
// participant, fetching it from the upstream repo pinned to the
// instance's Splice commit. Idempotent: a fully-vetted participant is a
// no-op (no network). Best-effort provenance — a fetch/upload failure is
// returned so the caller can surface the (rare) offline case.
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
// the resolved-uncurated cache for ad-hoc alpha tags).
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

// fetchDAR downloads a prebuilt DAR from the upstream repo at a pinned
// commit. Size-capped; the DARs are tiny but we never trust a remote
// Content-Length.
func fetchDAR(ctx context.Context, commit, file string) ([]byte, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/canton-network/splice/%s/daml/dars/%s", commit, file)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, darFetchMaxBytes))
}
