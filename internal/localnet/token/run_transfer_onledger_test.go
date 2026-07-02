package token

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	cregistry "github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// seedToken writes a TokenRef into the instance registry so the
// RunTransfer / RunAccept dispatch can resolve it. status drives the
// branch under test: "on-ledger" → issuer-administered (on-ledger
// TransferFactory); anything else → off-ledger (scan registry).
func seedToken(t *testing.T, instance, symbol, issuer, status string) {
	t.Helper()
	release, err := registry.Lock(instance)
	if err != nil {
		t.Fatalf("lock %q: %v", instance, err)
	}
	defer release()
	state, err := registry.Read(instance)
	if err != nil {
		t.Fatalf("read %q: %v", instance, err)
	}
	if state.Tokens == nil {
		state.Tokens = map[string]registry.TokenRef{}
	}
	state.Tokens[symbol] = registry.TokenRef{
		Symbol:       symbol,
		InstrumentID: symbol,
		IssuerParty:  issuer,
		Status:       status,
	}
	if err := registry.Write(state); err != nil {
		t.Fatalf("write %q: %v", instance, err)
	}
}

// seedParty registers an alias → party-id mapping in the instance so
// alias-resolution paths (RunTransfer/RunAccept) can resolve it.
func seedParty(t *testing.T, instance, alias, partyID string) {
	t.Helper()
	release, err := registry.Lock(instance)
	if err != nil {
		t.Fatalf("lock %q: %v", instance, err)
	}
	defer release()
	state, err := registry.Read(instance)
	if err != nil {
		t.Fatalf("read %q: %v", instance, err)
	}
	if state.Parties == nil {
		state.Parties = map[string]registry.PartyRef{}
	}
	state.Parties[alias] = registry.PartyRef{Alias: alias, PartyID: partyID, IsLocal: true}
	if err := registry.Write(state); err != nil {
		t.Fatalf("write %q: %v", instance, err)
	}
}

// --- dispatch: the bug regression ------------------------------------

// TestRunTransfer_OnLedgerInstrumentUsesLedgerFactory is the core
// regression for the reported bug: an issuer-administered (status
// "on-ledger") instrument MUST transfer via the issuer's on-ledger
// TransferFactory (the TokenRules contract, admin=issuer), NOT the
// off-ledger scan registry — which serves the Amulet factory (admin=DSO)
// and tripped "AssertionFailed: Expected admin 'issuer::…' matches actual
// admin 'DSO::…'". We assert the on-ledger path is taken with the
// issuer's ref and the off-ledger path is never reached.
func TestRunTransfer_OnLedgerInstrumentUsesLedgerFactory(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	seedToken(t, "demo", "XYZ", "issuer::abc", "on-ledger")

	var onLedgerCalled, offLedgerCalled bool
	var gotRef registry.TokenRef
	var gotOpts TransferOptions
	defer swapTransferOnLedger(func(_ context.Context, _ io.Writer, opts TransferOptions, ref registry.TokenRef) (string, error) {
		onLedgerCalled = true
		gotRef = ref
		gotOpts = opts
		return "offer-cid-1", nil
	})()
	defer swapTransferOffLedger(func(context.Context, io.Writer, TransferOptions) error {
		offLedgerCalled = true
		return nil
	})()

	var buf bytes.Buffer
	err := RunTransfer(context.Background(), &buf, TransferOptions{
		Instance: "demo", Instrument: "XYZ",
		From: "bob::abc", To: "issuer::abc", Amount: "200",
		Endpoint: "localhost:1", Role: "app-provider",
	})
	if err != nil {
		t.Fatalf("RunTransfer: %v", err)
	}
	if !onLedgerCalled {
		t.Fatal("on-ledger factory path was NOT taken for an on-ledger instrument")
	}
	if offLedgerCalled {
		t.Fatal("off-ledger scan-registry path WAS taken for an on-ledger instrument (the bug)")
	}
	if gotRef.IssuerParty != "issuer::abc" {
		t.Errorf("on-ledger path got issuer %q, want issuer::abc (factory admin must be the issuer)", gotRef.IssuerParty)
	}
	if gotOpts.From != "bob::abc" || gotOpts.To != "issuer::abc" || gotOpts.Amount != "200" {
		t.Errorf("on-ledger path got opts %+v, want from=bob::abc to=issuer::abc amount=200", gotOpts)
	}
	if !strings.Contains(buf.String(), "offer-cid-1") {
		t.Errorf("expected the resulting instruction id in output, got %q", buf.String())
	}
}

