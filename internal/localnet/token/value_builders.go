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

// V2 choice-argument Value builders. The gRPC ledger API takes
// `lapiv2.Value` (a sum-type of Record / Party / Text / Numeric /
// Timestamp / ContractId / List / GenMap / Optional) for every choice
// argument. The V2 Token Standard's TransferFactory_Transfer and
// AcceptTransferInstruction choices accept deeply-nested records of
// these types — this file hand-builds them from the typed Go structs
// `registry.TransferArgs`, `registry.ChoiceContextResponse`, etc.
//
// Why hand-built and not codegen: the V2 choice shapes are small, well-
// defined, and frozen by the OpenAPI spec. Generating a Daml LF codec
// from .dar files would pull in significantly more dependency surface
// and the win is marginal for ~3 choices we need to support.
//
// References (upstream Splice tree, branch
// `token-standard-v2-upcoming`):
//
//	token-standard/splice-api-token-transfer-instruction-v2/
//	  daml/Splice/Api/Token/TransferInstructionV2.daml
//	    — TransferFactory_Transfer, AcceptTransferInstruction signatures
//
//	token-standard/splice-api-token-metadata-v1/
//	  daml/Splice/Api/Token/MetadataV1.daml
//	    — InstrumentId, Metadata, ExtraArgs, ChoiceContext, AnyValue

// buildTransferRecord constructs the Daml `Transfer` record the
// TransferFactory_Transfer choice argument expects under its `transfer`
// field. Matches the field set + order from
// TransferInstructionV2.daml:
//
//	data Transfer = Transfer with
//	  sender           : Party
//	  receiver         : Party
//	  amount           : Decimal
//	  instrumentId     : InstrumentId
//	  requestedAt      : Time
//	  executeBefore    : Time
//	  inputHoldingCids : [ContractId Holding]
//	  meta             : Metadata
//
// Field-label order matters for the participant's record decoder when
// `verbose=true` is on the response side; for submissions the
// participant accepts any order and matches by label. We emit in the
// upstream-canonical order to stay greppable against the .daml source.
func buildTransferRecord(t registry.TransferArgs) *lapiv2.Value {
	return recordValue([]field{
		{"sender", buildAccountRecord(t.Sender)},
		{"receiver", buildAccountRecord(t.Receiver)},
		{"amount", numericValue(t.Amount)},
		{"instrumentId", buildInstrumentIDRecord(t.InstrumentID)},
		{"requestedAt", timestampValue(t.RequestedAt)},
		{"executeBefore", timestampValue(t.ExecuteBefore)},
		{"inputHoldingCids", listValue(t.InputHoldingCids, contractIDValue)},
		{"meta", buildMetadataRecord(t.Meta)},
	})
}

// buildAccountRecord:
//
//	data Account = Account with
//	  owner    : Optional Party
//	  provider : Optional Party
//	  id       : Text
//
// owner/provider are Optional Party — encoded as an Optional Value
// (Some → inner party Value, None → empty Optional). A nil *string
// pointer becomes None.
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
// The Daml `Map Text Text` is wire-encoded as a `TextMap` Value entry;
// the participant accepts a sorted-by-key form deterministically.
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
//
// ChoiceContext.values is `Map Text AnyValue`. The registry hands us
// `choiceContextData map[string]any` (JSON-decoded) — convert each
// entry to AnyValue.
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
// choiceContextData blob. The registry returns the choice context as a
// full ChoiceContext-shaped JSON object — `{"values": {key: AnyValue}}`
// — so the actual key→AnyValue entries live under `values`, not at the
// top level. When the blob is already a bare map (no `values` wrapper,
// e.g. an empty context), it's returned as-is.
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
// Each variant is wire-encoded as a Daml LF variant value:
// `Variant { variantId, constructor, value }`. The participant uses
// the constructor name to discriminate.
//
// The conversion is best-effort: registries that hand us
// JSON-encoded AnyValue payloads (a single-key object like
// `{"AV_Text": "hello"}`) round-trip directly; legacy registries that
// hand us bare scalars get sniffed into the closest matching variant.
func buildChoiceContextRecord(data map[string]any) (*lapiv2.Value, error) {
	// The registry hands back the choice context as a full
	// ChoiceContext JSON (`{"values": {...}}`), so unwrap the inner
	// `values` map before building the TextMap — otherwise we'd
	// double-wrap and the Daml choice's context lookups
	// (e.g. "amulet-rules", "open-round") would miss.
	values, err := anyValueTextMap(choiceContextValues(data))
	if err != nil {
		return nil, err
	}
	return recordValue([]field{{"values", values}}), nil
}

// anyValueTextMap builds a Daml `TextMap AnyValue` from a JSON map.
// ChoiceContext.values and the AV_Map variant both use this type —
// NOT GenMap. The participant's preprocessor rejects a GenMap where a
// TextMap is declared ("mismatching type: TextMap ... and value:
// GenMap()"). Entries are sorted by key for deterministic wire form.
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

