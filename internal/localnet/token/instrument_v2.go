package token

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	regstate "github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// On-ledger instrument lifecycle for the bundled splice-test-token-v2
// example token. Unlike Amulet (an externally-administered V1-era asset
// reached through the scan registry), the test token's TokenRules
// contract — created here with `admin = <our party>` — IS the registry:
// it directly implements V2.TransferFactory, so transfers exercise it
// on-ledger with an empty choice context, no off-ledger HTTP call.
//
// This is what makes us the issuer: we control the admin party, so we
// can mint freely (TokenRules_OfferMint is `controller admin`).

// ensureTokenRules dials the ledger and creates the issuer's TokenRules
// contract if it doesn't already exist. Called by RunCreate's live
// path. The issuer party (opts.Issuer) is the admin/signatory, so the
// dial must act as it — the auto-grant on dial covers the rights.
func ensureTokenRules(opts CreateOptions) error {
	ctx := context.Background()
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return err
	}
	defer cleanup()

	admin := opts.Issuer
	existing, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return fmt.Errorf("look up existing TokenRules: %w", err)
	}
	if existing != "" {
		return nil // already anchored for this admin
	}
	if _, err := createTokenRules(ctx, client, admin); err != nil {
		return err
	}
	return nil
}

// runMintLive performs an asset-specific mint of a test-token
// instrument: find the issuer's TokenRules, exercise
// TokenRules_OfferMint (controller = admin), then accept the resulting
// TokenTransferOffer so the holding lands in the receiver's account.
// The issuer party (ref.IssuerParty) is the admin; we dial as the
// instrument's role and auto-grant covers acting as both admin and
// receiver (LocalNet hosts them on the same participant).
func runMintLive(ctx context.Context, out io.Writer, opts MintOptions, ref regstate.TokenRef) error {
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return err
	}
	defer cleanup()

	admin := ref.IssuerParty
	tokenRulesCID, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return fmt.Errorf("look up TokenRules: %w", err)
	}
	if tokenRulesCID == "" {
		return fmt.Errorf("no on-ledger TokenRules for issuer %s — "+
			"run `localnet token create --endpoint ...` first", admin)
	}

	offerCID, err := mintViaOfferMint(ctx, client, admin, tokenRulesCID, opts.To, opts.Amount, ref.InstrumentID)
	if err != nil {
		return err
	}
	emit(out, "mint: offered", map[string]any{
		"offer_cid": offerCID, "to": opts.To, "amount": opts.Amount,
	})

	// Settle the offer by accepting it. The test token gates settlement
	// behind its configurable AccountConfig authorization model: the
	// accept needs AccountConfig contracts (signatory owner+provider)
	// for the involved accounts, referenced by id in the choice
	// context under "testTokenV2/accountConfigs". Wiring that account-
	// config state machine is the remaining step (tracked separately);
	// until then the mint authorization lands on-ledger as a
	// TokenTransferOffer the receiver can settle once configs exist.
	if err := acceptMintOffer(ctx, client, opts.To, offerCID); err != nil {
		return fmt.Errorf(
			"mint authorized on-ledger (offer %s created) but settlement "+
				"failed: the splice-test-token-v2 accept requires AccountConfig "+
				"contracts for the involved accounts — wiring that account-config "+
				"model is the remaining step. Underlying error: %w", offerCID, err)
	}
	emit(out, "mint: accepted", map[string]any{
		"instrument": ref.Symbol, "to": opts.To, "amount": opts.Amount,
	})
	return nil
}

// acceptMintOffer accepts a TokenTransferOffer created by OfferMint via
// its TransferInstruction interface — the offer implements
// TransferInstructionV2, so the receiver exercises
// TransferInstruction_Accept on it with an empty choice context (the
// test token needs no off-ledger context).
func acceptMintOffer(ctx context.Context, client *ledger.Client, receiver, offerCID string) error {
	choiceArg := recordValue([]field{
		{"actors", listValue([]string{receiver}, partyValue)},
		{"extraArgs", recordValue([]field{
			{"context", recordValue([]field{{"values", emptyTextMap()}})},
			{"meta", buildMetadataRecord(registry.Metadata{Values: map[string]string{}})},
		})},
	})
	pkg, mod, entity := splitInterfaceID(TransferInstructionInterfaceV2)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     offerCID,
				Choice:         "TransferInstruction_Accept",
				ChoiceArgument: choiceArg,
			},
		},
	}
	_, err := submitForTransaction(ctx, client, receiver, []*lapiv2.Command{exercise}, nil,
		createdContractFormat(receiver, ""))
	return err
}

// emptyTextMap builds an empty `TextMap AnyValue` for the choice
// context of a no-context accept.
func emptyTextMap() *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{}}}
}

