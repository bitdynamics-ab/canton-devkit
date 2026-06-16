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
// Why this exists (the bug it fixes): the off-ledger path in
// run_transfer.go POSTs to the LocalNet scan registry's
// /registry/transfer-instruction/v2/transfer-factory, which is served
// by the Splice scan app and returns the *Amulet* TransferFactory whose
// admin is the DSO party. Exercising that factory for an
// issuer-administered instrument trips
// TestTokenV2's `token_transferFactory_transferImpl`:
//
//	require' transfer.instrumentId.admin == transferFactory.admin
//
// → "AssertionFailed: Expected admin 'issuer::…' matches actual admin
// 'DSO::…'". For the test token the issuer's own TokenRules contract IS
// the V2 TransferFactory (it implements the interface, admin = issuer),
// so we exercise THAT on-ledger — no off-ledger HTTP call — exactly as
// mint/burn already do for the same instrument (ref.Status=="on-ledger").
//
// Flow (sender → receiver, both controlled on LocalNet):
//
//  1. find the issuer's TokenRules contract (the factory).
//  2. ACS-query the sender's holdings, group by account, pick the inputs.
//  3. ensure the sender account's AccountConfig exists (provider-scoped
//     accounts need one; self-custodial accounts use the basic config).
//  4. exercise TransferFactory_Transfer on the TokenRules contract with
//     the test-token choice context — creates a pending TokenTransferOffer
//     (a V2 TransferInstruction).
//  5. when --auto-accept is set, exercise TransferInstruction_Accept on
//     that offer as the receiver to settle in the same flow.

// runTransferOnLedgerFn is the dispatch seam RunTransfer uses for the
// on-ledger (issuer-administered test-token) transfer path. A package
// var so tests can assert the dispatch routes here for on-ledger
// instruments — and away from the off-ledger scan-registry path — without
// a live participant.
var runTransferOnLedgerFn = runTransferLiveOnLedger

// runAcceptOnLedgerFn is the dispatch seam RunAccept uses to try the
// on-ledger (test-token) accept before falling back to the off-ledger
// registry path. A package var for the same test-seam reason as
// runTransferOnLedgerFn.
var runAcceptOnLedgerFn = runAcceptOnLedgerIfTestToken

// runTransferOffLedgerFn / runAcceptOffLedgerFn are the off-ledger
// (Amulet / external-registry) dispatch seams. Package vars so the
// dispatch tests can assert that on-ledger instruments never reach the
// off-ledger scan-registry path (the original bug) without standing up a
// participant or an HTTP registry.
var runTransferOffLedgerFn = runTransferOffLedger

var runAcceptOffLedgerFn = runAcceptLive

// tokenAccount is the V2 Account a holding belongs to, plus the
// instrument coordinates carried alongside it. All of a sender's
// holdings for one (admin, instrument, provider, account-id) tuple
// share one tokenAccount; a transfer's inputs must too (the test
// token's sumAndArchiveHoldings asserts inputHolding.account ==
// transfer.sender).
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

