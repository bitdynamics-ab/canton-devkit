package localnet

import (
	"strings"
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// TestBuildEventFormat_PartyHandling pins the // `--party` is no longer silent. Empty parties → FiltersForAnyParty
// (wildcard); non-empty → FiltersByParty with one entry per party.
func TestBuildEventFormat_PartyHandling(t *testing.T) {
	t.Run("no parties + no templates → wildcard is non-nil empty FiltersForAnyParty", func(t *testing.T) {
		// Daml LF v2 contract: Canton REJECTS an EventFormat whose
		// FiltersByParty is empty AND FiltersForAnyParty is nil
		// (INVALID_ARGUMENT: "filtersByParty and filtersForAnyParty
		// cannot both be empty"). The wildcard is a NON-NIL empty
		// *Filters{} in FiltersForAnyParty — an empty Cumulative
		// list defaults to a wildcard CumulativeFilter, which the
		// participant gates by the JWT's claim. Pin that shape so
		// the flag-less default never regresses to the rejected
		// empty-EventFormat form.
		f := buildEventFormat(nil, nil, true)
		if len(f.FiltersByParty) != 0 {
			t.Errorf("FiltersByParty should be empty (wildcard), got %d entries",
				len(f.FiltersByParty))
		}
		if f.FiltersForAnyParty == nil {
			t.Fatal("wildcard: FiltersForAnyParty must be a non-nil empty Filters, got nil " +
				"(Canton rejects FiltersByParty-empty + FiltersForAnyParty-nil)")
		}
		if len(f.FiltersForAnyParty.Cumulative) != 0 {
			t.Errorf("wildcard: FiltersForAnyParty.Cumulative should be empty, got %d",
				len(f.FiltersForAnyParty.Cumulative))
		}
		if !f.Verbose {
			t.Error("verbose flag not propagated")
		}
	})
	t.Run("no parties + templates → wildcard FOR-ANY-PARTY with template filter set", func(t *testing.T) {
		f := buildEventFormat(nil, []string{"M:E"}, false)
		if f.FiltersForAnyParty == nil {
			t.Fatal("with templates: FiltersForAnyParty should be non-nil")
		}
		if len(f.FiltersForAnyParty.Cumulative) != 1 {
			t.Errorf("expected 1 cumulative template filter, got %d",
				len(f.FiltersForAnyParty.Cumulative))
		}
	})
	t.Run("single party → by-party with one non-nil entry", func(t *testing.T) {
		f := buildEventFormat([]string{"alice"}, nil, false)
		if f.FiltersForAnyParty != nil {
			t.Error("single party: wildcard should be nil")
		}
		v, ok := f.FiltersByParty["alice"]
		if !ok {
			t.Errorf("missing alice entry; got keys %v",
				keysOf(f.FiltersByParty))
		}
		// With no --template the per-party value must be a non-nil
		// empty *Filters{} (wildcard), never nil — a nil map value
		// is the rejected "party key with no filter" shape.
		if v == nil {
			t.Error("party filter value is nil; want non-nil empty Filters")
		}
	})
	t.Run("multi-party → one entry per", func(t *testing.T) {
		f := buildEventFormat([]string{"alice", "bob", "carol"}, nil, false)
		if len(f.FiltersByParty) != 3 {
			t.Errorf("multi-party: want 3 entries, got %d", len(f.FiltersByParty))
		}
		for _, p := range []string{"alice", "bob", "carol"} {
			if _, ok := f.FiltersByParty[p]; !ok {
				t.Errorf("missing %s entry", p)
			}
		}
	})
}

// TestBuildTemplateFilters_ParseShapes pins the --template flag
// grammar. Reviewer asked for it to actually work — this locks
// the contract.
func TestBuildTemplateFilters_ParseShapes(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantErr    bool
		wantPkg    string // for the first parsed filter
		wantMod    string
		wantEntity string
		wantCount  int
	}{
		{
			name:      "empty input → nil filters (wildcard)",
			in:        nil,
			wantCount: 0,
		},
		{
			name:       "Module:Entity (no package pin)",
			in:         []string{"Retail.Token:Token"},
			wantPkg:    "",
			wantMod:    "Retail.Token",
			wantEntity: "Token",
			wantCount:  1,
		},
		{
			name:       "pkg-name:Module:Entity → #name reference",
			in:         []string{"retail-token:Retail.Token:Token"},
			wantPkg:    "#retail-token",
			wantMod:    "Retail.Token",
			wantEntity: "Token",
			wantCount:  1,
		},
		{
			name:       "#pkg-name:Module:Entity passes through",
			in:         []string{"#retail-token:Retail.Token:Token"},
			wantPkg:    "#retail-token",
			wantMod:    "Retail.Token",
			wantEntity: "Token",
			wantCount:  1,
		},
		{
			name:       "hex package-id stays an exact pin",
			in:         []string{strings.Repeat("a1", 32) + ":Retail.Token:Token"},
			wantPkg:    strings.Repeat("a1", 32),
			wantMod:    "Retail.Token",
			wantEntity: "Token",
			wantCount:  1,
		},
		{
			name:    "single segment is invalid",
			in:      []string{"Token"},
			wantErr: true,
		},
		{
			name:    "too many segments invalid",
			in:      []string{"a:b:c:d:e"},
			wantErr: true,
		},
		{
			name:       "multiple templates accumulate",
			in:         []string{"M1:E1", "M2:E2", "M3:E3"},
			wantPkg:    "",
			wantMod:    "M1",
			wantEntity: "E1",
			wantCount:  3,
		},
		{
			name:       "whitespace stripped",
			in:         []string{"  Module:Entity  "},
			wantPkg:    "",
			wantMod:    "Module",
			wantEntity: "Entity",
			wantCount:  1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := buildTemplateFilters(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if c.wantCount == 0 {
				if f != nil {
					t.Errorf("empty input should yield nil filters, got %+v", f)
				}
				return
			}
			if got := len(f.Cumulative); got != c.wantCount {
				t.Errorf("got %d cumulative filters, want %d", got, c.wantCount)
			}
			tplFilter := f.Cumulative[0].GetTemplateFilter()
			if tplFilter == nil || tplFilter.TemplateId == nil {
				t.Fatalf("first filter missing template id; got %+v", f.Cumulative[0])
			}
			id := tplFilter.TemplateId
			if id.PackageId != c.wantPkg {
				t.Errorf("PackageId = %q, want %q", id.PackageId, c.wantPkg)
			}
			if id.ModuleName != c.wantMod {
				t.Errorf("ModuleName = %q, want %q", id.ModuleName, c.wantMod)
			}
			if id.EntityName != c.wantEntity {
				t.Errorf("EntityName = %q, want %q", id.EntityName, c.wantEntity)
			}
		})
	}
}