// jsonToAnyValue converts a JSON-decoded `any` into the AnyValue
// variant value the participant decodes. The registry uses
// single-key-object shape (`{"AV_Text": "..."}`); bare scalars
// fall back to type-sniffing.
//
// The variant value shape on the wire is:
//
//	Value_Variant{ VariantId: nil, Constructor: "AV_Text", Value: <inner> }
//
// VariantId is optional — the participant resolves it from the
// declaring template / interface package at exercise time.
func jsonToAnyValue(v any) (*lapiv2.Value, error) {
	switch x := v.(type) {
	case map[string]any:
		// Tagged form (what the Splice registry actually emits):
		// {"tag": "AV_ContractId", "value": "..."}.
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
		// JSON numbers come in as float64. If it's integral with no
		// fractional part and fits in int64, emit AV_Int; otherwise
		// AV_Decimal (string-encoded, no precision loss).
		if x == float64(int64(x)) {
			return variantValue("AV_Int", &lapiv2.Value{
				Sum: &lapiv2.Value_Int64{Int64: int64(x)},
			}), nil
		}
		// %g loses precision past ~15 significant digits and may emit
		// scientific notation, neither of which the registry accepts.
		// Round-trip the float64 through big.Rat so we emit an exact
		// decimal string. Daml Decimal allows up to 38 fractional
		// digits in V2; FloatString(18) covers V2's test-token cap
		// while keeping the wire form deterministic.
		return variantValue("AV_Decimal",
			numericValue(new(big.Rat).SetFloat64(x).FloatString(18))), nil
	case bool:
		return variantValue("AV_Bool", &lapiv2.Value{Sum: &lapiv2.Value_Bool{Bool: x}}), nil
	case nil:
		// AnyValue has no None variant; encode as empty AV_Text. The
		// registry shouldn't produce nulls in choice context, but the
		// fallback keeps the encoder total.
		return variantValue("AV_Text", textValue("")), nil
	default:
		return nil, fmt.Errorf("unsupported choice-context value type %T", v)
	}
}

// scalarToAnyValueInner converts the inner payload of an explicit
// `{"AV_X": ...}` variant. We trust the variant tag and build the
// proper inner Value type for it.
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
			// Some registries hand back ints as strings for precision.
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
		// Daml Date is wire-encoded as `Value_Date{ Date int32 }`
		// (days since epoch). The OpenAPI form is "YYYY-MM-DD".
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

// --- low-level Value constructors -----------------------------------

// field is the (label, value) tuple a Record carries; carried as a
// tiny struct so multi-field record literals stay readable.
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

// timestampValue encodes a Daml `Time` value. The wire format is
// microseconds since Unix epoch.
func timestampValue(t time.Time) *lapiv2.Value {
	micros := t.UnixMicro()
	return &lapiv2.Value{Sum: &lapiv2.Value_Timestamp{Timestamp: micros}}
}

// listValue builds a Daml `List a` from a slice and a per-element
// constructor. Empty input → empty list (NOT nil), which the
// participant accepts as `Nil`.
func listValue[T any](items []T, fn func(T) *lapiv2.Value) *lapiv2.Value {
	elems := make([]*lapiv2.Value, len(items))
	for i, it := range items {
		elems[i] = fn(it)
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: elems}}}
}

// textMapValue builds a Daml `TextMap a`. Entries are sorted by key
// for deterministic wire encoding; Daml's TextMap is semantically
// unordered but Canton hashes the proto wire form, so determinism
// matters when callers want stable command-id rederivation.
func textMapValue(m map[string]string) *lapiv2.Value {
	entries := make([]*lapiv2.TextMap_Entry, 0, len(m))
	for _, k := range sortedKeys(m) {
		entries = append(entries, &lapiv2.TextMap_Entry{Key: k, Value: textValue(m[k])})
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{Entries: entries}}}
}

// variantValue wraps an inner Value in the participant's variant shape.
// VariantId is intentionally left nil — the participant resolves the
// type from the declaring choice's signature at exercise time, so
// pinning the package id here would just couple us to a specific snapshot.
func variantValue(constructor string, inner *lapiv2.Value) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Variant{Variant: &lapiv2.Variant{
		Constructor: constructor,
		Value:       inner,
	}}}
}

// sortedKeys is a tiny generics helper that returns the map's keys in
// lexicographic order. Used wherever we need deterministic wire form.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- DisclosedContract converter ------------------------------------

// disclosedContractsToProto translates the OpenAPI registry shape
// (`[]registry.DisclosedContract` with base64-encoded createdEventBlob)
// into the participant's gRPC `Commands.disclosed_contracts` shape
// (decoded bytes + synchronizer id).
//
// The participant uses these to look up off-ledger-known contracts at
// exercise time — the factory contract returned by the registry, plus
// any dependency contracts (e.g. the Amulet rules contract that holds
// the fee schedule).
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
