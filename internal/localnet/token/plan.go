package token

import (
	"context"
	"fmt"
	"math/big"
)

// TransferPlan is the dry-run preview of a transfer: which Holding
// contracts (UTXOs) would be consumed, the change returned to the
// sender, and whether the sender can cover the amount. Powers the Web
// UI's coin-selection preview — no ledger mutation.
type TransferPlan struct {
	Instrument string      `json:"instrument"`
	From       string      `json:"from"`
	Amount     string      `json:"amount"`
	Inputs     []PlanInput `json:"inputs"`      // holdings that would be consumed
	TotalInput string      `json:"total_input"` // sum of inputs
	Change     string      `json:"change"`      // total_input - amount, returned to sender
	Sufficient bool        `json:"sufficient"`  // sender can cover the amount
	Shortfall  string      `json:"shortfall,omitempty"`
}

// PlanInput is one Holding contract that the transfer would archive.
type PlanInput struct {
	ContractID string `json:"contract_id"`
	Amount     string `json:"amount"`
}

// RunTransferPlan computes the coin selection for a transfer without
// submitting it. Dials, lists the sender's holdings of the instrument,
// greedily selects smallest-first to cover the amount, and reports the
// consume/change split. Insufficient funds is a non-error result
// (Sufficient=false + Shortfall) so the UI can render it inline.
func RunTransferPlan(ctx context.Context, opts TransferOptions) (*TransferPlan, error) {
	if err := requireFields("transfer plan", "instance", opts.Instance, "instrument", opts.Instrument,
		"sender party", opts.From, "recipient party", opts.To, "amount", opts.Amount); err != nil {
		return nil, err
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	aliases := aliasMapForInstance(opts.Instance)
	opts.From = ResolveAlias(aliases, opts.From)
	opts.To = ResolveAlias(aliases, opts.To)
	if err := validatePartyID("--from", opts.From); err != nil {
		return nil, err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return nil, err
	}
	if err := validateAmount("transfer", opts.Amount); err != nil {
		return nil, err
	}
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		return nil, unresolvedLedgerEndpoint(opts.Instance, opts.Role)
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	holdings, err := listSenderHoldings(ctx, client, opts.From, ref.InstrumentID)
	if err != nil {
		return nil, fmt.Errorf("list sender holdings: %w", err)
	}

	plan := &TransferPlan{
		Instrument: ref.InstrumentID,
		From:       opts.From,
		Amount:     opts.Amount,
	}
	picked, total, selErr := selectInputHoldings(holdings, opts.Amount)
	if selErr != nil {
		// Insufficient funds — report the shortfall, not an error.
		var have big.Float
		for _, h := range holdings {
			a, _ := new(big.Float).SetString(h.Amount)
			have.Add(&have, a)
		}
		want, _ := new(big.Float).SetString(opts.Amount)
		short := new(big.Float).Sub(want, &have)
		plan.TotalInput = have.Text('f', 10)
		plan.Sufficient = false
		plan.Shortfall = short.Text('f', 10)
		return plan, nil
	}
	for _, h := range picked {
		plan.Inputs = append(plan.Inputs, PlanInput{ContractID: h.ContractID, Amount: h.Amount})
	}
	plan.TotalInput = total
	plan.Sufficient = true
	// change = total_input - amount
	t, _ := new(big.Float).SetString(total)
	a, _ := new(big.Float).SetString(opts.Amount)
	plan.Change = new(big.Float).Sub(t, a).Text('f', 10)
	return plan, nil
}
