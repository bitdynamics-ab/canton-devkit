package token

import (
	"context"
	"io"
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
func RunFaucet(ctx context.Context, out io.Writer, opts FaucetOptions) error {
	if err := requireFields("faucet", opts.Instance, opts.Instrument, opts.To, opts.Amount); err != nil {
		return err
	}
	source := opts.Source
	if source == "" {
		// The role's seeded alias resolves to its own funded party.
		source = roleOrDefault(opts.Role)
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
