package token

import (
	"context"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// fakeLedger satisfies LedgerClient for orchestration tests. Each
// method is a function field so individual tests override only what
// they care about; unset hooks return zero values + nil error, which
// is the "happy default" most tests expect (empty parties / empty ACS).
//
// activeContractsItems is the stream the fake feeds back when a test
// doesn't replace ActiveContractsFn — the helper newStream() wraps it
// into a closed channel. ItemsErr, when set, is delivered as the
// final stream item (terminal error path) before the channel closes.
type fakeLedger struct {
	LedgerEndFn                func(ctx context.Context) (ledger.LedgerEnd, error)
	ActiveContractsFn          func(ctx context.Context, req ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error)
	ResolveActAndReadPartiesFn func(ctx context.Context) ([]string, error)
	ListKnownPartiesFn         func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error)
	GrantUserActAndReadAsFn    func(ctx context.Context, userID string, parties []string) error
	ListKnownPackagesFn        func(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error)
}

func (f *fakeLedger) LedgerEnd(ctx context.Context) (ledger.LedgerEnd, error) {
	if f.LedgerEndFn != nil {
		return f.LedgerEndFn(ctx)
	}
	return ledger.LedgerEnd{Offset: 1}, nil
}

func (f *fakeLedger) ActiveContracts(ctx context.Context, req ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
	if f.ActiveContractsFn != nil {
		return f.ActiveContractsFn(ctx, req)
	}
	return newStream(nil, nil), nil
}

func (f *fakeLedger) ResolveActAndReadParties(ctx context.Context) ([]string, error) {
	if f.ResolveActAndReadPartiesFn != nil {
		return f.ResolveActAndReadPartiesFn(ctx)
	}
	return nil, nil
}

func (f *fakeLedger) ListKnownParties(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
	if f.ListKnownPartiesFn != nil {
		return f.ListKnownPartiesFn(ctx)
	}
	return &adminv2.ListKnownPartiesResponse{}, nil
}

func (f *fakeLedger) GrantUserActAndReadAs(ctx context.Context, userID string, parties []string) error {
	if f.GrantUserActAndReadAsFn != nil {
		return f.GrantUserActAndReadAsFn(ctx, userID, parties)
	}
	return nil
}

func (f *fakeLedger) ListKnownPackages(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error) {
	if f.ListKnownPackagesFn != nil {
		return f.ListKnownPackagesFn(ctx)
	}
	// Happy default: both token-standard holding surfaces vetted, so
	// existing fixtures (which feed holdings via ActiveContracts) flow
	// through discovery unchanged.
	return &adminv2.ListKnownPackagesResponse{
		PackageDetails: []*adminv2.PackageDetails{
			{Name: "splice-api-token-holding-v1"},
			{Name: "splice-api-token-holding-v2"},
		},
	}, nil
}

// newStream returns a closed channel pre-populated with the items, then
// (if termErr != nil) one final StreamItem carrying that error before
// close. Matches the production contract on Client.ActiveContracts:
// terminal errors arrive as a StreamItem.Err and then the channel
// closes — there's no out-of-band error path.
func newStream(items []*lapiv2.GetActiveContractsResponse, termErr error) <-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse] {
	out := make(chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], len(items)+1)
	for _, it := range items {
		out <- ledger.StreamItem[*lapiv2.GetActiveContractsResponse]{Value: it}
	}
	if termErr != nil {
		out <- ledger.StreamItem[*lapiv2.GetActiveContractsResponse]{Err: termErr}
	}
	close(out)
	return out
}

// withFakeDial replaces dialLedgerFn for the duration of the test and
// restores it on cleanup. Returns the fake so the test can configure
// per-method hooks inline.
func withFakeDial(tb interface {
	Helper()
	Cleanup(func())
}, fake *fakeLedger) {
	tb.Helper()
	prev := dialLedgerFn
	dialLedgerFn = func(ctx context.Context, conn LedgerConn) (LedgerClient, func(), error) {
		return fake, func() {}, nil
	}
	tb.Cleanup(func() { dialLedgerFn = prev })
}

// emptyActiveContract is a no-op ContractEntry that decodes cleanly
// past the type-assertion in runBalanceLive / scanWorkspace but yields
// no interface views — used to flood the cap test without depending on
// the HoldingViewV2 record-walker.
func emptyActiveContract() *lapiv2.GetActiveContractsResponse {
	return &lapiv2.GetActiveContractsResponse{
		ContractEntry: &lapiv2.GetActiveContractsResponse_ActiveContract{
			ActiveContract: &lapiv2.ActiveContract{},
		},
	}
}
