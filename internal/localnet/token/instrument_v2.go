package token

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"strings"
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

	// Auto-bundle the test-token DARs FIRST — findTokenRules
	// filters the ACS by the #splice-test-token-v2 package name, which the
	// participant rejects with PACKAGE_NAMES_NOT_FOUND until the package is
	// vetted. Upload happens before any package-name-scoped query. No-op
	// when already vetted.
	if err := ensureTokenDARs(ctx, client, opts.Instance, nil); err != nil {
		return err
	}

	existing, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return fmt.Errorf("look up existing TokenRules: %w", err)
	}
	if existing != "" {
		return nil // already anchored for this admin
	}
	_, err = createTokenRules(ctx, client, admin)
	return err
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
	tokenRulesCID, tokenRulesDisc, err := findTokenRulesDisclosed(ctx, client, admin)
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

	// Settle the offer. The test token gates accept behind its
	// configurable AccountConfig model: the receiver's account needs an
	// AccountConfig contract, referenced by id in the accept's choice
	// context under "testTokenV2/accountConfigs". Create it (owner=
	// receiver, provider=admin) then accept with its cid threaded in.
	configCID, err := createAccountConfig(ctx, client, admin, opts.To, admin)
	if err != nil {
		return fmt.Errorf("create receiver account config: %w", err)
	}
	emit(out, "mint: account-config", map[string]any{"config_cid": configCID})

	if err := acceptMintOffer(ctx, client, opts.To, offerCID, configCID, tokenRulesCID, tokenRulesDisc); err != nil {
		return fmt.Errorf("accept mint offer: %w", err)
	}
	emit(out, "mint: accepted", map[string]any{
		"instrument": ref.Symbol, "to": opts.To, "amount": opts.Amount,
	})
	return nil
}

// runBurnLive burns `amount` of the holder's tokens by archiving their
// Holding contracts.
//
// The splice-test-token-v2 example has no protocol-level standalone burn:
// its transfer state machine unconditionally `create Token`s for the
// receiver, which violates the Token template's `ensure isSome
// account.owner` when the receiver is the special burn account — so you
// cannot transfer-to-burn-account. But the Token (holding) template's
// signatory is the account parties + the instrument admin, and on
// LocalNet we control all of them, so we exercise the built-in Archive
// choice directly to remove holdings from circulation. Supply = sum of
// holdings, so archiving reduces it.
//
// To stay amount-precise we select the smallest set of the holder's
// holdings covering `amount`, then in ONE atomic transaction archive them
// all and (if they overshoot) create a single change holding back to the
// holder. Atomicity means a partial burn can never strand the change.
func runBurnLive(ctx context.Context, out io.Writer, opts BurnOptions, ref regstate.TokenRef) error {
	conn := LedgerConn{
		Endpoint: opts.Endpoint, Token: opts.Token,
		Insecure: opts.Insecure, Instance: opts.Instance, Role: opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return err
	}
	defer cleanup()

	holdings, err := listSenderHoldings(ctx, client, opts.From, ref.InstrumentID)
	if err != nil {
		return fmt.Errorf("list holdings for %s: %w", opts.From, err)
	}
	picked, change, err := computeBurn(holdings, opts.Amount)
	if err != nil {
		return err
	}
	admin := inferAdminFromHoldings(picked)
	if admin == "" {
		admin = ref.IssuerParty
	}

	pkgID, err := resolvePackageID(ctx, client, "splice-test-token-v2")
	if err != nil {
		return err
	}
	_, hMod, hEntity := splitInterfaceID(TestTokenV2HoldingTemplateID)
	holdingTID := &lapiv2.Identifier{PackageId: pkgID, ModuleName: hMod, EntityName: hEntity}

	// Archive every selected holding (built-in Archive choice).
	commands := make([]*lapiv2.Command, 0, len(picked)+1)
	for _, h := range picked {
		commands = append(commands, &lapiv2.Command{
			Command: &lapiv2.Command_Exercise{
				Exercise: &lapiv2.ExerciseCommand{
					TemplateId:     holdingTID,
					ContractId:     h.ContractID,
					Choice:         "Archive",
					ChoiceArgument: recordValue(nil),
				},
			},
		})
	}
	// Re-create the change holding (one Token back to the holder) when
	// the selected holdings overshoot the burn amount.
	if change != "" && change != "0" {
		commands = append(commands, &lapiv2.Command{
			Command: &lapiv2.Command_Create{
				Create: &lapiv2.CreateCommand{
					TemplateId:      holdingTID,
					CreateArguments: buildHoldingRecord(picked[0], change),
				},
			},
		})
	}

	// Archive (and the change create) require ALL Token signatories:
	// the account parties plus the admin. Operator controls all on
	// LocalNet, so act as their union.
	actAs := burnActAs(picked, admin)
	if _, err := submitForTransactionMulti(ctx, client, actAs, commands, nil,
		createdContractFormat(opts.From, "")); err != nil {
		return fmt.Errorf("archive holdings (burn): %w", err)
	}

	emit(out, "burn complete", map[string]any{
		"instrument": ref.Symbol, "from": opts.From, "amount": opts.Amount,
		"holdings_archived": len(picked), "change": change,
	})
	return nil
}

