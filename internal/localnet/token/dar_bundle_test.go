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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/darops"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestTokenBundleCommit_FromCuratedCatalogue(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "token-standard-v2")
	commit, err := tokenBundleCommit(s)
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
	if _, err := tokenBundleCommit(s); err == nil {
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

// Create uploads the bundle on sv, app-provider, and app-user, not only
// the participant it dialed to create TokenRules.
func TestEnsureTokenDARs_FansOutToAllRoles(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, hits := startDARServer(t)
	swapDARBase(t, srv.URL)

	fakes := withDARDial(t, nil)

	var out bytes.Buffer
	roles, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), &out)
	if err != nil {
		t.Fatalf("ensureTokenDARs: %v", err)
	}
	if want := "sv,app-provider,app-user"; strings.Join(roles, ",") != want {
		t.Errorf("vetted roles=%v, want %v", roles, want)
	}
	wantUploads := len(tokenBundleDARs)
	for _, role := range roles {
		f := fakes.byRole(t, role)
		if f.uploads != wantUploads {
			t.Errorf("uploads on %s=%d, want %d", role, f.uploads, wantUploads)
		}
		// One ListDars before the uploads and one after, never per DAR.
		if f.lists != 2 {
			t.Errorf("ListDars calls on %s=%d, want 2", role, f.lists)
		}
	}
	if hits.Load() != int32(wantUploads) {
		t.Errorf("HTTP fetches=%d, want %d (one per file, shared across roles)", hits.Load(), wantUploads)
	}
	for _, role := range []string{"sv", "app-provider", "app-user"} {
		if !strings.Contains(out.String(), `"role":"`+role+`"`) {
			t.Errorf("output missing role %q:\n%s", role, out.String())
		}
	}
}

// A DAR already present under the pinned version is not re-uploaded.
func TestEnsureTokenDARs_SkipsAlreadyVetted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, hits := startDARServer(t)
	swapDARBase(t, srv.URL)

	fakes := withDARDial(t, func(f *fakeAdmin) {
		for _, d := range tokenBundleDARs {
			f.dars[d] = true
		}
	})
	if _, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard); err != nil {
		t.Fatalf("ensureTokenDARs: %v", err)
	}
	for _, role := range tokenDARRoles() {
		if f := fakes.byRole(t, role); f.uploads != 0 {
			t.Errorf("uploads on %s=%d, want 0 (already vetted)", role, f.uploads)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("HTTP fetches=%d, want 0 (nothing missing, nothing fetched)", hits.Load())
	}
}

// A pinned version must not be satisfied by a different version of the
// same package: the wallet DAR ships 1.0.0 and 1.1.0, and only 1.1.0
// carries BatchingUtilityV2.
func TestEnsureTokenDARs_WrongVersionIsNotVetted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, _ := startDARServer(t)
	swapDARBase(t, srv.URL)

	wallet := darops.DARRef{Name: "splice-util-token-standard-wallet", Version: "1.1.0"}
	fakes := withDARDial(t, func(f *fakeAdmin) {
		for _, d := range tokenBundleDARs {
			f.dars[d] = true
		}
		delete(f.dars, wallet)
		f.dars[darops.DARRef{Name: wallet.Name, Version: "1.0.0"}] = true
	})
	if _, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard); err != nil {
		t.Fatalf("ensureTokenDARs: %v", err)
	}
	for _, role := range tokenDARRoles() {
		if f := fakes.byRole(t, role); f.uploads != 1 {
			t.Errorf("uploads on %s=%d, want 1 (pinned wallet version missing)", role, f.uploads)
		}
	}
}

func TestEnsureTokenDARs_MissingPortFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", map[string]int{
		"participant_admin_app-provider": 3902,
	})
	withDARDial(t, nil)
	_, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when a role has no admin port")
	}
	if !strings.Contains(err.Error(), "sv") || !strings.Contains(err.Error(), "participant_admin") {
		t.Errorf("want missing-port error naming sv, got: %v", err)
	}
}