// TestRunTransfer_OffLedgerForRecordedAndUnknown pins the other branch:
// a recorded-only instrument (no on-ledger TokenRules) and an
// unregistered instrument (Amulet-style raw id) both keep using the
// off-ledger path. A regression that over-eagerly routed everything
// on-ledger would break Amulet transfers.
func TestRunTransfer_OffLedgerForRecordedAndUnknown(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	seedToken(t, "demo", "REC", "issuer::abc", "recorded")

	cases := []struct{ name, instrument string }{
		{"recorded instrument", "REC"},
		{"unregistered (amulet-style) instrument", "Amulet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var onLedgerCalled, offLedgerCalled bool
			defer swapTransferOnLedger(func(context.Context, io.Writer, TransferOptions, registry.TokenRef) (string, error) {
				onLedgerCalled = true
				return "", nil
			})()
			defer swapTransferOffLedger(func(context.Context, io.Writer, TransferOptions) error {
				offLedgerCalled = true
				return nil
			})()

			err := RunTransfer(context.Background(), &bytes.Buffer{}, TransferOptions{
				Instance: "demo", Instrument: tc.instrument,
				From: "bob::abc", To: "carol::abc", Amount: "5",
				Endpoint: "localhost:1", Role: "app-user",
			})
			if err != nil {
				t.Fatalf("RunTransfer: %v", err)
			}
			if onLedgerCalled {
				t.Errorf("on-ledger path taken for %s; want off-ledger", tc.name)
			}
			if !offLedgerCalled {
				t.Errorf("off-ledger path NOT taken for %s", tc.name)
			}
		})
	}
}

// TestRunAccept_PrefersOnLedgerThenFallsBack pins the accept dispatch:
// the on-ledger handler is consulted first; when it handles (or errors)
// the off-ledger path is skipped, and when it declines (handled=false)
// the off-ledger path runs.
func TestRunAccept_PrefersOnLedgerThenFallsBack(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	base := AcceptOptions{Instance: "demo", TransferInstructionID: "oc1", Endpoint: "localhost:1", Role: "app-provider"}

	t.Run("on-ledger handles → off-ledger skipped", func(t *testing.T) {
		var onCalled, offCalled bool
		defer swapAcceptOnLedger(func(context.Context, io.Writer, AcceptOptions) (bool, error) {
			onCalled = true
			return true, nil
		})()
		defer swapAcceptOffLedger(func(context.Context, io.Writer, AcceptOptions) error {
			offCalled = true
			return nil
		})()
		if err := RunAccept(context.Background(), &bytes.Buffer{}, base); err != nil {
			t.Fatalf("RunAccept: %v", err)
		}
		if !onCalled || offCalled {
			t.Errorf("onCalled=%v offCalled=%v; want on-ledger handled, off-ledger skipped", onCalled, offCalled)
		}
	})

	t.Run("on-ledger declines → off-ledger runs", func(t *testing.T) {
		var offCalled bool
		defer swapAcceptOnLedger(func(context.Context, io.Writer, AcceptOptions) (bool, error) {
			return false, nil
		})()
		defer swapAcceptOffLedger(func(context.Context, io.Writer, AcceptOptions) error {
			offCalled = true
			return nil
		})()
		if err := RunAccept(context.Background(), &bytes.Buffer{}, base); err != nil {
			t.Fatalf("RunAccept: %v", err)
		}
		if !offCalled {
			t.Error("off-ledger accept not reached when on-ledger declined")
		}
	})

	t.Run("on-ledger errors → surfaced, off-ledger skipped", func(t *testing.T) {
		boom := errors.New("boom")
		var offCalled bool
		defer swapAcceptOnLedger(func(context.Context, io.Writer, AcceptOptions) (bool, error) {
			return true, boom
		})()
		defer swapAcceptOffLedger(func(context.Context, io.Writer, AcceptOptions) error {
			offCalled = true
			return nil
		})()
		err := RunAccept(context.Background(), &bytes.Buffer{}, base)
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want boom", err)
		}
		if offCalled {
			t.Error("off-ledger accept ran despite on-ledger error")
		}
	})
}

