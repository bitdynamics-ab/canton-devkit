package token

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	regstate "github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// On-ledger V2 transfer for the bundled splice-test-token-v2 instrument.
//
// The off-ledger path returns the Amulet TransferFactory (admin = DSO),
// but TestTokenV2 asserts transfer.instrumentId.admin ==
// transferFactory.admin, so the Amulet factory rejects an
// issuer-administered instrument. The issuer's own TokenRules contract IS
// the V2 TransferFactory (admin = issuer), so we exercise THAT on-ledger,
// as mint/burn do for the same instrument (ref.Status=="on-ledger").

// Dispatch seams. Package vars so tests can assert the on-ledger vs
// off-ledger routing without a live participant or HTTP registry.
var runTransferOnLedgerFn = runTransferLiveOnLedger

var runAcceptOnLedgerFn = runAcceptOnLedgerIfTestToken

var runTransferOffLedgerFn = runTransferOffLedger

var runAcceptOffLedgerFn = runAcceptLive

// tokenAccount is the V2 Account a holding belongs to, plus the
// instrument coordinates carried alongside it. A transfer's inputs must
// all share one account: the test token's sumAndArchiveHoldings asserts
// inputHolding.account == transfer.sender.
type tokenAccount struct {
	Owner      string
	Provider   string // "" == None (self-custodial)
	AccountID  string
	Admin      string
	Instrument string
}

// registryAccount renders the tokenAccount as the registry.Account the
// transfer-record builder consumes (Optional Party encodes "" provider
// as None).
func (a tokenAccount) registryAccount() registry.Account {
	owner := a.Owner
	acct := registry.Account{Owner: &owner, ID: a.AccountID}
	if a.Provider != "" {
		p := a.Provider
		acct.Provider = &p
	}
	return acct
}