// A failing ListDars must surface as a list error, not be folded into
// "absent" and reported later as a bogus post-upload vetting failure.
func TestEnsureTokenDARs_ListErrorSurfaces(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, _ := startDARServer(t)
	swapDARBase(t, srv.URL)

	withDARDial(t, func(f *fakeAdmin) {
		if f.role == "app-user" {
			f.listErr = errors.New("unavailable")
		}
	})
	_, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when ListDars fails")
	}
	if !strings.Contains(err.Error(), "list DARs") || !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("want a list error, got: %v", err)
	}
	if strings.Contains(err.Error(), "after upload") {
		t.Errorf("list failure must not be reported as a post-upload vetting failure: %v", err)
	}
}

// An upload that does not land must fail create rather than report the
// role as vetted.
func TestEnsureTokenDARs_UploadNotReflectedFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, _ := startDARServer(t)
	swapDARBase(t, srv.URL)

	withDARDial(t, func(f *fakeAdmin) { f.silentUpload = true })
	_, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when an upload does not land")
	}
	if !strings.Contains(err.Error(), "not vetted") || !strings.Contains(err.Error(), "after upload") {
		t.Errorf("want a post-upload vetting error, got: %v", err)
	}
}

func TestEnsureTokenDARs_CachesDAROnDisk(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, hits := startDARServer(t)
	swapDARBase(t, srv.URL)

	withDARDial(t, nil)
	opts := bundleCreateOpts("demo")
	if _, err := ensureTokenDARs(context.Background(), opts, io.Discard); err != nil {
		t.Fatalf("first ensureTokenDARs: %v", err)
	}
	firstHits := hits.Load()
	if firstHits != int32(len(tokenBundleDARs)) {
		t.Fatalf("first pass HTTP fetches=%d, want %d", firstHits, len(tokenBundleDARs))
	}

	state, err := registry.Read("demo")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := tokenBundleCommit(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range tokenBundleDARs {
		p := darCachePath(commit, darFileName(d))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("cache miss %s: %v", p, err)
		}
	}

	// Fresh participants (nothing vetted) so the second pass would refetch
	// were the disk cache not consulted.
	withDARDial(t, nil)
	if _, err := ensureTokenDARs(context.Background(), opts, io.Discard); err != nil {
		t.Fatalf("second ensureTokenDARs: %v", err)
	}
	if hits.Load() != firstHits {
		t.Errorf("second pass HTTP fetches=%d, want %d (disk cache)", hits.Load(), firstHits)
	}
}

// The cache is written via rename, so a killed process leaves either no
// file or a complete one — never a truncated DAR a later run trusts.
func TestWriteDARCache_LeavesNoPartialFile(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	p := darCachePath("abc123", "splice-test-token-v2-1.0.0.dar")
	writeDARCache(p, []byte("dar-bytes"))

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(got) != "dar-bytes" {
		t.Errorf("cache content=%q, want %q", got, "dar-bytes")
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("cache dir holds %v, want only the final file", names)
	}
}

func TestEnsureTokenDARs_SecondaryRoleUploadFails(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv, _ := startDARServer(t)
	swapDARBase(t, srv.URL)

	withDARDial(t, func(f *fakeAdmin) {
		if f.role == "app-user" {
			f.uploadErr = errors.New("boom")
		}
	})
	_, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
	if err == nil {
		t.Fatal("want error when app-user upload fails")
	}
	if !strings.Contains(err.Error(), "app-user") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("want upload error naming app-user, got: %v", err)
	}
}

func TestEnsureTokenDARs_404IsUnavailable(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedBundleInstance(t, "demo", allAdminPorts())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	swapDARBase(t, srv.URL)
	withDARDial(t, nil)
	_, err := ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
	if !errors.Is(err, ErrTokenDARUnavailable) {
		t.Fatalf("want ErrTokenDARUnavailable, got %v", err)
	}
}