// TestRunAccept_ResolvesPartyAlias pins that `transfer accept --party
// <alias>` resolves the alias to a full party id before dispatch. The
// on-ledger accept puts the receiver into an ACS party filter, and an
// unresolved alias is not a valid filter key — it would poison the
// lookup, make the on-ledger detection bail, and wrongly send the
// instruction to the off-ledger registry.
func TestRunAccept_ResolvesPartyAlias(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	seedParty(t, "demo", "carol", "carol::1220deadbeef")

	var gotParty string
	defer swapAcceptOnLedger(func(_ context.Context, _ io.Writer, opts AcceptOptions) (bool, error) {
		gotParty = opts.Party
		return true, nil
	})()
	defer swapAcceptOffLedger(func(context.Context, io.Writer, AcceptOptions) error {
		t.Fatal("off-ledger accept must not run when on-ledger handles it")
		return nil
	})()

	err := RunAccept(context.Background(), &bytes.Buffer{}, AcceptOptions{
		Instance: "demo", TransferInstructionID: "oc1", Party: "carol",
		Endpoint: "localhost:1", Role: "app-provider",
	})
	if err != nil {
		t.Fatalf("RunAccept: %v", err)
	}
	if gotParty != "carol::1220deadbeef" {
		t.Errorf("opts.Party = %q, want resolved full id carol::1220deadbeef", gotParty)
	}
}

// --- pure helpers ----------------------------------------------------

func TestAccountPartiesOf(t *testing.T) {
	cases := []struct {
		name                   string
		admin, owner, provider string
		want                   []string
	}{
		{"owner + distinct provider", "DSO", "bob", "adm", []string{"bob", "adm"}},
		{"provider equals owner", "adm", "bob", "bob", []string{"bob"}},
		{"self-custodial (no provider)", "adm", "bob", "", []string{"bob"}},
		{"special account (no owner) → admin", "adm", "", "", []string{"adm"}},
		{"owner==provider==admin", "adm", "adm", "adm", []string{"adm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := accountPartiesOf(tc.admin, tc.owner, tc.provider)
			if !equalStrings(got, tc.want) {
				t.Errorf("accountPartiesOf(%q,%q,%q) = %v, want %v",
					tc.admin, tc.owner, tc.provider, got, tc.want)
			}
		})
	}
}

func TestDedupParties(t *testing.T) {
	got := dedupParties([]string{"a", "", "a", "b", "", "c", "b"})
	if !equalStrings(got, []string{"a", "b", "c"}) {
		t.Errorf("dedupParties = %v, want [a b c]", got)
	}
	if dedupParties(nil) != nil {
		t.Error("dedupParties(nil) should be nil")
	}
}

