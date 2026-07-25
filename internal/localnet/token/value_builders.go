package token

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 choice-argument Value builders. The gRPC ledger API takes `lapiv2.Value`
// (a sum-type of Record / Party / Text / Numeric / Timestamp / ContractId /
// List / GenMap / Optional) for every choice argument; the V2 Token Standard's
// TransferFactory_Transfer and AcceptTransferInstruction choices take deeply-
// nested records of these, hand-built here from the typed Go structs.
//
// Hand-built, not codegen: the shapes are small and frozen by the OpenAPI spec;
// a Daml LF codec would pull in far more dependency surface for ~3 choices.
//
// Reference shapes: Splice.Api.Token.TransferInstructionV2 (TransferFactory_
// Transfer, AcceptTransferInstruction) and Splice.Api.Token.MetadataV1
// (InstrumentId, Metadata, ExtraArgs, ChoiceContext, AnyValue).

// buildTransferRecord constructs the Daml `Transfer` record the
// TransferFactory_Transfer choice argument expects under `transfer`. The two
// generations differ in exactly one field:
//
//	V2 (CIP-0112): sender/receiver : Account
//	V1 (CIP-0056): sender/receiver : Party
//
// The rest are identical: amount, instrumentId, requestedAt, executeBefore,
// inputHoldingCids, meta. The participant matches fields by label, so order is
// not load-bearing; we emit upstream-canonical order to stay greppable.
func buildTransferRecord(t registry.TransferArgs, gen Generation) *lapiv2.Value {
	var sender, receiver *lapiv2.Value
	if gen == genV1 {
		sender = partyValue(accountParty(t.Sender))
		receiver = partyValue(accountParty(t.Receiver))
	} else {
		sender = buildAccountRecord(t.Sender)
		receiver = buildAccountRecord(t.Receiver)
	}
	return recordValue([]field{
		{"sender", sender},
		{"receiver", receiver},
		{"amount", numericValue(t.Amount)},
		{"instrumentId", buildInstrumentIDRecord(t.InstrumentID)},
		{"requestedAt", timestampValue(t.RequestedAt)},
		{"executeBefore", timestampValue(t.ExecuteBefore)},
		{"inputHoldingCids", listValue(t.InputHoldingCids, contractIDValue)},
		{"meta", buildMetadataRecord(t.Meta)},
	})
}

// accountParty flattens an Account to its owner party (or "" when None) — the
// bare Party the V1 transfer shape uses.
func accountParty(a registry.Account) string {
	if a.Owner != nil {
		return *a.Owner
	}
	return ""
}

// buildAccountRecord:
//
//	data Account = Account with
//	  owner    : Optional Party
//	  provider : Optional Party
//	  id       : Text
//
// A nil *string pointer encodes as None.
func buildAccountRecord(a registry.Account) *lapiv2.Value {
	return recordValue([]field{
		{"owner", optionalPartyValue(a.Owner)},
		{"provider", optionalPartyValue(a.Provider)},
		{"id", textValue(a.ID)},
	})
}

// optionalPartyValue builds a Daml `Optional Party`: Some when the
// pointer is non-nil, None (empty Optional) otherwise.
func optionalPartyValue(p *string) *lapiv2.Value {
	if p == nil {
		return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}}
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{Value: partyValue(*p)}}}
}

// buildInstrumentIDRecord:
//
//	data InstrumentId = InstrumentId with
//	  admin : Party
//	  id    : Text
func buildInstrumentIDRecord(i registry.InstrumentID) *lapiv2.Value {
	return recordValue([]field{
		{"admin", partyValue(i.Admin)},
		{"id", textValue(i.ID)},
	})
}

// buildMetadataRecord:
//
//	data Metadata = Metadata with
//	  values : Map Text Text
//
// `Map Text Text` wire-encodes as a TextMap.
func buildMetadataRecord(m registry.Metadata) *lapiv2.Value {
	return recordValue([]field{
		{"values", textMapValue(m.Values)},
	})
}

// buildExtraArgsRecord:
//
//	data ExtraArgs = ExtraArgs with
//	  context : ChoiceContext
//	  meta    : Metadata
func buildExtraArgsRecord(ctxData map[string]any, meta registry.Metadata) (*lapiv2.Value, error) {
	cctx, err := buildChoiceContextRecord(ctxData)
	if err != nil {
		return nil, fmt.Errorf("buildExtraArgsRecord.context: %w", err)
	}
	return recordValue([]field{
		{"context", cctx},
		{"meta", buildMetadataRecord(meta)},
	}), nil
}