// runTransferLiveOnLedger transfers an issuer-administered test-token
// instrument by exercising the issuer's on-ledger TransferFactory (the
// TokenRules contract), settling the resulting offer when AutoAccept is
// set. Returns the pending TransferInstruction contract id.
func runTransferLiveOnLedger(ctx context.Context, out io.Writer, opts TransferOptions, ref regstate.TokenRef) (string, error) {
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialSenderFn(ctx, conn)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// The issuer's TokenRules contract IS the V2 TransferFactory; its
	// admin (signatory) is the issuer party, so the factory-admin check
	// inside TransferFactory_Transfer matches the instrument's admin.
	admin := ref.IssuerParty
	tokenRulesCID, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return "", fmt.Errorf("look up TokenRules: %w", err)
	}
	if tokenRulesCID == "" {
		return "", fmt.Errorf("no on-ledger TokenRules for issuer %s — "+
			"run `localnet token create --endpoint ...` first", admin)
	}

	// Pick the sender's input holdings within a single account (the
	// test token forbids mixing accounts in one transfer).
	holdings, err := listSenderHoldings(ctx, client, opts.From, ref.InstrumentID)
	if err != nil {
		return "", fmt.Errorf("list sender holdings: %w", err)
	}
	senderAcct, picked, total, err := selectSenderAccountAndInputs(holdings, opts.Amount)
	if err != nil {
		return "", err
	}
	// Prefer the admin / instrument id on the holdings — authoritative
	// over the local registry ref, which can drift.
	if senderAcct.Admin != "" {
		admin = senderAcct.Admin
	}
	instrumentID := senderAcct.Instrument
	if instrumentID == "" {
		instrumentID = ref.InstrumentID
	}
	emit(out, "transfer: selected", map[string]any{
		"input_count": len(picked), "total_input": total, "amount": opts.Amount,
		"sender_account": senderAcct.Owner, "provider": senderAcct.Provider,
	})

	// Provider-scoped accounts need an AccountConfig in the choice
	// context; self-custodial (provider=None) accounts use the basic
	// config the choice synthesizes, so no contract is supplied.
	var accountConfigCIDs []string
	if senderAcct.Provider != "" {
		cid, err := findOrCreateAccountConfig(ctx, client, admin, senderAcct.Owner, senderAcct.Provider)
		if err != nil {
			return "", fmt.Errorf("ensure sender account config: %w", err)
		}
		accountConfigCIDs = []string{cid}
	}

	requestedAt, executeBefore := transferTimes()
	transferArgs := registry.TransferArgs{
		Sender:           senderAcct.registryAccount(),
		Receiver:         registry.NewOwnedAccount(opts.To),
		Amount:           opts.Amount,
		InstrumentID:     registry.InstrumentID{Admin: admin, ID: instrumentID},
		RequestedAt:      requestedAt,
		ExecuteBefore:    executeBefore,
		InputHoldingCids: contractIDsOf(picked),
		Meta:             registry.Metadata{Values: map[string]string{}},
	}

	// Act as the sender's account parties plus the admin: the locked
	// holdings + offer the choice creates are admin-co-signed, and on
	// LocalNet we host the admin, so the union avoids off-ledger
	// authority delegation.
	senderParties := accountPartiesOf(admin, senderAcct.Owner, senderAcct.Provider)
	factoryActAs := dedupParties(append(append([]string{}, senderParties...), admin))

	// Atomic path: batch the factory-transfer + receiver-accept into one
	// all-or-nothing ExecuteBatch. Only when the caller opted in and wants
	// the accept chained (with NoWait there's no accept to batch; a bare
	// transfer is already a single submit). See batch.go for the
	// partial-recovery tradeoff this gives up.
	if opts.Atomic && opts.AutoAccept && !opts.NoWait {
		return runTransferOnLedgerBatched(
			ctx, out, client, opts, admin, tokenRulesCID,
			senderAcct, senderParties, transferArgs, accountConfigCIDs,
		)
	}

	resp, err := exerciseTestTokenTransferFactory(
		ctx, client, tokenRulesCID, factoryActAs, senderParties, transferArgs, accountConfigCIDs,
	)
	if err != nil {
		return "", fmt.Errorf("exercise on-ledger TransferFactory_Transfer: %w", err)
	}
	instructionID := findCreatedInstructionID(resp, genV2)
	emit(out, "transfer: submitted", map[string]any{
		"transfer_instruction_id": instructionID,
		"update_id":               resp.GetTransaction().GetUpdateId(),
		"factory_admin":           admin,
	})

	// Auto-accept settles the offer in the same flow (LocalNet hosts the
	// receiver). The self-custodial receiver needs no AccountConfig of its
	// own, but the accept still references the sender's config.
	if opts.AutoAccept && !opts.NoWait && instructionID != "" {
		// Accept on the receiver's own participant; the sender's node
		// cannot act as a party it doesn't host.
		acceptClient := client
		var acceptCleanup func()
		senderConn := LedgerConn{
			Endpoint: opts.Endpoint, Insecure: opts.Insecure,
			Instance: opts.Instance, Role: opts.Role,
		}
		if aconn := resolveAcceptConn(senderConn, opts.Instance, opts.To); aconn.Role != opts.Role {
			acceptClient, acceptCleanup, err = dialLedgerConcreteFn(ctx, aconn)
			if err != nil {
				return instructionID, fmt.Errorf("dial receiver participant for accept: %w", err)
			}
			defer acceptCleanup()
		}
		receiverParties := accountPartiesOf(admin, opts.To, "")
		acceptActAs := dedupParties(append(append(append([]string{}, senderParties...), receiverParties...), admin))
		if err := acceptTestTokenTransfer(
			ctx, acceptClient, instructionID, acceptActAs, receiverParties, tokenRulesCID, accountConfigCIDs,
		); err != nil {
			return instructionID, fmt.Errorf("auto-accept on-ledger transfer %s: %w", instructionID, err)
		}
		emit(out, "transfer accepted", map[string]any{
			"transfer_instruction_id": instructionID,
			"receiver":                opts.To,
		})
	}
	return instructionID, nil
}

