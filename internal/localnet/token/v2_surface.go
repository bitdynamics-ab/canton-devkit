package token

// Token Standard interface IDs + asset-specific choice names — the
// single source of truth for every exercise the token CLI / Web UI
// submits, mirroring the upstream Splice CLI's constants.ts.
//
// What each verb uses:
//
//	balance → ACS query filtered by the vetted Holding interfaces.
//	create  → asset-specific TokenRules.create (no standard "instrument
//	          create" interface exists in V2).
//	mint    → asset-specific TokenRules_OfferMint. The test token
//	          doesn't implement BurnMintFactoryV1 and V2 has no burn-mint
//	          package, so mint is purely asset-specific.
//	transfer → TransferFactory interface. Off-ledger: registry POST
//	          /transfer-factory → exercise the returned factory choice.
//	transfer accept → TransferInstruction_Accept. Off-ledger: registry
//	          POST /choice-contexts/accept → exercise.
//	burn    → no generic burn interface; RunBurn archives test-token
//	          holdings directly, else ErrUnsupportedOnInstrument.
//
// Interface IDs use the package-name form (`#<package-name>:<module>:
// <entity>`) accepted by the Ledger API's EventFormat, so they keep
// resolving across V2 alpha snapshot rotations.

const (
	// HoldingInterfaceV2 is what `balance` ACS-queries against; V2
	// holdings of any V2 asset show up under this filter.
	HoldingInterfaceV2 = "#splice-api-token-holding-v2:Splice.Api.Token.HoldingV2:Holding"

	// HoldingInterfaceV1 covers V1 (CIP-0056) holdings — what real tokens
	// like Amulet implement on stable Splice releases. Queried alongside
	// V2 whenever the V1 package is vetted.
	HoldingInterfaceV1 = "#splice-api-token-holding-v1:Splice.Api.Token.HoldingV1:Holding"

	// TransferFactoryInterfaceV2 is the on-ledger interface a sender
	// exercises to initiate a V2 transfer. Factory + disclosed contracts
	// come from the registry's POST /transfer-factory.
	TransferFactoryInterfaceV2 = "#splice-api-token-transfer-instruction-v2:Splice.Api.Token.TransferInstructionV2:TransferFactory"

	// TransferInstructionInterfaceV2 is the interface a pending V2
	// TransferInstruction implements. The receiver exercises Accept on it;
	// choice context comes from the registry's POST /choice-contexts/accept.
	TransferInstructionInterfaceV2 = "#splice-api-token-transfer-instruction-v2:Splice.Api.Token.TransferInstructionV2:TransferInstruction"

	// V1 (CIP-0056) transfer interfaces. Same choice names as V2
	// (TransferFactory_Transfer / TransferInstruction_Accept); only the
	// package + module differ.
	TransferFactoryInterfaceV1 = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferFactory"

	TransferInstructionInterfaceV1 = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferInstruction"

	// V2 foundation surfaces. Signatures verified against Splice 0.6.12.

	// EventLogInterfaceV2 is the non-consuming EventLog interface the
	// instrument admin exercises (EventLog_HoldingsChange) to report
	// holdings changes for the EventLog history feature.
	EventLogInterfaceV2 = "#splice-api-token-transfer-events-v2:Splice.Api.Token.TransferEventsV2:EventLog"

	// AllocationInterfaceV2 is the Allocation interface (Allocation_Settle
	// / _Cancel / _Withdraw) representing an authorizer's ready-to-settle
	// DvP approval.
	AllocationInterfaceV2 = "#splice-api-token-allocation-v2:Splice.Api.Token.AllocationV2:Allocation"

	// SettlementFactoryInterfaceV2 is the executor-facing factory whose
	// SettlementFactory_SettleBatch nets and settles a batch of
	// allocations. Factory + disclosed contracts come from the registry's
	// settlement-factory endpoint.
	SettlementFactoryInterfaceV2 = "#splice-api-token-allocation-v2:Splice.Api.Token.AllocationV2:SettlementFactory"

	// AllocationFactoryInterfaceV2 is the wallet-facing factory whose
	// AllocationFactory_Allocate requests an allocation. Factory +
	// disclosed contracts come from the registry's allocation-factory
	// endpoint.
	AllocationFactoryInterfaceV2 = "#splice-api-token-allocation-instruction-v2:Splice.Api.Token.AllocationInstructionV2:AllocationFactory"

	// AllocationInstructionInterfaceV2 is the pending-allocation-creation
	// interface (AllocationInstruction_Accept / _Withdraw). Choice context
	// comes from the registry's accept/withdraw endpoints.
	AllocationInstructionInterfaceV2 = "#splice-api-token-allocation-instruction-v2:Splice.Api.Token.AllocationInstructionV2:AllocationInstruction"

	// AllocationRequestInterfaceV2 is the request-for-allocation interface
	// (AllocationRequest_Accept / _Reject / _Withdraw) an app raises for a
	// wallet to fulfil.
	AllocationRequestInterfaceV2 = "#splice-api-token-allocation-request-v2:Splice.Api.Token.AllocationRequestV2:AllocationRequest"

	// BatchingUtilityTemplateV2 is the wallet's BatchingUtility template.
	// Its BatchingUtility_ExecuteBatch choice threads holdings through a
	// list of TokenStandardAction in one transaction. Pinned to
	// splice-util-token-standard-wallet 1.1.0.
	BatchingUtilityTemplateV2 = "#splice-util-token-standard-wallet:Splice.Util.Token.Wallet.BatchingUtilityV2:BatchingUtility"

	// BatchingUtilityExecuteBatchChoiceV2 is the batch-execute choice
	// exercised on BatchingUtilityTemplateV2 (args: inputHoldingMap,
	// actions, archiveAfterExecution).
	BatchingUtilityExecuteBatchChoiceV2 = "BatchingUtility_ExecuteBatch"
)