func TestSelectSenderAccountAndInputs(t *testing.T) {
	t.Run("single account, enough", func(t *testing.T) {
		hs := []holdingRef{
			{ContractID: "c1", Owner: "bob", Provider: "adm", Admin: "iss", Instrument: "X", Amount: "10.0"},
			{ContractID: "c2", Owner: "bob", Provider: "adm", Admin: "iss", Instrument: "X", Amount: "25.0"},
		}
		acct, picked, total, err := selectSenderAccountAndInputs(hs, "30")
		if err != nil {
			t.Fatal(err)
		}
		if acct.Provider != "adm" || acct.Owner != "bob" || acct.Admin != "iss" {
			t.Errorf("account = %+v, want owner=bob provider=adm admin=iss", acct)
		}
		if len(picked) != 2 {
			t.Errorf("picked %d, want 2 (10+25 covers 30)", len(picked))
		}
		if total != "35.0000000000" {
			t.Errorf("total = %q", total)
		}
	})

	t.Run("never mixes accounts; picks the funded one", func(t *testing.T) {
		hs := []holdingRef{
			{ContractID: "c1", Owner: "bob", Provider: "adm", Admin: "iss", Instrument: "X", Amount: "10.0"},
			{ContractID: "c2", Owner: "bob", Provider: "", Admin: "iss", Instrument: "X", Amount: "100.0"},
		}
		acct, picked, _, err := selectSenderAccountAndInputs(hs, "50")
		if err != nil {
			t.Fatal(err)
		}
		// Only the self-custodial (provider="") account holds enough; the
		// picked inputs must all belong to one account.
		if acct.Provider != "" {
			t.Errorf("account provider = %q, want \"\" (the funded self-custodial account)", acct.Provider)
		}
		for _, p := range picked {
			if p.Provider != acct.Provider {
				t.Errorf("picked input from a different account: %+v vs account %+v", p, acct)
			}
		}
	})

	t.Run("insufficient in any single account errors", func(t *testing.T) {
		hs := []holdingRef{
			{ContractID: "c1", Owner: "bob", Provider: "adm", Admin: "iss", Instrument: "X", Amount: "10.0"},
			{ContractID: "c2", Owner: "bob", Provider: "", Admin: "iss", Instrument: "X", Amount: "20.0"},
		}
		if _, _, _, err := selectSenderAccountAndInputs(hs, "25"); err == nil {
			t.Error("want error: no single account covers 25 (10 and 20 separately)")
		}
	})

	t.Run("empty errors", func(t *testing.T) {
		if _, _, _, err := selectSenderAccountAndInputs(nil, "1"); err == nil {
			t.Error("want error on empty holdings")
		}
	})
}

// TestBuildTestTokenChoiceContext pins the on-ledger choice-context
// shape the TestTokenV2 transfer/accept state machine reads: a Choice
// context whose `values` map carries the tokenRules contract id and the
// list of accountConfig contract ids under the exact upstream keys.
func TestBuildTestTokenChoiceContext(t *testing.T) {
	ctx := buildTestTokenChoiceContext("tr-1", []string{"cfg-a", "cfg-b"})
	values := contextValuesMap(t, ctx)

	// keys must match the upstream TestTokenV2 constants verbatim.
	if _, ok := values[tokenRulesContextKey]; !ok {
		t.Fatalf("missing %q key; have %v", tokenRulesContextKey, keysOf(values))
	}
	if _, ok := values[accountConfigsContextKey]; !ok {
		t.Fatalf("missing %q key; have %v", accountConfigsContextKey, keysOf(values))
	}
	if tokenRulesContextKey != "testTokenV2/tokenRules" || accountConfigsContextKey != "testTokenV2/accountConfigs" {
		t.Fatalf("context keys drifted from upstream TestTokenV2: %q / %q", tokenRulesContextKey, accountConfigsContextKey)
	}

	// tokenRules → AV_ContractId "tr-1".
	ctor, inner := variantOf(t, values[tokenRulesContextKey])
	if ctor != "AV_ContractId" || contractIDOf(inner) != "tr-1" {
		t.Errorf("tokenRules entry = (%s,%q), want (AV_ContractId, tr-1)", ctor, contractIDOf(inner))
	}

	// accountConfigs → AV_List of AV_ContractId.
	lctor, linner := variantOf(t, values[accountConfigsContextKey])
	if lctor != "AV_List" {
		t.Fatalf("accountConfigs ctor = %s, want AV_List", lctor)
	}
	lst, ok := linner.Sum.(*lapiv2.Value_List)
	if !ok {
		t.Fatalf("accountConfigs inner is not a List: %T", linner.Sum)
	}
	var gotCIDs []string
	for _, e := range lst.List.Elements {
		c, ci := variantOf(t, e)
		if c != "AV_ContractId" {
			t.Errorf("config element ctor = %s, want AV_ContractId", c)
		}
		gotCIDs = append(gotCIDs, contractIDOf(ci))
	}
	if !equalStrings(gotCIDs, []string{"cfg-a", "cfg-b"}) {
		t.Errorf("config cids = %v, want [cfg-a cfg-b]", gotCIDs)
	}

	// Self-custodial both sides → empty config list (still present).
	empty := contextValuesMap(t, buildTestTokenChoiceContext("tr-1", nil))
	_, ei := variantOf(t, empty[accountConfigsContextKey])
	if el, ok := ei.Sum.(*lapiv2.Value_List); !ok || len(el.List.Elements) != 0 {
		t.Errorf("empty config list expected, got %#v", ei.Sum)
	}
}