// TestBuildUpdateFormat_HasACSDeltaShape pins that `tx ls` and
// `contracts watch` request the flat ACS-delta transaction shape —
// their use case is the ACS table, not the tree-shaped replay view.
func TestBuildUpdateFormat_HasACSDeltaShape(t *testing.T) {
	uf := buildUpdateFormat(nil, nil, true)
	if uf.IncludeTransactions == nil {
		t.Fatal("UpdateFormat.IncludeTransactions is nil")
	}
	got := uf.IncludeTransactions.TransactionShape
	want := lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA
	if got != want {
		t.Errorf("TransactionShape = %v, want %v", got, want)
	}
}

// TestResolveOffsetWindow pins the --from/--to window semantics:
// the default window is a generous fixed span DECOUPLED from --limit
// (so filtered queries find sparse matches), and --from is a real
// offset gated by `Changed` — `--from 0` reads from genesis.
func TestResolveOffsetWindow(t *testing.T) {
	const span = defaultTxWindowSpan
	cases := []struct {
		name           string
		fromOff, toOff int64
		fromSet, toSet bool
		ledgerEnd      int64
		wantBegin      int64
		wantEnd        int64
		wantErr        bool
	}{
		{
			name:      "no flags → generous recent window (span back from end), not end-limit",
			ledgerEnd: 1_000_000,
			wantBegin: 1_000_000 - span, wantEnd: 1_000_000,
		},
		{
			name:      "default window clamps to 0 when end < span",
			ledgerEnd: 100,
			wantBegin: 0, wantEnd: 100,
		},
		{
			name:  "explicit --to overrides ledger end; default begin is span back from --to",
			toOff: 500_000, toSet: true, ledgerEnd: 1_000_000,
			wantBegin: 500_000 - span, wantEnd: 500_000,
		},
		{
			name:    "explicit --from is an exact offset (ignores window span)",
			fromOff: 200, fromSet: true, ledgerEnd: 1_000_000,
			wantBegin: 200, wantEnd: 1_000_000,
		},
		{
			name:    "--from 0 (explicit) reads from genesis, not a sentinel",
			fromOff: 0, fromSet: true, ledgerEnd: 1_000_000,
			wantBegin: 0, wantEnd: 1_000_000,
		},
		{
			name:    "explicit --from and --to",
			fromOff: 100, toOff: 300, fromSet: true, toSet: true, ledgerEnd: 1_000_000,
			wantBegin: 100, wantEnd: 300,
		},
		{
			name:    "--from > --to fails loudly",
			fromOff: 500, toOff: 100, fromSet: true, toSet: true, ledgerEnd: 1_000_000,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			begin, endIncl, err := resolveOffsetWindow(c.fromOff, c.toOff, c.fromSet, c.toSet, c.ledgerEnd)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if begin != c.wantBegin {
				t.Errorf("begin = %d, want %d", begin, c.wantBegin)
			}
			if *endIncl != c.wantEnd {
				t.Errorf("end = %d, want %d", *endIncl, c.wantEnd)
			}
		})
	}
}

// TestTruncMiddle keeps long contract IDs visually scannable while
// preserving the distinguishing tail bits. Algorithm: when n>=5
// the middle gets the ellipsis (so contract IDs with shared
// prefixes stay distinguishable by tail); when n<5 we fall back
// to a simple tail-truncate.
func TestTruncMiddle(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},                   // fits
		{"exactlength", 11, "exactlength"},       // exactly fits
		{"abcdefghijklmnopqrst", 9, "abcd…qrst"}, // middle-trunc with keep=4 + tail=4
		{"abcdefghij", 5, "ab…ij"},               // middle-trunc with keep=2 + tail=2
		{"abc", 2, "a…"},                         // n<5 falls through to truncTail
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := truncMiddle(c.in, c.n)
			if got != c.want {
				t.Errorf("truncMiddle(%q, %d) = %q, want %q",
					c.in, c.n, got, c.want)
			}
			_ = strings.Contains // keep import even if no contains check fires
		})
	}
}

func keysOf(m map[string]*lapiv2.Filters) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
