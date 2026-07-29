package token

import (
	"context"
	"io"
	"math/big"
)

// FaucetOptions funds a party from a well-known source. A thin wrapper
// over the transfer engine: move `Amount` of `Instrument` from a funded
// source party to `To`, auto-accepted, so a fresh party is funded in
// one step with no instruction id to hand off.
type FaucetOptions struct {
	Instance   string
	Instrument string
	To         string // recipient (alias or party id)
	Amount     string
	Source     string // funder; empty defaults to the role's own funded party

	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}

// RunFaucet funds opts.To with opts.Amount of opts.Instrument from a
// funded source party, auto-accepting the resulting transfer. The source
// defaults to the role's own party (its seeded alias, e.g. "app-user",
// which holds the network's Amulet) — pass Source explicitly to fund from
// a different holder (e.g. the issuer of a created token).
// defaultFaucetSource picks the party to dispense from when the caller
// didn't name one: the largest current holder of the instrument.
//
// The role's own party is only a sensible default for Amulet, which the
// LocalNet bootstrap funds. A token created here starts with its supply
// wherever it was minted — often a dedicated holder — so defaulting to the
// role party made the faucet fail with "sender holds no units of this
// instrument" for every user-created instrument. Falls back to the role
// party when nothing holds the instrument yet, which surfaces that same
// (now accurate) error.
func defaultFaucetSource(ctx context.Context, opts FaucetOptions) string {
	fallback := roleOrDefault(opts.Role)
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = ResolveLedgerEndpoint(opts.Instance, roleOrDefault(opts.Role))
	}
	if endpoint == "" {
		return fallback
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	sum, err := RunInstrumentSummary(ctx, BalanceOptions{
		Instance: opts.Instance, Role: roleOrDefault(opts.Role), Insecure: opts.Insecure,
		Endpoint: endpoint, Instrument: ref.InstrumentID,
	})
	if err != nil {
		return fallback
	}
	// Holders come back biggest-first, so the first non-zero entry is the
	// party best able to cover the requested amount.
	for _, h := range sum.Holders {
		if r, ok := new(big.Rat).SetString(zeroIfEmpty(h.Balance)); ok && r.Sign() > 0 {
			return h.Party
		}
	}
	return fallback
}

func RunFaucet(ctx context.Context, out io.Writer, opts FaucetOptions) error {
	if err := requireFields("faucet", "instance", opts.Instance, "instrument", opts.Instrument,
		"recipient party", opts.To, "amount", opts.Amount); err != nil {
		return err
	}
	source := opts.Source
	if source == "" {
		source = defaultFaucetSource(ctx, opts)
	}
	return RunTransfer(ctx, out, TransferOptions{
		Instance:    opts.Instance,
		Instrument:  opts.Instrument,
		From:        source,
		To:          opts.To,
		Amount:      opts.Amount,
		AutoAccept:  true,
		Endpoint:    opts.Endpoint,
		Token:       opts.Token,
		Role:        opts.Role,
		Insecure:    opts.Insecure,
		RegistryURL: opts.RegistryURL,
	})
}