// TestExtractTransferFromArgs covers the offer-introspection used by the
// standalone on-ledger accept routing: parse a TokenTransferOffer's
// create arguments into sender/receiver accounts + instrument admin.
func TestExtractTransferFromArgs(t *testing.T) {
	args := &lapiv2.Record{Fields: []*lapiv2.RecordField{
		{Label: "mintAmount", Value: numericValue("0.0")},
		{Label: "transfer", Value: recordValue([]field{
			{"sender", buildAccountRecord(acct("bob", "adm", ""))},
			{"receiver", buildAccountRecord(acct("carol", "", ""))},
			{"instrumentId", buildInstrumentIDRecord(instr("iss", "X"))},
		})},
	}}
	sender, receiver, admin, ok := extractTransferFromArgs(args)
	if !ok {
		t.Fatal("expected ok=true for a well-formed transfer offer")
	}
	if admin != "iss" {
		t.Errorf("admin = %q, want iss", admin)
	}
	if sender.Owner != "bob" || sender.Provider != "adm" {
		t.Errorf("sender = %+v, want owner=bob provider=adm", sender)
	}
	if receiver.Owner != "carol" || receiver.Provider != "" {
		t.Errorf("receiver = %+v, want owner=carol provider=\"\"", receiver)
	}
	if sender.Admin != "iss" || sender.Instrument != "X" {
		t.Errorf("sender admin/instrument = %q/%q, want iss/X", sender.Admin, sender.Instrument)
	}

	if _, _, _, ok := extractTransferFromArgs(&lapiv2.Record{}); ok {
		t.Error("expected ok=false for args without a transfer field")
	}
}

func TestAccountConfigMatches(t *testing.T) {
	mk := func(owner, provider string) *lapiv2.Record {
		return &lapiv2.Record{Fields: []*lapiv2.RecordField{
			{Label: "admin", Value: partyValue("iss")},
			{Label: "account", Value: buildAccountRecord(acct(owner, provider, ""))},
		}}
	}
	if !accountConfigMatches(mk("bob", "adm"), "bob", "adm") {
		t.Error("want match for (bob, adm)")
	}
	if accountConfigMatches(mk("bob", "adm"), "bob", "other") {
		t.Error("want no match when provider differs")
	}
	if accountConfigMatches(mk("bob", ""), "bob", "") {
		// self-custodial config (provider None) matches provider="".
	} else {
		t.Error("want match for self-custodial (bob, \"\")")
	}
	if accountConfigMatches(nil, "bob", "adm") {
		t.Error("nil args must not match")
	}
}

func TestTokenAccountRegistryAccount(t *testing.T) {
	got := tokenAccount{Owner: "bob", Provider: "adm", AccountID: "acc1"}.registryAccount()
	if got.Owner == nil || *got.Owner != "bob" {
		t.Errorf("owner = %v, want bob", got.Owner)
	}
	if got.Provider == nil || *got.Provider != "adm" {
		t.Errorf("provider = %v, want adm", got.Provider)
	}
	if got.ID != "acc1" {
		t.Errorf("id = %q, want acc1", got.ID)
	}
	// Self-custodial: provider must encode as None (nil pointer).
	self := tokenAccount{Owner: "bob"}.registryAccount()
	if self.Provider != nil {
		t.Errorf("self-custodial provider = %v, want nil (None)", self.Provider)
	}
}

