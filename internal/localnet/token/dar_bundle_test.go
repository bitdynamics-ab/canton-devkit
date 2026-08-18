package token

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestTokenBundleCommit_FromCuratedCatalogue(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "token-standard-v2")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	commit, err := tokenBundleCommit("demo")
	if err != nil {
		t.Fatalf("resolve commit: %v", err)
	}
	if len(commit) < 12 {
		t.Errorf("commit looks wrong: %q", commit)
	}
}

func TestTokenBundleCommit_UnknownVersionErrors(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "does-not-exist-9.9.9")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenBundleCommit("demo"); err == nil {
		t.Error("want error for an unknown Splice version")
	}
}

func TestTokenDARRoles_MatchesAllRoles(t *testing.T) {
	got := tokenDARRoles()
	want := splice.AllRoles()
	if len(got) != len(want) {
		t.Fatalf("roles len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != string(want[i]) {
			t.Errorf("roles[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestEnsureTokenDARs_FansOutToAllRoles pins #318: create vets the
// bundle on sv, app-provider, and app-user — not only the create client.
func TestEnsureTokenDARs_FansOutToAllRoles(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allLedgerPorts())
	srv, hits := startDARServer(t)
	swapDARBase(t, srv.URL)

	create := newFakeDAR()
	sv := newFakeDAR()
	user := newFakeDAR()
	var dialed []string
	withDARDial(t, func(_ context.Context, conn LedgerConn) (darClient, func(), error) {
		dialed = append(dialed, conn.Role)
		switch conn.Role {
		case "sv":
			return sv, func() {}, nil
		case "app-user":
			return user, func() {}, nil
		default:
			return nil, func() {}, errors.New("should reuse create client for " + conn.Role)
		}
	})

	var out bytes.Buffer
	opts := bundleCreateOpts("demo")
	roles, err := ensureTokenDARs(context.Background(), create, opts, &out)
	if err != nil {
		t.Fatalf("ensureTokenDARs: %v", err)
	}
	if want := []string{"sv", "app-provider", "app-user"}; strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Errorf("vetted roles=%v, want %v", roles, want)
	}
	wantUploads := len(tokenBundleDARs)
	if create.uploads != wantUploads || sv.uploads != wantUploads || user.uploads != wantUploads {
		t.Errorf("uploads create=%d sv=%d user=%d, want %d each",
			create.uploads, sv.uploads, user.uploads, wantUploads)
	}
	if hits.Load() != int32(wantUploads) {
		t.Errorf("HTTP fetches=%d, want %d (one per file, shared across roles)", hits.Load(), wantUploads)
	}
	if strings.Join(dialed, ",") != "sv,app-user" {
		t.Errorf("dialed=%v, want sv then app-user (app-provider reused)", dialed)
	}
	for _, role := range []string{"sv", "app-provider", "app-user"} {
		if !strings.Contains(out.String(), `"role":"`+role+`"`) {
			t.Errorf("output missing role %q:\n%s", role, out.String())
		}
	}
}

func TestEnsureTokenDARs_MissingPortFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", map[string]int{
		"participant_ledger_app-provider": 3901,
	})
	_, err := ensureTokenDARs(context.Background(), newFakeDAR(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when a role has no ledger port")
	}
	if !strings.Contains(err.Error(), "sv") || !strings.Contains(err.Error(), "participant_ledger_sv") {
		t.Errorf("want missing-port error naming sv, got: %v", err)
	}
}

func TestEnsureTokenDARs_CachesDAROnDisk(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allLedgerPorts())
	srv, hits := startDARServer(t)
	swapDARBase(t, srv.URL)

	withDARDial(t, func(_ context.Context, conn LedgerConn) (darClient, func(), error) {
		return newFakeDAR(), func() {}, nil
	})
	opts := bundleCreateOpts("demo")
	if _, err := ensureTokenDARs(context.Background(), newFakeDAR(), opts, io.Discard); err != nil {
		t.Fatalf("first ensureTokenDARs: %v", err)
	}
	firstHits := hits.Load()
	if firstHits != int32(len(tokenBundleDARs)) {
		t.Fatalf("first pass HTTP fetches=%d, want %d", firstHits, len(tokenBundleDARs))
	}

	commit, err := tokenBundleCommit("demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range tokenBundleDARs {
		p := darCachePath(commit, d.file)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("cache miss %s: %v", p, err)
		}
	}

	if _, err := ensureTokenDARs(context.Background(), newFakeDAR(), opts, io.Discard); err != nil {
		t.Fatalf("second ensureTokenDARs: %v", err)
	}
	if hits.Load() != firstHits {
		t.Errorf("second pass HTTP fetches=%d, want %d (disk cache)", hits.Load(), firstHits)
	}
}