// choiceContextValues extracts the inner `values` map from a registry
// choiceContextData blob (`{"values": {key: AnyValue}}`). A blob that is
// already a bare map (no `values` wrapper) is returned as-is.
func choiceContextValues(ctxData map[string]any) map[string]any {
	if inner, ok := ctxData["values"].(map[string]any); ok {
		return inner
	}
	return ctxData
}

// buildChoiceContextRecord:
//
//	data ChoiceContext = ChoiceContext with
//	  values : Map Text AnyValue
//
// The AnyValue sum type lives in MetadataV1.daml:
//
//	data AnyValue
//	  = AV_Text Text
//	  | AV_Int Int
//	  | AV_Decimal Decimal
//	  | AV_Bool Bool
//	  | AV_Date Date
//	  | AV_Time Time
//	  | AV_Party Party
//	  | AV_ContractId AnyContractId
//	  | AV_Reference Reference
//	  | AV_List [AnyValue]
//	  | AV_Map (Map Text AnyValue)
//
// Each variant wire-encodes as a variant value discriminated by constructor.
// Best-effort: single-key JSON objects (`{"AV_Text": "hello"}`) round-trip
// directly; bare scalars get sniffed into the closest matching variant.
func buildChoiceContextRecord(data map[string]any) (*lapiv2.Value, error) {
	// Unwrap the inner `values` map first; double-wrapping would make the
	// choice's context lookups (e.g. "amulet-rules") miss.
	values, err := anyValueTextMap(choiceContextValues(data))
	if err != nil {
		return nil, err
	}
	return recordValue([]field{{"values", values}}), nil
}

// anyValueTextMap builds a Daml `TextMap AnyValue` from a JSON map — the type
// ChoiceContext.values and AV_Map use, NOT GenMap (the preprocessor rejects a
// GenMap where a TextMap is declared). Entries sorted by key for determinism.
func anyValueTextMap(data map[string]any) (*lapiv2.Value, error) {
	entries := make([]*lapiv2.TextMap_Entry, 0, len(data))
	for _, k := range sortedKeys(data) {
		anyVal, err := jsonToAnyValue(data[k])
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		entries = append(entries, &lapiv2.TextMap_Entry{Key: k, Value: anyVal})
	}
	return &lapiv2.Value{
		Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{Entries: entries}},
	}, nil
}

// jsonToAnyValue converts a JSON-decoded `any` into the AnyValue variant value.
// The registry uses single-key-object shape (`{"AV_Text": "..."}`); bare scalars
// fall back to type-sniffing. VariantId is left nil — the participant resolves
// it from the declaring package at exercise time.
func jsonToAnyValue(v any) (*lapiv2.Value, error) {
	switch x := v.(type) {
	case map[string]any:
		// Tagged form (what the Splice registry emits): {"tag": "AV_...", "value": ...}.
		if tag, ok := x["tag"].(string); ok && strings.HasPrefix(tag, "AV_") {
			innerVal, err := scalarToAnyValueInner(tag, x["value"])
			if err != nil {
				return nil, err
			}
			return variantValue(tag, innerVal), nil
		}
		// Wrapped form: {"AV_Text": "..."}.
		if len(x) == 1 {
			for ctor, inner := range x {
				if strings.HasPrefix(ctor, "AV_") {
					innerVal, err := scalarToAnyValueInner(ctor, inner)
					if err != nil {
						return nil, err
					}
					return variantValue(ctor, innerVal), nil
				}
			}
		}
		// Bare map → AV_Map (TextMap AnyValue).
		inner, err := anyValueTextMap(x)
		if err != nil {
			return nil, err
		}
		return variantValue("AV_Map", inner), nil
	case []any:
		items := make([]*lapiv2.Value, 0, len(x))
		for _, it := range x {
			innerVal, err := jsonToAnyValue(it)
			if err != nil {
				return nil, err
			}
			items = append(items, innerVal)
		}
		return variantValue("AV_List", &lapiv2.Value{
			Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: items}},
		}), nil
	case string:
		return variantValue("AV_Text", textValue(x)), nil
	case float64:
		// Integral and int64-fitting → AV_Int; otherwise AV_Decimal.
		if x == float64(int64(x)) {
			return variantValue("AV_Int", &lapiv2.Value{
				Sum: &lapiv2.Value_Int64{Int64: int64(x)},
			}), nil
		}
		// Round-trip through big.Rat for an exact decimal string; %g would lose
		// precision past ~15 digits and may emit scientific notation the registry
		// rejects. FloatString(18) covers V2's test-token cap.
		return variantValue("AV_Decimal",
			numericValue(new(big.Rat).SetFloat64(x).FloatString(18))), nil
	case bool:
		return variantValue("AV_Bool", &lapiv2.Value{Sum: &lapiv2.Value_Bool{Bool: x}}), nil
	case nil:
		// AnyValue has no None variant; encode as empty AV_Text to keep the
		// encoder total.
		return variantValue("AV_Text", textValue("")), nil
	default:
		return nil, fmt.Errorf("unsupported choice-context value type %T", v)
	}
}