// runAcceptOnLedgerIfTestToken handles a standalone `transfer accept`
// when the pending TransferInstruction is an issuer-administered
// test-token offer. handled=false means it isn't a local test-token
// offer and RunAccept should fall back to the off-ledger registry path.
//
// Detection is conservative: it routes on-ledger only when the
// instrument's admin actually anchors a TokenRules contract here. An
// Amulet instruction (admin = DSO, no TokenRules) falls through, as do
// instructions we can't read or parse.
func runAcceptOnLedgerIfTestToken(ctx context.Context, out io.Writer, opts AcceptOptions) (bool, error) {
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		// Let the off-ledger path surface the dial error consistently.
		return false, nil
	}
	defer cleanup()

	// Parties that might see the offer: the explicit --party (receiver)
	// plus the JWT's granted set.
	parties, _ := client.ResolveActAndReadParties(ctx)
	if opts.Party != "" {
		parties = append([]string{opts.Party}, parties...)
	}
	sender, receiver, admin, ok := fetchOfferTransfer(ctx, client, dedupParties(parties), opts.TransferInstructionID)
	if !ok || admin == "" {
		return false, nil
	}
	tokenRulesCID, err := findTokenRules(ctx, client, admin)
	if err != nil || tokenRulesCID == "" {
		// admin doesn't anchor a TokenRules here (e.g. Amulet's DSO) —
		// not our token; defer to the off-ledger path.
		return false, nil
	}

	// Supply an AccountConfig for every provider-scoped endpoint (sender
	// and receiver).
	var configCIDs []string
	for _, a := range []tokenAccount{sender, receiver} {
		if a.Provider == "" {
			continue
		}
		cid, cerr := findOrCreateAccountConfig(ctx, client, admin, a.Owner, a.Provider)
		if cerr != nil {
			return true, fmt.Errorf("ensure account config: %w", cerr)
		}
		configCIDs = append(configCIDs, cid)
	}

	// Accept on the receiver's own participant; the initial client dials
	// the sender-side role which cannot act as receiver parties on a
	// different node.
	acceptClient := client
	var acceptCleanup func()
	initialConn := LedgerConn{
		Endpoint: opts.Endpoint, Insecure: opts.Insecure,
		Instance: opts.Instance, Role: opts.Role,
	}
	if aconn := resolveAcceptConn(initialConn, opts.Instance, receiver.Owner); aconn.Role != opts.Role {
		var aerr error
		acceptClient, acceptCleanup, aerr = dialLedgerConcreteFn(ctx, aconn)
		if aerr != nil {
			return true, fmt.Errorf("dial receiver participant for accept: %w", aerr)
		}
		defer acceptCleanup()
	}
	actors := accountPartiesOf(admin, receiver.Owner, receiver.Provider)
	actAs := dedupParties(append(append(
		append([]string{}, accountPartiesOf(admin, sender.Owner, sender.Provider)...),
		actors...), admin))
	if err := acceptTestTokenTransfer(ctx, acceptClient, opts.TransferInstructionID, actAs, actors, tokenRulesCID, configCIDs); err != nil {
		return true, fmt.Errorf("exercise on-ledger TransferInstruction_Accept: %w", err)
	}
	emit(out, "accept: submitted", map[string]any{
		"transfer_instruction_id": opts.TransferInstructionID,
		"on_ledger":               true,
	})
	return true, nil
}

