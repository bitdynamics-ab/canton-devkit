package token

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedInstance writes the minimum registry State required for the
// token-create orchestration tests. Mirrors the pattern used by
// internal/ui/handlers tests so the test surface stays uniform.
func seedInstance(t *testing.T, name string) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DataDir = t.TempDir()
	s.ProjectDir = t.TempDir()
	s.Status = registry.StatusRunning
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// happyPath := a populated CreateOptions that validates cleanly.
func happyOpts(instance string) CreateOptions {
	return CreateOptions{
		Instance:      instance,
		Name:          "Retail Token",
		Symbol:        "RTK",
		Decimals:      6,
		InitialSupply: "1000000",
		Issuer:        "alice::abc",
	}
}

// TestRunCreate_HappyPath_PersistsRef pins the contract: a valid
// create writes a TokenRef into state.Tokens keyed by symbol, with a
// generated InstrumentID and Status="recorded".
func TestRunCreate_HappyPath_PersistsRef(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	var out bytes.Buffer
	res, err := RunCreate(&out, happyOpts("demo"))
	if err != nil {
		t.Fatalf("RunCreate: %v", err)
	}
	if res.TokenRef.Symbol != "RTK" {
		t.Errorf("Symbol = %q, want RTK", res.TokenRef.Symbol)
	}
	if res.TokenRef.InstrumentID == "" {
		t.Errorf("InstrumentID must be populated")
	}
	if res.TokenRef.Status != "recorded" {
		t.Errorf("Status = %q, want recorded (until v2 submit lands)", res.TokenRef.Status)
	}

	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("read after create: %v", err)
	}
	if persisted, ok := got.Tokens["RTK"]; !ok {
		t.Errorf("RTK not persisted; tokens = %+v", got.Tokens)
	} else if persisted.InstrumentID != res.TokenRef.InstrumentID {
		t.Errorf("persisted InstrumentID drifted: %q vs %q",
			persisted.InstrumentID, res.TokenRef.InstrumentID)
	}
}

// TestRunCreate_DuplicateSymbol returns ErrSymbolInUse without
// mutating state — pinned so the CLI / HTTP handler can map this to
// ExitUserError / 409 unambiguously.
func TestRunCreate_DuplicateSymbol(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := RunCreate(nil, happyOpts("demo"))
	if err == nil {
		t.Fatal("expected error on duplicate symbol, got nil")
	}
	if !errors.Is(err, ErrSymbolInUse) {
		t.Errorf("expected ErrSymbolInUse, got %v", err)
	}

	// And persistence shouldn't have grown.
	got, _ := registry.Read("demo")
	if len(got.Tokens) != 1 {
		t.Errorf("Tokens len = %d after dup create, want 1 unchanged", len(got.Tokens))
	}
}

// TestRunCreate_ValidationRejects covers the validateCreate surface
// with one minimal failing case per field so a regression that
// loosens an invariant fails loudly.
func TestRunCreate_ValidationRejects(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	base := happyOpts("demo")

	cases := []struct {
		name string
		mut  func(*CreateOptions)
		want string
	}{
		{"empty instance", func(o *CreateOptions) { o.Instance = "" }, "instance name is required"},
		{"empty name", func(o *CreateOptions) { o.Name = "" }, "instrument name is required"},
		{"empty symbol", func(o *CreateOptions) { o.Symbol = "" }, "symbol is required"},
		{"symbol too long", func(o *CreateOptions) { o.Symbol = strings.Repeat("X", 17) }, "exceeds 16"},
		{"symbol bad char", func(o *CreateOptions) { o.Symbol = "RTK-1" }, "disallowed character"},
		{"decimals negative", func(o *CreateOptions) { o.Decimals = -1 }, "out of range"},
		{"decimals too big", func(o *CreateOptions) { o.Decimals = 19 }, "out of range"},
		{"empty supply", func(o *CreateOptions) { o.InitialSupply = "" }, "initial supply is required"},
		{"non-numeric supply", func(o *CreateOptions) { o.InitialSupply = "1.2e3" }, "not a valid decimal"},
		{"empty issuer", func(o *CreateOptions) { o.Issuer = "" }, "issuer party is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			tc.mut(&opts)
			_, err := RunCreate(nil, opts)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestResolveBySymbol_MissingIsFriendlyError covers the
// downstream-friendliness contract: when mint/transfer/burn looks up
// a symbol that hasn't been created, the user gets a hint about
// `token create`, not a raw map-miss error.
func TestResolveBySymbol_MissingIsFriendlyError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	_, err := ResolveBySymbol("demo", "GHOST")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "token create") {
		t.Errorf("error %q should mention `token create`", err.Error())
	}
}

// TestListTokens_EmptyReturnsEmptySlice — the empty-state contract
// for callers expecting `[]` not `null` (frontend depends on this).
func TestListTokens_EmptyReturnsEmptySlice(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	got, err := ListTokens("demo")
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if got == nil {
		t.Error("ListTokens returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