func TestDarCachePath_UnderRegistryRoot(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	p := darCachePath("abc123", "splice-test-token-v2-1.0.0.dar")
	root := registry.Root()
	if !strings.HasPrefix(p, filepath.Join(root, darCacheDirName)) {
		t.Errorf("cache path %q not under %s/%s", p, root, darCacheDirName)
	}
}

func seedBundleInstance(t *testing.T, name string, ports map[string]int) {
	t.Helper()
	s := registry.NewState(name, "0.6.12")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = registry.StatusRunning
	s.Ports = ports
	for _, role := range tokenDARRoles() {
		s.Credentials[role] = registry.Credential{Role: role, JWT: "jwt-" + role}
	}
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
}

func allAdminPorts() map[string]int {
	return map[string]int{
		"participant_admin_sv":           4902,
		"participant_admin_app-provider": 3902,
		"participant_admin_app-user":     2902,
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

// adminPortRole inverts allAdminPorts so the dialer, which only sees a
// host:port, can label each fake with the role it stands in for.
func adminPortRole(host string) string {
	for role, port := range allAdminPorts() {
		if strings.HasSuffix(host, ":"+strconv.Itoa(port)) {
			return strings.TrimPrefix(role, "participant_admin_")
		}
	}
	return ""
}

// darFakes collects the per-role fakes a test run dialed.
type darFakes struct {
	byHost map[string]*fakeAdmin
}

func (d *darFakes) byRole(t *testing.T, role string) *fakeAdmin {
	t.Helper()
	for _, f := range d.byHost {
		if f.role == role {
			return f
		}
	}
	t.Fatalf("no participant dialed for role %q", role)
	return nil
}

// withDARDial swaps the admin dialer for per-role fakes. customise, when
// non-nil, seeds each fake before it serves any call.
func withDARDial(t *testing.T, customise func(*fakeAdmin)) *darFakes {
	t.Helper()
	fakes := &darFakes{byHost: map[string]*fakeAdmin{}}
	prev := dialTokenDARAdmin
	dialTokenDARAdmin = func(_ context.Context, cfg admin.Config) (darops.PackageAdmin, func() error, error) {
		f, ok := fakes.byHost[cfg.Host]
		if !ok {
			f = newFakeAdmin(adminPortRole(cfg.Host))
			if customise != nil {
				customise(f)
			}
			fakes.byHost[cfg.Host] = f
		}
		return f, func() error { return nil }, nil
	}
	t.Cleanup(func() { dialTokenDARAdmin = prev })
	return fakes
}

type fakeAdmin struct {
	role    string
	dars    map[darops.DARRef]bool
	uploads int
	lists   int

	uploadErr error
	listErr   error
	// silentUpload accepts an upload without recording the DAR, standing
	// in for a vetting transaction that never lands.
	silentUpload bool
}

func newFakeAdmin(role string) *fakeAdmin {
	return &fakeAdmin{role: role, dars: map[darops.DARRef]bool{}}
}

func (f *fakeAdmin) ListDars(context.Context, *adminproto.ListDarsRequest, ...grpc.CallOption) (*adminproto.ListDarsResponse, error) {
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	dars := make([]*adminproto.DarDescription, 0, len(f.dars))
	for d := range f.dars {
		dars = append(dars, &adminproto.DarDescription{
			Main: d.String(), Name: d.Name, Version: d.Version,
		})
	}
	return &adminproto.ListDarsResponse{Dars: dars}, nil
}

func (f *fakeAdmin) UploadDar(_ context.Context, req *adminproto.UploadDarRequest, _ ...grpc.CallOption) (*adminproto.UploadDarResponse, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	f.uploads++
	if f.silentUpload {
		return &adminproto.UploadDarResponse{}, nil
	}
	for _, up := range req.GetDars() {
		file := strings.TrimPrefix(string(up.GetBytes()), "dar:")
		for _, d := range tokenBundleDARs {
			if darFileName(d) == file {
				f.dars[d] = true
			}
		}
	}
	return &adminproto.UploadDarResponse{}, nil
}