// fetchOfferTransfer reads a TokenTransferOffer's `transfer` record and
// returns the sender + receiver accounts and the instrument admin.
// ok=false when the contract isn't visible, isn't a transfer offer, or
// can't be parsed.
func fetchOfferTransfer(ctx context.Context, client *ledger.Client, parties []string, cid string) (sender, receiver tokenAccount, admin string, ok bool) {
	if len(parties) == 0 || cid == "" {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = &lapiv2.Filters{}
	}
	resp, err := client.EventsByContractId(ctx, &lapiv2.GetEventsByContractIdRequest{
		ContractId:  cid,
		EventFormat: &lapiv2.EventFormat{FiltersByParty: byParty, Verbose: true},
	})
	if err != nil {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	cev := resp.GetCreated()
	if cev == nil || cev.GetCreatedEvent() == nil {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	return extractTransferFromArgs(cev.GetCreatedEvent().GetCreateArguments())
}

// extractTransferFromArgs pulls the sender/receiver accounts and
// instrument admin out of a TokenTransferOffer's create-argument record.
// ok=false when there's no transfer record or no admin.
func extractTransferFromArgs(args *lapiv2.Record) (sender, receiver tokenAccount, admin string, ok bool) {
	if args == nil {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	var transferRec *lapiv2.Record
	for _, f := range args.Fields {
		if f.Label == "transfer" {
			transferRec = recordOf(f.Value)
			break
		}
	}
	if transferRec == nil {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	var instrumentID string
	for _, f := range transferRec.Fields {
		switch f.Label {
		case "sender":
			sender = accountFromRecord(recordOf(f.Value))
		case "receiver":
			receiver = accountFromRecord(recordOf(f.Value))
		case "instrumentId":
			if rec := recordOf(f.Value); rec != nil {
				for _, af := range rec.Fields {
					switch af.Label {
					case "admin":
						admin = partyOf(af.Value)
					case "id":
						instrumentID = textOf(af.Value)
					}
				}
			}
		}
	}
	sender.Admin, sender.Instrument = admin, instrumentID
	receiver.Admin, receiver.Instrument = admin, instrumentID
	if admin == "" {
		return tokenAccount{}, tokenAccount{}, "", false
	}
	return sender, receiver, admin, true
}

// accountFromRecord parses a V2 Account record ({owner, provider, id})
// into a tokenAccount (Admin/Instrument left for the caller to fill).
func accountFromRecord(rec *lapiv2.Record) tokenAccount {
	if rec == nil {
		return tokenAccount{}
	}
	var a tokenAccount
	for _, af := range rec.Fields {
		switch af.Label {
		case "owner":
			a.Owner = optionalPartyOf(af.Value)
		case "provider":
			a.Provider = optionalPartyOf(af.Value)
		case "id":
			a.AccountID = textOf(af.Value)
		}
	}
	return a
}

// exerciseTestTokenTransferFactory submits TransferFactory_Transfer
// against the issuer's TokenRules contract (the on-ledger V2
// TransferFactory). `actors` is the sender's account parties (the choice
// controller); `actAs` is the submission party set (sender parties +
// admin). The choice creates a pending TokenTransferOffer; the response
// carries actAs[0]'s created events with the TransferInstructionV2
// interface view for findCreatedInstructionID.
func exerciseTestTokenTransferFactory(
	ctx context.Context,
	client *ledger.Client,
	tokenRulesCID string,
	actAs []string,
	actors []string,
	transferArgs registry.TransferArgs,
	accountConfigCIDs []string,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	choiceArg := testTokenTransferFactoryArg(transferArgs, actors, tokenRulesCID, accountConfigCIDs)
	pkg, mod, entity := splitInterfaceID(TransferFactoryInterfaceV2)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     tokenRulesCID,
				Choice:         "TransferFactory_Transfer",
				ChoiceArgument: choiceArg,
			},
		},
	}
	return submitForTransactionMulti(ctx, client, actAs, []*lapiv2.Command{exercise}, nil,
		transferInstructionTxFormat(actAs[0], genV2))
}

// acceptTestTokenTransfer submits TransferInstruction_Accept against a
// pending TokenTransferOffer. `actors` is the receiver's account parties
// (the choice controller); `actAs` covers sender + receiver + admin so
// the consequences (archiving the admin-co-signed locked holdings,
// creating the receiver's holding) are authorized without off-ledger
// disclosure.
func acceptTestTokenTransfer(
	ctx context.Context,
	client *ledger.Client,
	instructionID string,
	actAs []string,
	actors []string,
	tokenRulesCID string,
	accountConfigCIDs []string,
) error {
	choiceArg := testTokenAcceptArg(actors, tokenRulesCID, accountConfigCIDs)
	pkg, mod, entity := splitInterfaceID(TransferInstructionInterfaceV2)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     instructionID,
				Choice:         "TransferInstruction_Accept",
				ChoiceArgument: choiceArg,
			},
		},
	}
	_, err := submitForTransactionMulti(ctx, client, actAs, []*lapiv2.Command{exercise}, nil,
		createdContractFormat(actAs[0], ""))
	return err
}

// testTokenTransferFactoryArg builds the TransferFactory_Transfer choice
// argument ({transfer, actors, extraArgs}). Shared by the sequential
// exercise and the atomic batch path so the two can't drift.
func testTokenTransferFactoryArg(transferArgs registry.TransferArgs, actors []string, tokenRulesCID string, accountConfigCIDs []string) *lapiv2.Value {
	return recordValue([]field{
		{"transfer", buildTransferRecord(transferArgs, genV2)},
		{"actors", listValue(actors, partyValue)},
		{"extraArgs", buildTestTokenExtraArgs(tokenRulesCID, accountConfigCIDs)},
	})
}