// runTransferLiveOnLedger executes a transfer of an issuer-administered
// test-token instrument by exercising the issuer's on-ledger
// TransferFactory (the TokenRules contract), and — when AutoAccept is
// set — settling the resulting offer. Returns the pending
// TransferInstruction contract id (empty only if the response carried
// none). RunTransfer dispatches here when ref.Status=="on-ledger".
func runTransferLiveOnLedger(ctx context.Context, out io.Writer, opts TransferOptions, ref regstate.TokenRef) (string, error) {
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// The issuer's TokenRules contract IS the V2 TransferFactory. Its
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
	// Prefer the admin / instrument id recorded on the actual holdings —
	// authoritative over the local registry ref, which can drift.
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

	// Authorize the transfer as the sender's account parties. We act as
	// those parties plus the admin: the locked holdings + offer created
	// by the choice are co-signed by the admin, and on LocalNet we host
	// it, so submitting as the union avoids the off-ledger
	// authority-delegation dance.
	senderParties := accountPartiesOf(admin, senderAcct.Owner, senderAcct.Provider)
	factoryActAs := dedupParties(append(append([]string{}, senderParties...), admin))
	resp, err := exerciseTestTokenTransferFactory(
		ctx, client, tokenRulesCID, factoryActAs, senderParties, transferArgs, accountConfigCIDs,
	)
	if err != nil {
		return "", fmt.Errorf("exercise on-ledger TransferFactory_Transfer: %w", err)
	}
	// The on-ledger path is the V2 splice-test-token-v2.
	instructionID := findCreatedInstructionID(resp, genV2)
	emit(out, "transfer: submitted", map[string]any{
		"transfer_instruction_id": instructionID,
		"update_id":               resp.GetTransaction().GetUpdateId(),
		"factory_admin":           admin,
	})

	// Auto-accept settles the offer in the same flow (LocalNet hosts the
	// receiver). The receiver account is self-custodial, so it needs no
	// AccountConfig of its own — but the accept still references the
	// sender's config (extractAccountConfigMap covers both endpoints).
	if opts.AutoAccept && !opts.NoWait && instructionID != "" {
		receiverParties := accountPartiesOf(admin, opts.To, "")
		acceptActAs := dedupParties(append(append(append([]string{}, senderParties...), receiverParties...), admin))
		if err := acceptTestTokenTransfer(
			ctx, client, instructionID, acceptActAs, receiverParties, tokenRulesCID, accountConfigCIDs,
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
// test-token offer (TokenTransferOffer). It reports handled=true once it
// has accepted (or failed to accept) on-ledger; handled=false means the
// instruction isn't a local test-token offer and RunAccept should fall
// back to the off-ledger registry path.
//
// Detection is conservative: it reads the offer's `transfer` record and
// routes on-ledger ONLY when the instrument's admin actually anchors a
// TokenRules contract on this participant. An Amulet instruction
// (admin = DSO, no TokenRules) therefore falls through to the off-ledger
// path, as do any instructions we can't read or parse.
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
	// plus the JWT's granted set (the receiver is an observer of its own
	// pending instruction).
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

	// Supply an AccountConfig for every provider-scoped endpoint; the
	// accept's extractAccountConfigMap covers both sender and receiver.
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

	actors := accountPartiesOf(admin, receiver.Owner, receiver.Provider)
	actAs := dedupParties(append(append(
		append([]string{}, accountPartiesOf(admin, sender.Owner, sender.Provider)...),
		actors...), admin))
	if err := acceptTestTokenTransfer(ctx, client, opts.TransferInstructionID, actAs, actors, tokenRulesCID, configCIDs); err != nil {
		return true, fmt.Errorf("exercise on-ledger TransferInstruction_Accept: %w", err)
	}
	emit(out, "accept: submitted", map[string]any{
		"transfer_instruction_id": opts.TransferInstructionID,
		"on_ledger":               true,
	})
	return true, nil
}

// fetchOfferTransfer reads a TokenTransferOffer's `transfer` record via
// EventsByContractId and returns the sender + receiver accounts and the
// instrument admin. ok=false when the contract isn't visible to the
// given parties, isn't a transfer offer, or the args can't be parsed.
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

// extractTransferFromArgs walks a TokenTransferOffer create-argument
// record for its `transfer` field and pulls out the sender/receiver
// accounts and the instrument admin. Returns ok=false when there's no
// transfer record or no admin (i.e. not a recognizable transfer offer).
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
// controller); `actAs` is the participant submission party set (sender
// parties + admin). The choice creates a pending TokenTransferOffer; the
// response carries the actAs[0] party's created events with the
// TransferInstructionV2 interface view so findCreatedInstructionID can
// surface its contract id.
func exerciseTestTokenTransferFactory(
	ctx context.Context,
	client *ledger.Client,
	tokenRulesCID string,
	actAs []string,
	actors []string,
	transferArgs registry.TransferArgs,
	accountConfigCIDs []string,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	// The bundled splice-test-token-v2 is a V2 instrument, so its
	// transfer record uses the V2 (Account) sender/receiver shape.
	choiceArg := recordValue([]field{
		{"transfer", buildTransferRecord(transferArgs, genV2)},
		{"actors", listValue(actors, partyValue)},
		{"extraArgs", buildTestTokenExtraArgs(tokenRulesCID, accountConfigCIDs)},
	})
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
// (the choice controller); `actAs` covers sender + receiver parties +
// admin so the consequences (archiving the admin-co-signed locked
// holdings, creating the receiver's holding) are authorized without
// off-ledger disclosure. The choice context is the same test-token
// context the factory used.
func acceptTestTokenTransfer(
	ctx context.Context,
	client *ledger.Client,
	instructionID string,
	actAs []string,
	actors []string,
	tokenRulesCID string,
	accountConfigCIDs []string,
) error {
	choiceArg := recordValue([]field{
		{"actors", listValue(actors, partyValue)},
		{"extraArgs", buildTestTokenExtraArgs(tokenRulesCID, accountConfigCIDs)},
	})
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

// selectSenderAccountAndInputs groups the sender's holdings by account
// (same provider / account-id / admin / instrument) and returns the
// first account whose holdings cover `amount`, along with the picked
// inputs and their sum. The test token requires every input of a
// transfer to share one account, so we never mix across accounts.
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
		// All accounts came up short; surface the closest reason.
		return tokenAccount{}, nil, "", lastErr
	}
	return tokenAccount{}, nil, "", fmt.Errorf("no single account covers %s", amount)
}

// findOrCreateAccountConfig returns the contract id of the AccountConfig
// for (owner, provider) under `admin`, creating one if none exists.
// Idempotent: the mint flow already creates a receiver AccountConfig, so
// transfers from minted holdings reuse it rather than spawning duplicates
// (the registry rejects more than one config per account).
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

// findAccountConfigCID ACS-queries the admin's AccountConfig contracts
// and returns the contract id of the one whose account matches
// (owner, provider), or "" when none exists. The admin is an observer on
// every AccountConfig (so it can serve them as choice context), so the
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
			// Verbose so created-event record fields carry labels for
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
// lists (e.g. sender parties + receiver parties + admin).
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
