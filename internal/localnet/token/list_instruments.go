package token

import (
	"context"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// ListInstruments is the single live-then-recorded decision behind both
// `dpm localnet token ls` and the Web UI's GET /api/tokens. With a live
// endpoint it returns on-chain ACS discovery (so Amulet and every minted
// token appear); when the endpoint is empty or discovery fails it falls
// back to the instruments recorded at create. Both surfaces render from
// the returned types.TokenListResponse, so the orchestration and the wire
// keys ("instruments" vs "tokens") live here, not duplicated per surface.
//
// The caller resolves the endpoint (the CLI via ResolveLedgerEndpoint, the
// handler via its liveLedgerEndpoint seam) and passes it in; an empty
// endpoint means "no live ledger — use the recorded list".
func ListInstruments(ctx context.Context, opts BalanceOptions) (types.TokenListResponse, error) {
	resp := types.TokenListResponse{SchemaVersion: types.SchemaVersion}
	if opts.Endpoint != "" {
		if insts, err := RunInstruments(ctx, opts); err == nil {
			instruments := make([]types.InstrumentRef, len(insts))
			for i, it := range insts {
				instruments[i] = types.InstrumentRef(it)
			}
			resp.Instruments = &instruments
			return resp, nil
		}
		// Discovery failed (e.g. ledger momentarily unreachable) — fall
		// through to the recorded list rather than erroring the surface.
	}
	refs, err := ListTokens(opts.Instance)
	if err != nil {
		return types.TokenListResponse{}, err
	}
	tokens := make([]types.TokenRef, len(refs))
	for i, r := range refs {
		tokens[i] = types.TokenRef(r)
	}
	resp.Tokens = &tokens
	return resp, nil
}