// testTokenAcceptArg builds the TransferInstruction_Accept choice
// argument ({actors, extraArgs}). Shared by the sequential accept and
// the atomic batch path.
func testTokenAcceptArg(actors []string, tokenRulesCID string, accountConfigCIDs []string) *lapiv2.Value {
	return recordValue([]field{
		{"actors", listValue(actors, partyValue)},
		{"extraArgs", buildTestTokenExtraArgs(tokenRulesCID, accountConfigCIDs)},
	})
}

// runTransferOnLedgerBatched executes the transfer + receiver-accept as
// one atomic BatchingUtility_ExecuteBatch: it wraps the same choice args
// as TSA_* batch actions and threads the sender's input holdings through
// the batch's HoldingMap. The accept action targets the instruction the
// transfer action creates inside the SAME batch, so either both land or
// neither does. Returns "" — the batch settles both legs, leaving no
// pending instruction to hand back.
func runTransferOnLedgerBatched(
	ctx context.Context,
	out io.Writer,
	client *ledger.Client,
	opts TransferOptions,
	admin, tokenRulesCID string,
	senderAcct tokenAccount,
	senderParties []string,
	transferArgs registry.TransferArgs,
	accountConfigCIDs []string,
) (string, error) {
	batchUser := senderParties[0]
	receiverParties := accountPartiesOf(admin, opts.To, "")
	actAs := dedupParties(append(append(append([]string{}, senderParties...), receiverParties...), admin))

	// Action 2's target instruction is created by action 1 within the
	// batch, so we pass the factory cid as a placeholder the wallet
	// re-binds to the produced instruction.
	transferArg := testTokenTransferFactoryArg(transferArgs, senderParties, tokenRulesCID, accountConfigCIDs)
	acceptArg := testTokenAcceptArg(receiverParties, tokenRulesCID, accountConfigCIDs)
	actions := []batchAction{
		tsaTransferFactoryTransferV2(tokenRulesCID, transferArg),
		tsaTransferInstructionAcceptV2(tokenRulesCID, acceptArg),
	}

	// Thread the sender's picked input holdings through the HoldingMap.
	// Admin + Account + Instrument must match the transfer's
	// instrumentId.admin / sender / instrumentId.id, or ExecuteBatch's
	// getHoldingsForInstrument lookup misses (inputAmount = 0).
	inputHoldings := []scopedHoldings{{
		Admin:       admin,
		Account:     senderAcct.registryAccount(),
		Instrument:  transferArgs.InstrumentID.ID,
		HoldingCIDs: transferArgs.InputHoldingCids,
	}}

	res, err := executeBatch(ctx, client, batchUser, actAs, inputHoldings, actions, false)
	if err != nil {
		// TODO: atomic transfer+accept is experimental and not yet supported
		// on this Splice version. The batch's wire shape is correct, but
		// BatchingUtility_ExecuteBatch does not rebind the accept leg to the
		// TransferInstruction the transfer leg creates in the SAME batch —
		// that forward reference needs ExecuteBatch intra-batch output
		// binding, which the current test-token/wallet DARs don't wire. The
		// batch is all-or-nothing, so nothing committed.
		return "", fmt.Errorf(
			"atomic transfer+accept batching is experimental and not yet supported on this "+
				"Splice version (the accept leg can't reference the transfer leg's instruction "+
				"within one BatchingUtility_ExecuteBatch); nothing was committed — re-run "+
				"without --atomic for the sequential path. underlying: %w", err)
	}
	emit(out, "transfer batched", map[string]any{
		"update_id":    res.UpdateID,
		"action_count": len(res.Actions),
		"receiver":     opts.To,
		"atomic":       true,
	})
	// Callers treat an empty id as "already settled".
	return "", nil
}

