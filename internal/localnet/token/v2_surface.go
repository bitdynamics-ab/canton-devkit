package token

// Token Standard interface IDs + asset-specific choice names — the
// single source of truth for every exercise the token CLI / Web UI
// submits. They mirror the upstream Splice CLI's
// `cli/src/constants.ts`:
// https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/cli/src/constants.ts
//
// What each verb uses:
//
//	balance → ACS query filtered by the vetted Holding interfaces
//	          (V1 and/or V2; see discoverTokenSurfaces).
//
//	create  → asset-specific: TokenRules.create on the issuer party
//	          for splice-test-token-v2. No standard "instrument
//	          create" interface exists in V2.
//
//	mint    → asset-specific: TokenRules_OfferMint on the issuer's
//	          TokenRules contract. The test token does NOT implement
//	          BurnMintFactoryV1 — and V2 has no burn-mint package at
//	          all — so mint is purely asset-specific. Real tokens may
//	          expose mint through their own choice or by implementing
//	          BurnMintFactoryV1.
//
//	transfer → TransferFactory interface (V1 or V2). Off-ledger flow:
//	          registry POST /transfer-factory → exercise the returned
//	          factory choice on the ledger with the disclosed
//	          contracts the registry handed back.
//
//	transfer accept → TransferInstruction_Accept on the
//	          TransferInstruction interface. Off-ledger flow: registry
//	          POST /choice-contexts/accept → exercise on the ledger.
//
//	burn    → no generic burn interface; splice-test-token-v2 does
//	          not implement BurnMintFactoryV1 either. RunBurn archives
//	          test-token holdings directly, and surfaces
//	          ErrUnsupportedOnInstrument for instruments with no burn
//	          path.
//
// Interface IDs use the package-name form (`#<package-name>:<module>:
// <entity>`) accepted by the Ledger API's EventFormat — same as the
// upstream CLI. They are package-name-relative so they keep resolving
// across V2 alpha snapshot rotations without editing the constants.

const (
	// HoldingInterfaceV2 is what `balance` ACS-queries against. V2
	// holdings of any V2 asset (Amulet, splice-test-token-v2, custom
	// implementations) all show up under this filter.
	HoldingInterfaceV2 = "#splice-api-token-holding-v2:Splice.Api.Token.HoldingV2:Holding"

	// HoldingInterfaceV1 covers V1 (CIP-0056) holdings — the
	// generation real tokens like Amulet implement on stable Splice
	// releases. The read paths query it alongside V2 whenever the V1
	// package is vetted.
	HoldingInterfaceV1 = "#splice-api-token-holding-v1:Splice.Api.Token.HoldingV1:Holding"

	// TransferFactoryInterfaceV2 is the on-ledger interface a sender
	// exercises to initiate a V2 transfer. The factory contract + the
	// disclosed contracts needed to exercise it come from the
	// registry's POST /transfer-factory; see internal/canton/registry.
	TransferFactoryInterfaceV2 = "#splice-api-token-transfer-instruction-v2:Splice.Api.Token.TransferInstructionV2:TransferFactory"

	// TransferInstructionInterfaceV2 is the on-ledger interface a
	// pending V2 TransferInstruction implements. AcceptTransferInstruction
	// is the choice the receiver exercises on it; choice context comes
	// from the registry's POST /choice-contexts/accept.
	TransferInstructionInterfaceV2 = "#splice-api-token-transfer-instruction-v2:Splice.Api.Token.TransferInstructionV2:TransferInstruction"

	// V1 (CIP-0056) transfer interfaces — the generation real tokens like
	// Amulet implement on stable Splice releases. Same choice names as V2
	// (TransferFactory_Transfer / TransferInstruction_Accept); only the
	// package + module differ.
	TransferFactoryInterfaceV1 = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferFactory"

	TransferInstructionInterfaceV1 = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferInstruction"
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

// transferInstructionModule is the resolved Daml module a created
// TransferInstruction carries for a generation — used to mine the
// instruction id out of a submit response (the package id rotates; the
// module name is stable).
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

// Asset-specific choice / template names for the bundled
// splice-test-token-v2 example token. These move with the upstream
// snapshot rotation — keep them in lock-step with
// `token-standard/examples/splice-test-token-v2/daml/Splice/Testing/
// Tokens/TestTokenV2.daml` whenever the catalogue entry is refreshed.
const (
	// TestTokenV2RulesTemplate is the issuer-owned TokenRules contract
	// `create` instantiates. Mint / transfer-factory / allocation-factory
	// interfaces all attach to this template's interface instances.
	TestTokenV2RulesTemplate = "Splice.Testing.Tokens.TestTokenV2:TokenRules"

	// TestTokenV2OfferMintChoice is the asset-specific mint choice
	// `mint` exercises. Controller = admin; signatory of TokenRules =
	// admin; so it's a single-signer submit-and-wait.
	//
	// Arguments (verified against TestTokenV2.daml):
	// receiver : V2.Account — { owner?, provider?, id }
	// amount : Decimal
	// instrumentId : V2.InstrumentId — { admin, id }
	// offeredAt : Time
	// receiverConfig : AccountConfig — account-level auth rules
	TestTokenV2OfferMintChoice = "TokenRules_OfferMint"

	// TestTokenV2RulesTemplateID is the package-name-qualified template
	// id for create/exercise against the bundled splice-test-token-v2
	// TokenRules. The `#package-name` form lets Canton resolve to
	// whichever vetted package satisfies the name, surviving the V2
	// alpha's weekly snapshot rotation.
	TestTokenV2RulesTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2:TokenRules"

	// TestTokenV2HoldingTemplateID is the Token (holding) template —
	// the UTXO unit. Its signatory is the account parties + the
	// instrument admin, so the built-in Archive choice is authorizable
	// by those parties together (all operator-controlled on LocalNet).
	// This is the archive-path burn: archive a holder's holdings to
	// remove them from circulation.
	TestTokenV2HoldingTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2.Holding:Token"
)
