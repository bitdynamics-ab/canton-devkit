package token

// V2 Token Standard interface IDs + asset-specific choice names used
// when the live ledger wiring lands on top of BIT-139.
//
// These constants are the single source of truth for *every* exercise
// the token CLI / Web UI submits. They mirror the upstream Splice
// CLI's `cli/src/constants.ts`:
// https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/cli/src/constants.ts
//
// V1 vs V2 — what each verb actually uses
// --------------------------------------
//
//	balance        → ACS query filtered by HoldingInterfaceV2.
//	                 (V2 holdings implement BOTH V1 and V2 Holding
//	                 interfaces; querying V2 is the V2-native path
//	                 and gives us HoldingViewV2 directly — no downcast.)
//
//	create         → asset-specific: TokenRules.create on the issuer
//	                 party for splice-test-token-v2. No standard
//	                 "instrument create" interface exists in V2.
//
//	mint           → asset-specific: TokenRules_OfferMint choice on the
//	                 issuer's TokenRules contract. The test token does
//	                 NOT implement BurnMintFactoryV1 — and V2 has no
//	                 burn-mint package at all — so mint here is purely
//	                 asset-specific. Real tokens (e.g. Amulet) may
//	                 choose to expose mint through their own choice or
//	                 by implementing BurnMintFactoryV1.
//
//	transfer       → TransferFactoryInterface (V2). Off-ledger flow:
//	                 registry POST /transfer-factory → exercise the
//	                 returned factory choice on the ledger with the
//	                 disclosed contracts the registry handed back.
//	                 (See internal/canton/registry.GetTransferFactory.)
//
//	transfer accept → AcceptTransferInstruction on TransferInstructionInterface
//	                 (V2). Off-ledger flow: registry POST
//	                 /choice-contexts/accept → exercise on the ledger.
//	                 (See internal/canton/registry.GetAcceptChoiceContext.)
//
//	burn           → no generic V2 burn interface; splice-test-token-v2
//	                 does not implement BurnMintFactoryV1 either, so a
//	                 burn against the test token has no path. Real
//	                 tokens that want burn typically implement
//	                 BurnMintFactoryV1.BurnMint or expose an asset-
//	                 specific archive choice on the holding. RunBurn
//	                 surfaces ErrNeedsAssetSpecificBurn when invoked
//	                 against an instrument with no burn path.
//
// Interface IDs use the package-name form (`#<package-name>:<module>:
// <entity>`) accepted by the Ledger API's EventFormat — same as the
// upstream CLI. They are template-package-relative so they keep
// resolving across V2 alpha snapshot rotations without us editing
// the constants every week.

const (
	// HoldingInterfaceV2 is what `balance` ACS-queries against. V2
	// holdings of any V2 asset (Amulet, splice-test-token-v2, custom
	// implementations) all show up under this filter.
	HoldingInterfaceV2 = "#splice-api-token-holding-v2:Splice.Api.Token.HoldingV2:Holding"

	// HoldingInterfaceV1 is intentionally exported too: a V2 LocalNet
	// may also carry V1-only legacy holdings, and the upstream
	// transfer CLI uses V1 to gather input holdings (it works because
	// V2 holdings implement V1 for backward query compat). DevKit
	// stays V2-first; this constant exists so a future "show me both
	// generations" UI toggle is a one-liner.
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
)

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
	//   receiver       : V2.Account            — { owner?, provider?, id }
	//   amount         : Decimal
	//   instrumentId   : V2.InstrumentId       — { admin, id }
	//   offeredAt      : Time
	//   receiverConfig : AccountConfig         — account-level auth rules
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
	// This is the "archive path" burn (BIT-216): archive a holder's
	// holdings to remove them from circulation.
	TestTokenV2HoldingTemplateID = "#splice-test-token-v2:Splice.Testing.Tokens.TestTokenV2.Holding:Token"
)
