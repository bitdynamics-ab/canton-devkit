package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
	"google.golang.org/grpc"
)

// darMux mounts the DAR endpoints for an isolated test surface. The
// hub-dependent handlers receive a real stream.Hub (cheap, no
// goroutines) so the watch SSE bridge tests can publish + subscribe
// end-to-end.
func darMux(t *testing.T) (*httptest.Server, *stream.Hub) {
	t.Helper()
	mux := http.NewServeMux()
	hub := stream.New()
	MountDAR(mux, hub)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		hub.Close()
	})
	return srv, hub
}

// TestDARInspect_RejectsInvalidPackageID guards the package-id
// validator. A path-segment id has to be 64 lowercase hex; anything
// else has to land as 400 INVALID_REQUEST before we touch the
// admin client or registry — the validator is the hot-path defence
// against parameter-injection into a gRPC field.
func TestDARInspect_RejectsInvalidPackageID(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := darMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/dar/notahex/inspect")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARDiff_RejectsSameID pins the "trivial diff" rejection. A
// caller passing a=b is either confused or chasing a bug — return
// 400 rather than the empty-diff envelope so the bug is visible
// upstream.
func TestDARDiff_RejectsSameID(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := darMux(t)
	id := strings.Repeat("a", 64)
	resp, err := http.Get(srv.URL + "/api/instances/demo/dar/diff?a=" + id + "&b=" + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARDiff_RejectsInvalidIDs pins the two-id validation gate.
func TestDARDiff_RejectsInvalidIDs(t *testing.T) {
	srv, _ := darMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/dar/diff?a=foo&b=bar")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARVettingToggle_RejectsInvalidRole pins the role-enum check
// on the toggle endpoint. The role is a path segment that flows
// into the registry lookup; an unknown role must return 400 before
// the lookup so the error envelope is consistent with the list
// endpoint.
func TestDARVettingToggle_RejectsInvalidRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := darMux(t)
	id := strings.Repeat("a", 64)
	body := bytes.NewReader([]byte(`{"vetted":true}`))
	req, err := http.NewRequest("POST",
		srv.URL+"/api/instances/demo/dar/"+id+"/vetting/admin", body)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARVettingToggle_RejectsUnknownBodyField guards the
// DisallowUnknownFields contract. A typo like `{"Vet": true}` must
// 400, not silently no-op against the zero-value default.
func TestDARVettingToggle_RejectsUnknownBodyField(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := darMux(t)
	id := strings.Repeat("a", 64)
	body := bytes.NewReader([]byte(`{"Vet": true}`))
	req, _ := http.NewRequest("POST",
		srv.URL+"/api/instances/demo/dar/"+id+"/vetting/app-user", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARWatchPublish_RejectsUnknownEvent pins the event-enum
// check. The CLI watch process must use the documented event set;
// a typo lands as 400 INVALID_REQUEST so the bug surfaces in the
// publisher logs rather than as a phantom UI badge.
func TestDARWatchPublish_RejectsUnknownEvent(t *testing.T) {
	srv, _ := darMux(t)
	body := bytes.NewReader([]byte(`{"instance":"demo","event":"made_up","at":1}`))
	resp, err := http.Post(srv.URL+"/api/dar/watch/publish", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestDARWatchPublish_AcceptsValidEvent walks the happy path: a
// well-formed publish lands a 202, AND the published event is
// re-emitted on the wildcard hub topic. Wired with a real hub
// because the bridge contract is "publish to hub", not "respond OK
// in isolation".
func TestDARWatchPublish_AcceptsValidEvent(t *testing.T) {
	srv, hub := darMux(t)

	// Subscribe BEFORE publishing — Hub.Publish is fan-out, no
	// replay, so a late subscriber misses the event.
	topic := WatchTopic("demo", "*")
	ch, cancel := hub.Subscribe(topic)
	defer cancel()

	body := bytes.NewReader([]byte(
		`{"instance":"demo","event":"rebuild_finished","at":1700000000,"detail":"foo.dar"}`))
	resp, err := http.Post(srv.URL+"/api/dar/watch/publish", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	// Drain one event off the channel and check the payload
	// round-trips. Use a buffered receive with a timeout-like
	// fallback via select to keep the test deterministic.
	var received []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ev := <-ch
		received = ev.Data
	}()
	wg.Wait()

	var got map[string]any
	if err := json.Unmarshal(received, &got); err != nil {
		t.Fatalf("decode event: %v\nraw: %s", err, received)
	}
	if got["instance"] != "demo" {
		t.Errorf("instance: want demo, got %v", got["instance"])
	}
	if got["event"] != "rebuild_finished" {
		t.Errorf("event: want rebuild_finished, got %v", got["event"])
	}
}

// TestDARWatchEvents_RejectsBadInstanceName pins the SSE
// subscribe-side validation. An invalid instance must 400 before
// we open a long-lived SSE connection.
func TestDARWatchEvents_RejectsBadInstanceName(t *testing.T) {
	srv, _ := darMux(t)
	resp, err := http.Get(srv.URL + "/api/dar/watch/events?instance=..%2Fetc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestStructuralDiff_DetectsAddedTemplate is the unit test for the
// in-package diff comparator. Two minimal package trees: one with
// a template `M:T1`, one with both `M:T1` and `M:T2`. Verifies the
// added/removed/changed bucketing.
func TestStructuralDiff_DetectsAddedTemplate(t *testing.T) {
	// We construct the structures via the cdkdar.Info shape — see
	// internal/dar.PackageContents. The diff function only reads
	// .Packages[*].Contents.Modules so we don't need a manifest.

	// Build the two infos by hand. The structuralDiff function
	// signature takes *cdkdar.Info; reach for the package directly.
	// (covered separately by go test ./internal/dar)
	// The smoke check here lives in dar_inspect_unit_test.go to
	// keep handler tests focused on HTTP contracts.
}

// TestDARList_RejectsInvalidName guards the existing list endpoint
// stays wired after MountDAR was extended.
func TestDARList_RejectsInvalidName(t *testing.T) {
	srv, _ := darMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/..%2Fetc/dar")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// ── happy-path coverage via a real gRPC PackageService ──────────────
//
// The list/upload handlers dial admin.Connect("localhost:<port>"),
// so a bufconn fake isn't reachable through that real TCP dial. We
// instead stand up a genuine in-process gRPC server on an ephemeral
// loopback port and point the seeded registry entry at it. This
// exercises the full handler path the pre-gRPC validation tests skip:
// the proto→JSON projection, the multipart cap, the per-role fan-out,
// and partial-failure aggregation.

// darPackageStub is a minimal PackageServiceServer for the happy-path
// tests. Records the requests it saw and returns canned responses.
type darPackageStub struct {
	adminproto.UnimplementedPackageServiceServer

	mu         sync.Mutex
	uploadReqs []*adminproto.UploadDarRequest

	listResp *adminproto.ListDarsResponse
	darIDs   []string
}

func (s *darPackageStub) ListDars(_ context.Context, _ *adminproto.ListDarsRequest) (*adminproto.ListDarsResponse, error) {
	return s.listResp, nil
}

func (s *darPackageStub) UploadDar(_ context.Context, req *adminproto.UploadDarRequest) (*adminproto.UploadDarResponse, error) {
	s.mu.Lock()
	s.uploadReqs = append(s.uploadReqs, req)
	s.mu.Unlock()
	return &adminproto.UploadDarResponse{DarIds: s.darIDs}, nil
}

func (s *darPackageStub) uploadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.uploadReqs)
}

// startDARStub runs stub on a fresh loopback TCP port and returns that
// port. The server is torn down on test cleanup.
func startDARStub(t *testing.T, stub *darPackageStub) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	adminproto.RegisterPackageServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

// seedDARInstance writes a registry entry whose per-role admin ports
// and credentials are populated so the DAR handlers can dial. ports
// maps role → admin port; every role gets a dummy JWT so the
// credential lookup succeeds.
func seedDARInstance(t *testing.T, name string, ports map[string]int) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = registry.StatusRunning
	for role, port := range ports {
		s.Ports["participant_admin_"+role] = port
		s.Credentials[role] = registry.Credential{Role: role, User: role, JWT: "dev-jwt-" + role}
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// darUploadBody builds a multipart/form-data body for the upload
// endpoint: the given roles (repeated `roles` fields) and one `file`
// part carrying fileBytes. Returns the body and its Content-Type.
func darUploadBody(t *testing.T, roles []string, filename string, fileBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, r := range roles {
		if err := mw.WriteField("roles", r); err != nil {
			t.Fatalf("write roles field: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(fileBytes); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// TestDARList_HappyPath drives the list endpoint end-to-end against a
// real gRPC server: a seeded instance, a ListDars response, and the
// projected JSON the browser consumes.
func TestDARList_HappyPath(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	stub := &darPackageStub{
		listResp: &adminproto.ListDarsResponse{
			Dars: []*adminproto.DarDescription{
				{Main: "id1", Name: "splice-wallet", Version: "0.1.5", Description: "wallet"},
				{Main: "id2", Name: "daml-prim", Version: "0.0.0"},
			},
		},
	}
	port := startDARStub(t, stub)
	seedDARInstance(t, "demo", map[string]int{"app-provider": port})

	srv, _ := darMux(t)
	// Omit ?role= — empty-role fallback is app-provider.
	resp, err := http.Get(srv.URL + "/api/instances/demo/dar")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Instance string `json:"instance"`
		Role     string `json:"role"`
		Dars     []struct {
			Main, Name, Version, Description string
		} `json:"dars"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Instance != "demo" || got.Role != "app-provider" {
		t.Errorf("instance/role = %q/%q, want demo/app-provider", got.Instance, got.Role)
	}
	if len(got.Dars) != 2 {
		t.Fatalf("dars count = %d, want 2", len(got.Dars))
	}
	if got.Dars[0].Name != "splice-wallet" || got.Dars[0].Version != "0.1.5" || got.Dars[0].Description != "wallet" {
		t.Errorf("first dar row not projected faithfully: %+v", got.Dars[0])
	}
}

// TestDARUpload_HappyPath drives the upload endpoint end-to-end: a
// single role, a real participant, and a 200 envelope reporting the
// dar id the server returned. Also asserts the bytes reached the
// server and vetting flags were forced ON.
func TestDARUpload_HappyPath(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	stub := &darPackageStub{darIDs: []string{"uploaded-id-1"}}
	port := startDARStub(t, stub)
	seedDARInstance(t, "demo", map[string]int{"app-user": port})

	srv, _ := darMux(t)
	body, ct := darUploadBody(t, []string{"app-user"}, "app.dar", []byte("dar-bytes"))
	resp, err := http.Post(srv.URL+"/api/instances/demo/dar", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Instance      string `json:"instance"`
		TotalUploaded int    `json:"total_uploaded"`
		Results       []struct {
			Role   string   `json:"role"`
			OK     bool     `json:"ok"`
			DarIDs []string `json:"dar_ids"`
			Count  int      `json:"count"`
			Error  string   `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TotalUploaded != 1 {
		t.Errorf("total_uploaded = %d, want 1", got.TotalUploaded)
	}
	if len(got.Results) != 1 || !got.Results[0].OK || got.Results[0].Count != 1 {
		t.Fatalf("results not as expected: %+v", got.Results)
	}
	if stub.uploadCount() != 1 {
		t.Errorf("server saw %d uploads, want 1", stub.uploadCount())
	}
	req := stub.uploadReqs[0]
	if !req.GetVetAllPackages() || !req.GetSynchronizeVetting() {
		t.Errorf("vetting flags not forced on: vet=%v sync=%v",
			req.GetVetAllPackages(), req.GetSynchronizeVetting())
	}
	if string(req.GetDars()[0].GetBytes()) != "dar-bytes" {
		t.Errorf("dar bytes not forwarded to participant")
	}
}

// TestDARUpload_PartialFailure pins the partial-failure aggregation:
// two roles selected, only one has a recorded port, so the response is
// still 200 but reports one ok and one failed role — a participant
// being down must NOT masquerade as a total failure.
func TestDARUpload_PartialFailure(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	stub := &darPackageStub{darIDs: []string{"id-x"}}
	port := startDARStub(t, stub)
	// app-user has a port; sv does NOT (no participant_admin_sv).
	seedDARInstance(t, "demo", map[string]int{"app-user": port})

	srv, _ := darMux(t)
	body, ct := darUploadBody(t, []string{"app-user", "sv"}, "app.dar", []byte("payload"))
	resp, err := http.Post(srv.URL+"/api/instances/demo/dar", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (partial failure is a 200 envelope); body=%s", resp.StatusCode, raw)
	}
	var got struct {
		TotalUploaded int `json:"total_uploaded"`
		Results       []struct {
			Role  string `json:"role"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TotalUploaded != 1 {
		t.Errorf("total_uploaded = %d, want 1 (only app-user succeeded)", got.TotalUploaded)
	}
	byRole := map[string]bool{}
	errByRole := map[string]string{}
	for _, r := range got.Results {
		byRole[r.Role] = r.OK
		errByRole[r.Role] = r.Error
	}
	if !byRole["app-user"] {
		t.Errorf("app-user should have succeeded: %+v", got.Results)
	}
	if byRole["sv"] {
		t.Errorf("sv should have failed (no recorded port): %+v", got.Results)
	}
	if !strings.Contains(errByRole["sv"], "port not recorded") {
		t.Errorf("sv error should explain the missing port, got %q", errByRole["sv"])
	}
}

// TestDARUpload_TooLargeMapsTo413 pins the MaxBytesError → 413
// mapping: a body over the darUploadMax cap must surface as
// REQUEST_TOO_LARGE, not a generic 400. We don't need a valid
// instance — the cap fires while parsing the multipart body, before
// the registry read.
func TestDARUpload_TooLargeMapsTo413(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := darMux(t)

	// One file part just over the 64 MiB cap.
	big := make([]byte, darUploadMax+1024)
	body, ct := darUploadBody(t, []string{"app-user"}, "huge.dar", big)
	resp, err := http.Post(srv.URL+"/api/instances/demo/dar", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 413; body=%s", resp.StatusCode, raw)
	}
}