// selectSenderAccountAndInputs groups the sender's holdings by account
// and returns the first account whose holdings cover `amount`, with the
// picked inputs and their sum. The test token requires every input of a
// transfer to share one account.
func selectSenderAccountAndInputs(holdings []holdingRef, amount string) (tokenAccount, []holdingRef, string, error) {
	if len(holdings) == 0 {
		return tokenAccount{}, nil, "", fmt.Errorf("sender holds no units of this instrument")
	}
	groups := map[string][]holdingRef{}
	var order []string
	for _, h := range holdings {
		k := h.Provider + "\x00" + h.AccountID + "\x00" + h.Admin + "\x00" + h.Instrument
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], h)
	}
	sort.Strings(order)
	var lastErr error
	for _, k := range order {
		g := groups[k]
		picked, total, err := selectInputHoldings(g, amount)
		if err != nil {
			lastErr = err
			continue
		}
		h0 := g[0]
		return tokenAccount{
			Owner:      h0.Owner,
			Provider:   h0.Provider,
			AccountID:  h0.AccountID,
			Admin:      h0.Admin,
			Instrument: h0.Instrument,
		}, picked, total, nil
	}
	if lastErr != nil {
		return tokenAccount{}, nil, "", lastErr
	}
	return tokenAccount{}, nil, "", fmt.Errorf("no single account covers %s", amount)
}

// findOrCreateAccountConfig returns the AccountConfig contract id for
// (owner, provider) under `admin`, creating one if none exists.
// Idempotent: transfers from minted holdings reuse the receiver
// AccountConfig the mint flow created (one config per account).
func findOrCreateAccountConfig(ctx context.Context, client *ledger.Client, admin, owner, provider string) (string, error) {
	cid, err := findAccountConfigCID(ctx, client, admin, owner, provider)
	if err != nil {
		return "", err
	}
	if cid != "" {
		return cid, nil
	}
	return createAccountConfig(ctx, client, admin, owner, provider)
}

// findAccountConfigCID returns the contract id of the admin's
// AccountConfig whose account matches (owner, provider), or "" when none
// exists. The admin is an observer on every AccountConfig, so the
// admin-party filter sees all of them.
func findAccountConfigCID(ctx context.Context, client *ledger.Client, admin, owner, provider string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return "", fmt.Errorf("ledger end: %w", err)
	}
	pkg, mod, entity := splitInterfaceID(accountConfigTemplateID)
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				admin: {Cumulative: []*lapiv2.CumulativeFilter{{
					IdentifierFilter: &lapiv2.CumulativeFilter_TemplateFilter{
						TemplateFilter: &lapiv2.TemplateFilter{
							TemplateId: &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
						},
					},
				}}},
			},
			// Verbose so created-event fields carry labels for
			// accountConfigMatches to walk by name.
			Verbose: true,
		},
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return "", fmt.Errorf("query AccountConfig: %w", err)
	}
	for item := range stream {
		if item.Err != nil {
			return "", item.Err
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		created := entry.ActiveContract.GetCreatedEvent()
		if created == nil {
			continue
		}
		if accountConfigMatches(created.GetCreateArguments(), owner, provider) {
			return created.GetContractId(), nil
		}
	}
	return "", nil
}

// accountConfigMatches reports whether an AccountConfig create-argument
// record's `account` field has the given owner + provider (provider ""
// means the Optional is None).
func accountConfigMatches(args *lapiv2.Record, owner, provider string) bool {
	if args == nil {
		return false
	}
	for _, f := range args.Fields {
		if f.Label != "account" {
			continue
		}
		rec := recordOf(f.Value)
		if rec == nil {
			return false
		}
		var gotOwner, gotProvider string
		for _, af := range rec.Fields {
			switch af.Label {
			case "owner":
				gotOwner = optionalPartyOf(af.Value)
			case "provider":
				gotProvider = optionalPartyOf(af.Value)
			}
		}
		return gotOwner == owner && gotProvider == provider
	}
	return false
}

// accountPartiesOf mirrors Splice.TokenStandard.Utils.accountParties:
// the account principal (the owner, or the admin for special accounts
// with no owner) plus the provider when it is set and distinct from the
// principal. This is who must authorize an account's side of a transfer.
func accountPartiesOf(admin, owner, provider string) []string {
	principal := owner
	if principal == "" {
		principal = admin
	}
	if provider != "" && provider != principal {
		return []string{principal, provider}
	}
	return []string{principal}
}

// dedupParties returns the input with duplicates and empties removed,
// order preserved — for assembling an actAs set from overlapping party
// lists.
func dedupParties(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range in {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