// scalarToAnyValueInner converts the inner payload of an explicit `{"AV_X": ...}`
// variant, trusting the tag to pick the inner Value type.
func scalarToAnyValueInner(ctor string, inner any) (*lapiv2.Value, error) {
	switch ctor {
	case "AV_Text", "AV_Party", "AV_ContractId", "AV_Reference":
		s, ok := inner.(string)
		if !ok {
			return nil, fmt.Errorf("%s expected string inner, got %T", ctor, inner)
		}
		switch ctor {
		case "AV_Party":
			return partyValue(s), nil
		case "AV_ContractId":
			return contractIDValue(s), nil
		default:
			return textValue(s), nil
		}
	case "AV_Int":
		switch n := inner.(type) {
		case float64:
			return &lapiv2.Value{Sum: &lapiv2.Value_Int64{Int64: int64(n)}}, nil
		case string:
			// Some registries hand ints back as strings for precision.
			var iv int64
			if _, err := fmt.Sscan(n, &iv); err != nil {
				return nil, fmt.Errorf("AV_Int parse %q: %w", n, err)
			}
			return &lapiv2.Value{Sum: &lapiv2.Value_Int64{Int64: iv}}, nil
		default:
			return nil, fmt.Errorf("AV_Int expected number, got %T", inner)
		}
	case "AV_Decimal":
		s, ok := inner.(string)
		if !ok {
			s = fmt.Sprintf("%v", inner)
		}
		return numericValue(s), nil
	case "AV_Bool":
		b, ok := inner.(bool)
		if !ok {
			return nil, fmt.Errorf("AV_Bool expected bool, got %T", inner)
		}
		return &lapiv2.Value{Sum: &lapiv2.Value_Bool{Bool: b}}, nil
	case "AV_Date":
		// Daml Date wire-encodes as Value_Date (days since epoch); OpenAPI
		// form is "YYYY-MM-DD".
		s, ok := inner.(string)
		if !ok {
			return nil, fmt.Errorf("AV_Date expected string, got %T", inner)
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, fmt.Errorf("AV_Date parse %q: %w", s, err)
		}
		days := int32(t.Sub(time.Unix(0, 0)) / (24 * time.Hour))
		return &lapiv2.Value{Sum: &lapiv2.Value_Date{Date: days}}, nil
	case "AV_Time":
		s, ok := inner.(string)
		if !ok {
			return nil, fmt.Errorf("AV_Time expected string, got %T", inner)
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fmt.Errorf("AV_Time parse %q: %w", s, err)
		}
		return timestampValue(t), nil
	default:
		// AV_List, AV_Map should recurse via jsonToAnyValue.
		return jsonToAnyValue(inner)
	}
}

// --- test-token (splice-test-token-v2) choice context ----------------

// buildTestTokenExtraArgs builds the `extraArgs` record ({context, meta}) the
// bundled splice-test-token-v2 transfer/accept state machine expects. Unlike
// Amulet's opaque off-ledger blob, the test token is issuer-administered
// on-ledger: the context is two well-known entries the choice looks up directly
// — the issuer's TokenRules (the event log) and the accounts' AccountConfig
// contracts.
func buildTestTokenExtraArgs(tokenRulesCID string, accountConfigCIDs []string) *lapiv2.Value {
	return recordValue([]field{
		{"context", buildTestTokenChoiceContext(tokenRulesCID, accountConfigCIDs)},
		{"meta", buildMetadataRecord(registry.Metadata{Values: map[string]string{}})},
	})
}

// buildTestTokenChoiceContext builds the ChoiceContext record
// ({values : Map Text AnyValue}) carrying:
//
//	testTokenV2/tokenRules    → AV_ContractId of the issuer's TokenRules
//	testTokenV2/accountConfigs → AV_List of AV_ContractId for each
//	                             provider-scoped account's AccountConfig
//
// Entries are key-sorted for a deterministic wire form. An empty
// accountConfigCIDs slice yields an empty AV_List, which the choice accepts for
// self-custodial accounts (it falls back to the basic config).
func buildTestTokenChoiceContext(tokenRulesCID string, accountConfigCIDs []string) *lapiv2.Value {
	configElems := make([]*lapiv2.Value, len(accountConfigCIDs))
	for i, cid := range accountConfigCIDs {
		configElems[i] = variantValue("AV_ContractId", contractIDValue(cid))
	}
	values := &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{
		Entries: []*lapiv2.TextMap_Entry{
			{
				Key: accountConfigsContextKey,
				Value: variantValue("AV_List", &lapiv2.Value{Sum: &lapiv2.Value_List{
					List: &lapiv2.List{Elements: configElems},
				}}),
			},
			{
				Key:   tokenRulesContextKey,
				Value: variantValue("AV_ContractId", contractIDValue(tokenRulesCID)),
			},
		},
	}}}
	return recordValue([]field{{"values", values}})
}