// createTokenRules creates the issuer-owned TokenRules contract that
// anchors a test-token instrument. Returns the created contract id.
//
//	template TokenRules with admin : Party  (signatory admin)
//
// Single-signer create: the admin (our acting party) is the only
// signatory, so a plain submit-and-wait suffices.
func createTokenRules(ctx context.Context, client *ledger.Client, admin string) (string, error) {
	// CreateCommand needs a concrete package id — Canton does NOT
	// resolve the `#package-name` reference for creates (only for
	// interface-choice exercises under smart-contract upgrade). Resolve
	// the vetted splice-test-token-v2 package id at runtime so we stay
	// robust across the alpha's weekly snapshot rotation.
	pkgID, err := resolvePackageID(ctx, client, "splice-test-token-v2")
	if err != nil {
		return "", err
	}
	_, mod, entity := splitInterfaceID(TestTokenV2RulesTemplateID)
	create := &lapiv2.Command{
		Command: &lapiv2.Command_Create{
			Create: &lapiv2.CreateCommand{
				TemplateId: &lapiv2.Identifier{
					PackageId:  pkgID,
					ModuleName: mod,
					EntityName: entity,
				},
				CreateArguments: &lapiv2.Record{
					Fields: []*lapiv2.RecordField{
						{Label: "admin", Value: partyValue(admin)},
					},
				},
			},
		},
	}
	// TokenRules is a template (not an interface) — use the wildcard
	// created-event format so firstCreatedOfTemplate can match it by
	// entity name.
	resp, err := submitForTransaction(ctx, client, admin, []*lapiv2.Command{create}, nil,
		createdContractFormat(admin, ""))
	if err != nil {
		return "", fmt.Errorf("create TokenRules: %w", err)
	}
	cid := firstCreatedOfTemplate(resp, "TokenRules")
	if cid == "" {
		return "", fmt.Errorf("TokenRules created but contract id not found in transaction response")
	}
	return cid, nil
}

// findTokenRules returns the admin's existing TokenRules contract id, or
// "" when none exists yet. Lets create be idempotent — one TokenRules
// per admin party is enough to anchor every instrument that admin issues
// (the instrument identity is the (admin, id) pair carried per holding,
// not per-TokenRules).
func findTokenRules(ctx context.Context, client *ledger.Client, admin string) (string, error) {
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return "", fmt.Errorf("ledger end: %w", err)
	}
	// ACS template filters take the `#package-name` reference form
	// (the opposite of CreateCommand, which needs a concrete id).
	pkg, mod, entity := splitInterfaceID(TestTokenV2RulesTemplateID)
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
		},
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return "", fmt.Errorf("query TokenRules: %w", err)
	}
	for item := range stream {
		if item.Err != nil {
			return "", item.Err
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		if ce := entry.ActiveContract.GetCreatedEvent(); ce != nil {
			return ce.GetContractId(), nil
		}
	}
	return "", nil
}

// mintViaOfferMint exercises TokenRules_OfferMint on the issuer's
// TokenRules contract, then accepts the resulting offer so the holding
// actually lands in the receiver's account. Mint in this token is an
// offer (the upstream pattern), so a complete mint = exercise + accept.
//
// OfferMint args (TestTokenV2.daml):
//
//	receiver       : Account       — { owner, provider, id }
//	amount         : Decimal
//	instrumentId   : InstrumentId  — { admin, id }
//	offeredAt      : Time          — must be in the past
//	receiverConfig : AccountConfig — per-account auth rules
//
// Returns the resulting offer contract id (for the accept that follows).
func mintViaOfferMint(
	ctx context.Context,
	client *ledger.Client,
	admin, tokenRulesCID, receiver, amount, instrumentID string,
) (string, error) {
	offeredAt := time.Now().UTC().Add(-5 * time.Second) // "in the past" per assertDeadlineExceeded
	// The OfferMint `receiver` account MUST equal receiverConfig.account
	// — the choice keys its account-config map by receiverConfig.account
	// and then looks up `receiver` in it ("Cannot compute next actors"
	// otherwise). Both carry provider=admin so the AccountConfig's
	// `isSome account.provider` ensure passes.
	adminParty := admin
	ownerParty := receiver
	receiverAccount := registry.Account{Owner: &ownerParty, Provider: &adminParty, ID: ""}
	choiceArg := recordValue([]field{
		{"receiver", buildAccountRecord(receiverAccount)},
		{"amount", numericValue(amount)},
		{"instrumentId", buildInstrumentIDRecord(registry.InstrumentID{Admin: admin, ID: instrumentID})},
		{"offeredAt", timestampValue(offeredAt)},
		{"receiverConfig", buildAccountConfigRecord(admin, receiver)},
	})
	pkg, mod, entity := splitInterfaceID(TestTokenV2RulesTemplateID)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     tokenRulesCID,
				Choice:         TestTokenV2OfferMintChoice,
				ChoiceArgument: choiceArg,
			},
		},
	}
	resp, err := submitForTransaction(ctx, client, admin, []*lapiv2.Command{exercise}, nil,
		// The OfferMint creates a TokenTransferOffer; capture it so the
		// caller can accept it. Match by the offer template's entity.
		createdContractFormat(admin, ""))
	if err != nil {
		return "", fmt.Errorf("exercise TokenRules_OfferMint: %w", err)
	}
	cid := firstCreatedOfTemplate(resp, "TokenTransferOffer")
	if cid == "" {
		return "", fmt.Errorf("OfferMint succeeded but offer contract id not found")
	}
	return cid, nil
}