// computeBurn selects the smallest set of holdings covering `amount` and
// returns the picked set + the change (selected sum − amount, "" or "0"
// when exact). Pure — unit-testable.
func computeBurn(holdings []holdingRef, amount string) ([]holdingRef, string, error) {
	picked, sum, err := selectInputHoldings(holdings, amount)
	if err != nil {
		return nil, "", err
	}
	s, _ := new(big.Float).SetString(sum)
	a, _ := new(big.Float).SetString(amount)
	change := new(big.Float).Sub(s, a)
	if change.Sign() <= 0 {
		return picked, "0", nil
	}
	return picked, strings.TrimRight(strings.TrimRight(change.Text('f', 10), "0"), "."), nil
}

// burnActAs is the unique set of parties needed to authorize archiving
// the picked holdings: each holding's account parties (owner + provider
// when set) plus the instrument admin.
func burnActAs(picked []holdingRef, admin string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for _, h := range picked {
		add(h.Owner)
		add(h.Provider)
	}
	add(admin)
	return out
}

// buildHoldingRecord builds the Token create argument (a record with one
// `holding : HoldingView` field) for the change holding, mirroring the
// account + instrument of `from` with the given amount.
func buildHoldingRecord(from holdingRef, amount string) *lapiv2.Record {
	owner := from.Owner
	acct := registry.Account{Owner: &owner, ID: from.AccountID}
	if from.Provider != "" {
		p := from.Provider
		acct.Provider = &p
	}
	view := recordValue([]field{
		{"account", buildAccountRecord(acct)},
		{"instrumentId", buildInstrumentIDRecord(registry.InstrumentID{Admin: from.Admin, ID: from.Instrument})},
		{"amount", numericValue(amount)},
		{"lock", &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}}},
		{"meta", buildMetadataRecord(registry.Metadata{Values: map[string]string{}})},
	})
	return &lapiv2.Record{Fields: []*lapiv2.RecordField{{Label: "holding", Value: view}}}
}

// acceptMintOffer accepts a TokenTransferOffer created by OfferMint via
// its TransferInstruction interface — the offer implements
// TransferInstructionV2, so the receiver exercises
// TransferInstruction_Accept on it with an empty choice context (the
// test token needs no off-ledger context).
func acceptMintOffer(ctx context.Context, client *ledger.Client, receiver, offerCID, accountConfigCID, tokenRulesCID string, tokenRulesDisc *lapiv2.DisclosedContract) error {
	// The accept's choice context must carry two well-known entries the
	// test token's state machine looks up: the issuer's TokenRules
	// contract id and the involved accounts' AccountConfig contract ids.
	// buildTestTokenExtraArgs assembles the tagged-AnyValue shape.
	choiceArg := recordValue([]field{
		{"actors", listValue([]string{receiver}, partyValue)},
		{"extraArgs", buildTestTokenExtraArgs(tokenRulesCID, []string{accountConfigCID})},
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
	var disclosed []*lapiv2.DisclosedContract
	if tokenRulesDisc != nil {
		disclosed = []*lapiv2.DisclosedContract{tokenRulesDisc}
	}
	_, err := submitForTransaction(ctx, client, receiver, []*lapiv2.Command{exercise}, disclosed,
		createdContractFormat(receiver, ""))
	return err
}

// Test token's well-known choice-context keys (see
// TestTokenV2.Util.tokenRulesContextKey +
// TestTokenV2.AccountConfig.accountConfigsContextKey). The accept/
// transfer state machine looks these up to find the TokenRules
// contract and the involved accounts' AccountConfig contracts.
const (
	accountConfigsContextKey = "testTokenV2/accountConfigs"
	tokenRulesContextKey     = "testTokenV2/tokenRules"
)

// createTokenRules creates the issuer-owned TokenRules contract that
// anchors a test-token instrument. Returns the created contract id.
//
//	template TokenRules with admin : Party (signatory admin)
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
	// Cancel the stream pump on every return path so an early break
	// (first match found) doesn't leak the goroutine for the lifetime
	// of the parent request context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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

// findTokenRulesDisclosed returns the issuer's TokenRules contract id
// PLUS a DisclosedContract for it. The mint receiver isn't a
// stakeholder on the TokenRules (signatory = admin), so the accept
// submission must disclose it (with its createdEventBlob) for the
// receiver's transaction to reference it via the choice context.
func findTokenRulesDisclosed(ctx context.Context, client *ledger.Client, admin string) (string, *lapiv2.DisclosedContract, error) {
	// Cancel the stream pump on every return path; first-match returns
	// otherwise leak the upstream goroutine.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("ledger end: %w", err)
	}
	pkg, mod, entity := splitInterfaceID(TestTokenV2RulesTemplateID)
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				admin: {Cumulative: []*lapiv2.CumulativeFilter{{
					IdentifierFilter: &lapiv2.CumulativeFilter_TemplateFilter{
						TemplateFilter: &lapiv2.TemplateFilter{
							TemplateId:              &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
							IncludeCreatedEventBlob: true,
						},
					},
				}}},
			},
		},
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("query TokenRules: %w", err)
	}
	for item := range stream {
		if item.Err != nil {
			return "", nil, item.Err
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		ac := entry.ActiveContract
		ce := ac.GetCreatedEvent()
		if ce == nil {
			continue
		}
		disc := &lapiv2.DisclosedContract{
			TemplateId:       ce.GetTemplateId(),
			ContractId:       ce.GetContractId(),
			CreatedEventBlob: ce.GetCreatedEventBlob(),
			SynchronizerId:   ac.GetSynchronizerId(),
		}
		return ce.GetContractId(), disc, nil
	}
	return "", nil, nil
}