// --- low-level Value constructors -----------------------------------

// field is the (label, value) tuple a Record carries.
type field struct {
	label string
	value *lapiv2.Value
}

func recordValue(fields []field) *lapiv2.Value {
	rf := make([]*lapiv2.RecordField, len(fields))
	for i, f := range fields {
		rf[i] = &lapiv2.RecordField{Label: f.label, Value: f.value}
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_Record{Record: &lapiv2.Record{Fields: rf}}}
}

func partyValue(p string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: p}}
}

func textValue(s string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Text{Text: s}}
}

func numericValue(s string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Numeric{Numeric: s}}
}

func contractIDValue(c string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_ContractId{ContractId: c}}
}

// timestampValue encodes a Daml `Time` as microseconds since Unix epoch.
func timestampValue(t time.Time) *lapiv2.Value {
	micros := t.UnixMicro()
	return &lapiv2.Value{Sum: &lapiv2.Value_Timestamp{Timestamp: micros}}
}

// listValue builds a Daml `List a` from a slice and a per-element constructor.
// Empty input → empty (non-nil) list, which the participant accepts as `Nil`.
func listValue[T any](items []T, fn func(T) *lapiv2.Value) *lapiv2.Value {
	elems := make([]*lapiv2.Value, len(items))
	for i, it := range items {
		elems[i] = fn(it)
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: elems}}}
}

// textMapValue builds a Daml `TextMap a`. Entries sorted by key: TextMap is
// semantically unordered, but Canton hashes the proto wire form, so determinism
// matters for stable command-id rederivation.
func textMapValue(m map[string]string) *lapiv2.Value {
	entries := make([]*lapiv2.TextMap_Entry, 0, len(m))
	for _, k := range sortedKeys(m) {
		entries = append(entries, &lapiv2.TextMap_Entry{Key: k, Value: textValue(m[k])})
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{Entries: entries}}}
}

// genMapEntry is one (key, value) pair of a Daml `DA.Map` (GenMap): both sides
// are arbitrary Values, unlike TextMap whose key is always Text.
type genMapEntry struct {
	key   *lapiv2.Value
	value *lapiv2.Value
}

// genMapValue builds a Daml `DA.Map k v` — wire-encoded as a Value_GenMap, NOT
// a TextMap. Keys are arbitrary `Ord` types (here a ScopedAccount record), so
// the participant rejects a TextMap or bare List where a GenMap is declared.
func genMapValue(entries []genMapEntry) *lapiv2.Value {
	out := make([]*lapiv2.GenMap_Entry, len(entries))
	for i, e := range entries {
		out[i] = &lapiv2.GenMap_Entry{Key: e.key, Value: e.value}
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_GenMap{GenMap: &lapiv2.GenMap{Entries: out}}}
}

// variantValue wraps an inner Value in the participant's variant shape.
// VariantId is left nil — the participant resolves the type from the declaring
// choice at exercise time; pinning a package id would couple us to a snapshot.
func variantValue(constructor string, inner *lapiv2.Value) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Variant{Variant: &lapiv2.Variant{
		Constructor: constructor,
		Value:       inner,
	}}}
}

// sortedKeys returns the map's keys in lexicographic order, for deterministic
// wire form.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- DisclosedContract converter ------------------------------------

// disclosedContractsToProto translates the OpenAPI registry shape (base64
// createdEventBlob) into the participant's gRPC Commands.disclosed_contracts
// (decoded bytes + synchronizer id). The participant uses these to resolve
// off-ledger-known contracts (the registry's factory plus dependencies like
// the Amulet rules) at exercise time.
func disclosedContractsToProto(in []registry.DisclosedContract) ([]*lapiv2.DisclosedContract, error) {
	out := make([]*lapiv2.DisclosedContract, len(in))
	for i, d := range in {
		blob, err := base64.StdEncoding.DecodeString(d.CreatedEventBlob)
		if err != nil {
			return nil, fmt.Errorf("disclosed[%d] %s: decode createdEventBlob: %w", i, d.ContractID, err)
		}
		out[i] = &lapiv2.DisclosedContract{
			ContractId:       d.ContractID,
			CreatedEventBlob: blob,
			SynchronizerId:   d.SynchronizerID,
		}
	}
	return out, nil
}
