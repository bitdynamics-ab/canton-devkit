package darops

import (
	"context"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"google.golang.org/grpc"
)

// Fan-out vetting for a fixed set of DARs. `dar upload
// --all-participants` and `token create` both need "make sure these
// DARs are vetted on every participant"; keeping it here stops the two
// from drifting in how they dial, probe, and report.

// DARRef identifies one DAR by the (name, version) pair Canton reports
// in ListDars. Matching on the pair — not the name alone — is what
// makes a pinned version meaningful: a participant carrying
// splice-util-token-standard-wallet 1.0.0 must still receive the
// pinned 1.1.0.
type DARRef struct {
	Name    string
	Version string
}

func (d DARRef) String() string { return d.Name + "-" + d.Version }

// PackageAdmin is the slice of the Canton admin PackageService that
// vetting needs. adminproto.PackageServiceClient satisfies it; tests
// inject a stub. Signatures match the generated client exactly so the
// real client is assignable.
type PackageAdmin interface {
	PackageLister
	UploadDar(ctx context.Context, in *adminproto.UploadDarRequest, opts ...grpc.CallOption) (*adminproto.UploadDarResponse, error)
}

var _ PackageAdmin = adminproto.PackageServiceClient(nil)

// AdminDialer opens a PackageAdmin for a resolved admin.Config.
// Production callers pass DialPackageAdmin; tests pass a stub.
type AdminDialer func(ctx context.Context, cfg admin.Config) (PackageAdmin, func() error, error)

// DialPackageAdmin is the production AdminDialer.
func DialPackageAdmin(ctx context.Context, cfg admin.Config) (PackageAdmin, func() error, error) {
	client, err := admin.Connect(ctx, cfg)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return client.Package, client.Close, nil
}

// VetStage distinguishes the two progress events EnsureVetted emits.
type VetStage string

const (
	// StageUploading is emitted before a DAR is pushed to a participant.
	StageUploading VetStage = "uploading"
	// StageVetted is emitted after the post-upload ListDars confirms the
	// DAR is present, and therefore vetted.
	StageVetted VetStage = "vetted"
)

// VetEvent is one progress notification from EnsureVetted.
type VetEvent struct {
	Stage VetStage
	Role  string
	Host  string
	DAR   DARRef
}

// VetRequest is the input to EnsureVetted.
type VetRequest struct {
	State *registry.State
	// Roles to vet on, in the caller's preferred order. Empty means Roles.
	Roles []string
	// DARs is the set every role must end up carrying.
	DARs []DARRef
	// Load returns the DAR bytes for a ref. Called only for a ref a
	// participant is actually missing, so an unreachable source costs
	// nothing when everything is already vetted. Callers that vet
	// several roles should cache; EnsureVetted does not.
	Load func(DARRef) ([]byte, error)
	// Description is recorded on the uploaded DAR so `dar list` shows
	// where it came from. Optional.
	Description string
	// OnEvent, when non-nil, receives progress events. Optional.
	OnEvent func(VetEvent)
}

// EnsureVetted uploads every DAR a participant is missing, on every
// requested role, and confirms via ListDars that the upload landed.
// Returns the roles vetted, in the order they were processed.
//
// Unlike ListVetting, a per-role failure aborts rather than being
// recorded: a caller that vets only some participants leaves
// cross-participant workflows broken in a way the user cannot see.
func EnsureVetted(ctx context.Context, dial AdminDialer, req VetRequest) ([]string, error) {
	roles := req.Roles
	if len(roles) == 0 {
		roles = Roles
	}
	done := make([]string, 0, len(roles))
	for _, role := range roles {
		cfg, err := ResolveParticipant(req.State, role, "", true)
		if err != nil {
			return nil, fmt.Errorf("resolve participant %s: %w", role, err)
		}
		if err := vetOne(ctx, dial, cfg, role, req); err != nil {
			return nil, err
		}
		done = append(done, role)
	}
	return done, nil
}

// vetOne brings a single participant up to the requested DAR set.
func vetOne(ctx context.Context, dial AdminDialer, cfg admin.Config, role string, req VetRequest) error {
	client, closeFn, err := dial(ctx, cfg)
	if err != nil {
		return fmt.Errorf("dial %s (%s): %w", role, cfg.Host, err)
	}
	defer func() { _ = closeFn() }()

	// One ListDars per role, not per DAR: the set answers every
	// membership question for this participant.
	present, err := vettedSet(ctx, client)
	if err != nil {
		return fmt.Errorf("list DARs on %s (%s): %w", role, cfg.Host, err)
	}

	missing := make([]DARRef, 0, len(req.DARs))
	for _, d := range req.DARs {
		if !present[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	for _, d := range missing {
		data, err := req.Load(d)
		if err != nil {
			return err
		}
		emit(req.OnEvent, VetEvent{Stage: StageUploading, Role: role, Host: cfg.Host, DAR: d})
		upload := &adminproto.UploadDarRequest_UploadDarData{Bytes: data}
		if req.Description != "" {
			desc := req.Description
			upload.Description = &desc
		}
		if _, err := client.UploadDar(ctx, &adminproto.UploadDarRequest{
			Dars:               []*adminproto.UploadDarRequest_UploadDarData{upload},
			VetAllPackages:     true,
			SynchronizeVetting: true,
		}); err != nil {
			return fmt.Errorf("upload %s on %s (%s): %w", d, role, cfg.Host, err)
		}
	}

	present, err = vettedSet(ctx, client)
	if err != nil {
		return fmt.Errorf("re-list DARs on %s (%s) after upload: %w", role, cfg.Host, err)
	}
	for _, d := range missing {
		if !present[d] {
			return fmt.Errorf("%s is not vetted on %s (%s) after upload", d, role, cfg.Host)
		}
		emit(req.OnEvent, VetEvent{Stage: StageVetted, Role: role, Host: cfg.Host, DAR: d})
	}
	return nil
}

// vettedSet lists the participant's DARs as a (name, version) set. A
// list failure is returned, never folded into "absent": treating an
// unreachable participant as empty turns a connectivity problem into a
// misleading "still not vetted after upload".
func vettedSet(ctx context.Context, lister PackageLister) (map[DARRef]bool, error) {
	resp, err := lister.ListDars(ctx, &adminproto.ListDarsRequest{})
	if err != nil {
		return nil, err
	}
	set := make(map[DARRef]bool, len(resp.GetDars()))
	for _, d := range resp.GetDars() {
		set[DARRef{Name: d.GetName(), Version: d.GetVersion()}] = true
	}
	return set, nil
}

func emit(fn func(VetEvent), ev VetEvent) {
	if fn != nil {
		fn(ev)
	}
}