// transferFactoryInterface / transferInstructionInterface pick the
// interface id for an instrument's generation.
func transferFactoryInterface(gen Generation) string {
	if gen == genV1 {
		return TransferFactoryInterfaceV1
	}
	return TransferFactoryInterfaceV2
}

func transferInstructionInterface(gen Generation) string {
	if gen == genV1 {
		return TransferInstructionInterfaceV1
	}
	return TransferInstructionInterfaceV2
}

// transferInstructionModule is the Daml module a created
// TransferInstruction carries for a generation — used to mine the
// instruction id out of a submit response (the module name is stable; the
// package id rotates).
func transferInstructionModule(gen Generation) string {
	if gen == genV1 {
		return "Splice.Api.Token.TransferInstructionV1"
	}
	return "Splice.Api.Token.TransferInstructionV2"
}

// registryVersionSeg maps a generation to the registry URL path segment.
func registryVersionSeg(gen Generation) string {
	if gen == genV1 {
		return "v1"
	}
	return "v2"
}

// V2-foundation registry endpoint paths. The upstream OpenAPI mixes
// singular (`/allocation/`, `/allocation-instruction/`) and plural
// (`/allocations/`) segments, so these are copied verbatim, not
// normalized. `{id}` is filled by the caller.
const (
	// AllocationFactoryPathV2 → AllocationFactory_Allocate context.
	AllocationFactoryPathV2 = "/registry/allocation-instruction/v2/allocation-factory"
	// AllocationInstructionAcceptPathV2 → accept context.
	AllocationInstructionAcceptPathV2 = "/registry/allocation-instruction/v2/{allocationInstructionId}/choice-contexts/accept"
	// AllocationInstructionWithdrawPathV2 → withdraw context.
	AllocationInstructionWithdrawPathV2 = "/registry/allocation-instruction/v2/{allocationInstructionId}/choice-contexts/withdraw"
	// SettlementFactoryPathV2 → SettlementFactory_SettleBatch context.
	SettlementFactoryPathV2 = "/registry/allocation/v2/settlement-factory"
	// AllocationWithdrawPathV2 → Allocation_Withdraw context.
	AllocationWithdrawPathV2 = "/registry/allocations/v2/{allocationId}/choice-contexts/withdraw"
	// AllocationCancelPathV2 → Allocation_Cancel context.
	AllocationCancelPathV2 = "/registry/allocations/v2/{allocationId}/choice-contexts/cancel"
)

// Asset-specific choice / template names for the bundled
// splice-test-token-v2 example token. Keep in lock-step with the upstream
// TestTokenV2.daml when the catalogue entry is refreshed.
const (
	// TestTokenV2RulesTemplate is the issuer-owned TokenRules contract
	// `create` instantiates; the mint / transfer-factory /
	// allocation-factory interfaces all attach to it.
	TestTokenV2RulesTemplate = "Splice.Testing.Tokens.TestTokenV2:TokenRules"

	// TestTokenV2OfferMintChoice is the asset-specific mint choice.
	// Controller = admin = TokenRules signatory, so it's a single-signer
	// submit-and-wait.
	//
	// Arguments (verified against TestTokenV2.daml):
	// receiver : V2.Account — { owner?, provider?, id }
	// amount : Decimal
	// instrumentId : V2.InstrumentId — { admin, id }
	// offeredAt : Time
	// receiverConfig : AccountConfig — account-level auth rules
	TestTokenV2OfferMintChoice = "TokenRules_OfferMint"

	// TestTokenV2RulesTemplateID is the package-name-qualified template id
	// for create/exercise against the bundled TokenRules. The
	// `#package-name` form survives the alpha's weekly snapshot rotation.
	TestTokenV2RulesTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2:TokenRules"

	// TestTokenV2HoldingTemplateID is the Token (holding) template — the
	// UTXO unit. Its signatory is the account parties + the instrument
	// admin (all operator-controlled on LocalNet), so the archive-path
	// burn exercises the built-in Archive choice as their union.
	TestTokenV2HoldingTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2.Holding:Token"
)