// buildAccountConfigRecord builds the AccountConfig the OfferMint
// receiverConfig expects:
//
//	template AccountConfig with
//	  admin         : Party
//	  account       : Account
//	  ownerConfig   : PartyConfig   — { canInitiate, mustApprove }
//	  providerConfig: PartyConfig
//
// We mirror the mintConfig the choice builds internally: owner can
// initiate + need not approve, provider neither. provider is set to the
// admin so the Account passes its `isSome account.provider` ensure
// clause.
func buildAccountConfigRecord(admin, owner string) *lapiv2.Value {
	ownerParty := owner
	adminParty := admin
	account := registry.Account{Owner: &ownerParty, Provider: &adminParty, ID: ""}
	return recordValue([]field{
		{"admin", partyValue(admin)},
		{"account", buildAccountRecord(account)},
		{"ownerConfig", buildPartyConfigRecord(true, false)},
		{"providerConfig", buildPartyConfigRecord(false, false)},
	})
}

// buildPartyConfigRecord: data PartyConfig with canInitiate, mustApprove : Bool.
func buildPartyConfigRecord(canInitiate, mustApprove bool) *lapiv2.Value {
	return recordValue([]field{
		{"canInitiate", boolValue(canInitiate)},
		{"mustApprove", boolValue(mustApprove)},
	})
}

func boolValue(b bool) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Bool{Bool: b}}
}

// resolvePackageID returns the concrete package id of the vetted
// package with the given package-name. Creates and template-filtered
// ACS queries need the concrete id (the `#name` reference form is only
// resolved for interface-choice exercises). Resolving at runtime keeps
// us robust across the V2 alpha's weekly snapshot rotation.
func resolvePackageID(ctx context.Context, client *ledger.Client, name string) (string, error) {
	resp, err := client.ListKnownPackages(ctx)
	if err != nil {
		return "", fmt.Errorf("list known packages: %w", err)
	}
	for _, p := range resp.GetPackageDetails() {
		if p.GetName() == name {
			return p.GetPackageId(), nil
		}
	}
	return "", fmt.Errorf("package %q not vetted on this participant — "+
		"upload it first (`localnet dar upload <%s.dar>`)", name, name)
}

// submitForTransaction is the generalised submit used by the instrument
// lifecycle (create / mint), mirroring submitExercise but taking the
// TransactionFormat explicitly so callers can request whichever created
// events they need to mine for contract ids.
func submitForTransaction(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	commands []*lapiv2.Command,
	disclosed []*lapiv2.DisclosedContract,
	txFormat *lapiv2.TransactionFormat,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	cmdID, err := newCommandID()
	if err != nil {
		return nil, fmt.Errorf("generate command id: %w", err)
	}
	req := &lapiv2.SubmitAndWaitForTransactionRequest{
		Commands: &lapiv2.Commands{
			UserId:             exerciseUserID,
			CommandId:          cmdID,
			ActAs:              []string{actAs},
			Commands:           commands,
			DisclosedContracts: disclosed,
		},
		TransactionFormat: txFormat,
	}
	subCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.SubmitAndWaitForTransaction(subCtx, req)
}

// createdContractFormat builds a TransactionFormat that returns the
// party's created events (ACS_DELTA) with template ids populated
// (verbose), so firstCreatedOfTemplate can match by entity name. The
// optional interfaceID adds an interface-view filter; pass "" for a
// plain wildcard-template view.
func createdContractFormat(party, interfaceID string) *lapiv2.TransactionFormat {
	var cumulative []*lapiv2.CumulativeFilter
	if interfaceID != "" {
		pkg, mod, entity := splitInterfaceID(interfaceID)
		cumulative = []*lapiv2.CumulativeFilter{{
			IdentifierFilter: &lapiv2.CumulativeFilter_InterfaceFilter{
				InterfaceFilter: &lapiv2.InterfaceFilter{
					InterfaceId:          &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
					IncludeInterfaceView: true,
				},
			},
		}}
	} else {
		cumulative = []*lapiv2.CumulativeFilter{{
			IdentifierFilter: &lapiv2.CumulativeFilter_WildcardFilter{
				WildcardFilter: &lapiv2.WildcardFilter{},
			},
		}}
	}
	return &lapiv2.TransactionFormat{
		TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{party: {Cumulative: cumulative}},
			Verbose:        true,
		},
	}
}

// firstCreatedOfTemplate returns the contract id of the first created
// event whose template entity name matches `entity`. Matches on entity
// name alone — the package id is the alpha snapshot hash and rotates.
func firstCreatedOfTemplate(resp *lapiv2.SubmitAndWaitForTransactionResponse, entity string) string {
	tx := resp.GetTransaction()
	if tx == nil {
		return ""
	}
	for _, ev := range tx.GetEvents() {
		created := ev.GetCreated()
		if created == nil {
			continue
		}
		if created.GetTemplateId().GetEntityName() == entity {
			return created.GetContractId()
		}
	}
	return ""
}
