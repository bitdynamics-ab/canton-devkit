package dar

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenSpliceDAR is an integration-style test that runs against a
// real Splice DAR if one is present on the host. Skipped if the file
// isn't there (e.g. in CI where /tmp/dar-test/ isn't populated).
//
// Local dev: extract DARs via
//
//	docker run --rm -v /tmp/dar-test:/out --entrypoint sh \
//	  ghcr.io/digital-asset/decentralized-canton-sync/docker/splice-app:0.6.4 \
//	  -c 'cp /app/splice-node/dars/splice-wallet-0.1.5.dar /out/'
func TestOpenSpliceDAR(t *testing.T) {
	const path = "/tmp/dar-test/splice-api-token-metadata-v1-1.0.0.dar"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("real DAR not present at %s: %v", path, err)
	}

	info, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	if info.MainPackageID == "" {
		t.Errorf("MainPackageID is empty")
	}
	if info.Manifest == nil || info.Manifest.SdkVersion == "" {
		t.Errorf("manifest missing SdkVersion")
	}
	main := info.MainPackage()
	if main == nil {
		t.Fatal("no main package found")
	}
	if main.LFMajor == "" {
		t.Errorf("LFMajor empty; envelope decode failed")
	}
	t.Logf("DAR: %s", filepath.Base(path))
	t.Logf("SDK: %s", info.Manifest.SdkVersion)
	t.Logf("main: %s-%s id=%s lf=%s", main.Name, main.Version,
		main.PackageID[:16]+"…", main.LFVersion())
	t.Logf("packages: %d total", len(info.Packages))
}