func TestEnsureTokenDARs_SecondaryRoleUploadFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allLedgerPorts())
	srv, _ := startDARServer(t)
	swapDARBase(t, srv.URL)

	user := newFakeDAR()
	user.uploadErr = errors.New("boom")
	withDARDial(t, func(_ context.Context, conn LedgerConn) (darClient, func(), error) {
		if conn.Role == "app-user" {
			return user, func() {}, nil
		}
		return newFakeDAR(), func() {}, nil
	})
	_, err := ensureTokenDARs(context.Background(), newFakeDAR(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when app-user upload fails")
	}
	if !strings.Contains(err.Error(), "app-user") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("want upload error naming app-user, got: %v", err)
	}
}

func TestEnsureTokenDARs_404IsUnavailable(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allLedgerPorts())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	swapDARBase(t, srv.URL)
	withDARDial(t, func(context.Context, LedgerConn) (darClient, func(), error) {
		return newFakeDAR(), func() {}, nil
	})
	_, err := ensureTokenDARs(context.Background(), newFakeDAR(), bundleCreateOpts("demo"), io.Discard)
	if !errors.Is(err, ErrTokenDARUnavailable) {
		t.Fatalf("want ErrTokenDARUnavailable, got %v", err)
	}
}

func seedBundleInstance(t *testing.T, name string, ports map[string]int) {
	t.Helper()
	s := registry.NewState(name, "0.6.12")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = registry.StatusRunning
	s.Ports = ports
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
}

func allLedgerPorts() map[string]int {
	return map[string]int{
		"participant_ledger_sv":           4901,
		"participant_ledger_app-provider": 3901,
		"participant_ledger_app-user":     2901,
	}
}

func bundleCreateOpts(instance string) CreateOptions {
	return CreateOptions{
		Instance: instance,
		Role:     "app-provider",
		Endpoint: "localhost:3901",
		Insecure: true,
	}
}

func startDARServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("dar:" + path.Base(r.URL.Path)))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func swapDARBase(t *testing.T, url string) {
	t.Helper()
	prev := darBundleBaseURL
	darBundleBaseURL = url
	t.Cleanup(func() { darBundleBaseURL = prev })
}

func withDARDial(t *testing.T, fn func(context.Context, LedgerConn) (darClient, func(), error)) {
	t.Helper()
	prev := dialDARClient
	dialDARClient = fn
	t.Cleanup(func() { dialDARClient = prev })
}

type fakeDAR struct {
	packages  map[string]struct{}
	uploads   int
	uploadErr error
}

func newFakeDAR() *fakeDAR {
	return &fakeDAR{packages: map[string]struct{}{}}
}

func (f *fakeDAR) ListKnownPackages(context.Context) (*adminv2.ListKnownPackagesResponse, error) {
	details := make([]*adminv2.PackageDetails, 0, len(f.packages))
	for name := range f.packages {
		details = append(details, &adminv2.PackageDetails{Name: name, PackageId: name})
	}
	return &adminv2.ListKnownPackagesResponse{PackageDetails: details}, nil
}

func (f *fakeDAR) UploadDarFile(_ context.Context, req *adminv2.UploadDarFileRequest) (*adminv2.UploadDarFileResponse, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	f.uploads++
	file := strings.TrimPrefix(string(req.GetDarFile()), "dar:")
	for _, d := range tokenBundleDARs {
		if d.file == file {
			f.packages[d.pkg] = struct{}{}
		}
	}
	return &adminv2.UploadDarFileResponse{}, nil
}

func TestDarCachePath_UnderRegistryRoot(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	p := darCachePath("abc123", "splice-test-token-v2-1.0.0.dar")
	root := registry.Root()
	if !strings.HasPrefix(p, filepath.Join(root, darCacheDirName)) {
		t.Errorf("cache path %q not under %s/%s", p, root, darCacheDirName)
	}
}