// mintViaOfferMint exercises TokenRules_OfferMint on the issuer's
// TokenRules contract, then accepts the resulting offer so the holding
// actually lands in the receiver's account. Mint in this token is an
// offer (the upstream pattern), so a complete mint = exercise + accept.
//
// OfferMint args (TestTokenV2.daml):
//
//	receiver : Account — { owner, provider, id }
//	amount : Decimal
//	instrumentId : InstrumentId — { admin, id }
//	offeredAt : Time — must be in the past
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
//	  admin : Party
//	  account : Account
//	  ownerConfig : PartyConfig — { canInitiate, mustApprove }
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
	return submitForTransactionMulti(ctx, client, []string{actAs}, commands, disclosed, txFormat)
}

// submitForTransactionMulti is the multi-actAs variant — needed when a
// contract has several required authorizers (e.g. AccountConfig, whose
// signatory is account.owner AND account.provider). On LocalNet all
// parties are operator-controlled, so acting as several at once is
// legitimate.
func submitForTransactionMulti(
	ctx context.Context,
	client *ledger.Client,
	actAs []string,
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
			ActAs:              actAs,
			Commands:           commands,
			DisclosedContracts: disclosed,
		},
		TransactionFormat: txFormat,
	}
	subCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.SubmitAndWaitForTransaction(subCtx, req)
}

// accountConfigTemplateID is the package-name-qualified id of the test
// token's AccountConfig template.
const accountConfigTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2.AccountConfig:AccountConfig"

// createAccountConfig creates an AccountConfig contract for the
// receiver account so the mint/transfer accept can authorize. The
// template's signatory is account.owner AND account.provider, so the
// submit acts as both (operator controls both on LocalNet). Config
// flags: owner can initiate AND approves (mustApprove), provider does
// neither — so the owner alone settles, satisfying
// isValidAccountConfig ((owner.canInitiate||…) && (owner.mustApprove||…)).
func createAccountConfig(ctx context.Context, client *ledger.Client, admin, owner, provider string) (string, error) {
	pkgID, err := resolvePackageID(ctx, client, "splice-test-token-v2")
	if err != nil {
		return "", err
	}
	_, mod, entity := splitInterfaceID(accountConfigTemplateID)
	ownerParty := owner
	providerParty := provider
	account := registry.Account{Owner: &ownerParty, Provider: &providerParty, ID: ""}
	create := &lapiv2.Command{
		Command: &lapiv2.Command_Create{
			Create: &lapiv2.CreateCommand{
				TemplateId: &lapiv2.Identifier{PackageId: pkgID, ModuleName: mod, EntityName: entity},
				CreateArguments: &lapiv2.Record{Fields: []*lapiv2.RecordField{
					{Label: "admin", Value: partyValue(admin)},
					{Label: "account", Value: buildAccountRecord(account)},
					{Label: "ownerConfig", Value: buildPartyConfigRecord(true, true)},
					{Label: "providerConfig", Value: buildPartyConfigRecord(false, false)},
				}},
			},
		},
	}
	resp, err := submitForTransactionMulti(ctx, client, []string{owner, provider},
		[]*lapiv2.Command{create}, nil, createdContractFormat(owner, ""))
	if err != nil {
		return "", fmt.Errorf("create AccountConfig: %w", err)
	}
	cid := firstCreatedOfTemplate(resp, "AccountConfig")
	if cid == "" {
		return "", fmt.Errorf("AccountConfig created but contract id not found")
	}
	return cid, nil
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