func TestBuildTestTokenExtraArgs(t *testing.T) {
	extra := buildTestTokenExtraArgs("tr-1", []string{"cfg-a"})
	rec := recordOf(extra)
	if rec == nil {
		t.Fatalf("extraArgs is not a record: %#v", extra.GetSum())
	}
	var ctx, meta *lapiv2.Value
	for _, f := range rec.Fields {
		switch f.Label {
		case "context":
			ctx = f.Value
		case "meta":
			meta = f.Value
		}
	}
	if ctx == nil || meta == nil {
		t.Fatalf("extraArgs must carry context + meta, got fields %d", len(rec.Fields))
	}
	values := contextValuesMap(t, ctx)
	if _, ok := values[tokenRulesContextKey]; !ok {
		t.Errorf("context missing %q", tokenRulesContextKey)
	}
}

// --- test helpers ----------------------------------------------------

func acct(owner, provider, id string) cregistry.Account {
	o := owner
	a := cregistry.Account{Owner: &o, ID: id}
	if provider != "" {
		p := provider
		a.Provider = &p
	}
	return a
}

func instr(admin, id string) cregistry.InstrumentID {
	return cregistry.InstrumentID{Admin: admin, ID: id}
}

func swapTransferOnLedger(fn func(context.Context, io.Writer, TransferOptions, registry.TokenRef) (string, error)) func() {
	prev := runTransferOnLedgerFn
	runTransferOnLedgerFn = fn
	return func() { runTransferOnLedgerFn = prev }
}

func swapTransferOffLedger(fn func(context.Context, io.Writer, TransferOptions) error) func() {
	prev := runTransferOffLedgerFn
	runTransferOffLedgerFn = fn
	return func() { runTransferOffLedgerFn = prev }
}

func swapAcceptOnLedger(fn func(context.Context, io.Writer, AcceptOptions) (bool, error)) func() {
	prev := runAcceptOnLedgerFn
	runAcceptOnLedgerFn = fn
	return func() { runAcceptOnLedgerFn = prev }
}

func swapAcceptOffLedger(fn func(context.Context, io.Writer, AcceptOptions) error) func() {
	prev := runAcceptOffLedgerFn
	runAcceptOffLedgerFn = fn
	return func() { runAcceptOffLedgerFn = prev }
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contextValuesMap(t *testing.T, v *lapiv2.Value) map[string]*lapiv2.Value {
	t.Helper()
	rec := recordOf(v)
	if rec == nil {
		t.Fatalf("context is not a record: %#v", v.GetSum())
	}
	var values *lapiv2.Value
	for _, f := range rec.Fields {
		if f.Label == "values" {
			values = f.Value
		}
	}
	tm, ok := values.GetSum().(*lapiv2.Value_TextMap)
	if !ok {
		t.Fatalf("context.values is not a TextMap: %#v", values.GetSum())
	}
	out := map[string]*lapiv2.Value{}
	for _, e := range tm.TextMap.Entries {
		out[e.Key] = e.Value
	}
	return out
}

func variantOf(t *testing.T, v *lapiv2.Value) (string, *lapiv2.Value) {
	t.Helper()
	vv, ok := v.GetSum().(*lapiv2.Value_Variant)
	if !ok {
		t.Fatalf("value is not a variant: %#v", v.GetSum())
	}
	return vv.Variant.Constructor, vv.Variant.Value
}

func contractIDOf(v *lapiv2.Value) string {
	if c, ok := v.GetSum().(*lapiv2.Value_ContractId); ok {
		return c.ContractId
	}
	return ""
}

func keysOf(m map[string]*lapiv2.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
