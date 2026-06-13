package token

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedTokenInstance writes a running instance with one recorded token
// and (deliberately) no participant_ledger_* port, so `token balance`
// takes the registry pseudo-balance fallback — the #63 case under test.
func seedTokenInstance(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DataDir = t.TempDir()
	s.ProjectDir = t.TempDir()
	s.Status = registry.StatusRunning
	s.Tokens = map[string]registry.TokenRef{
		"RTK": {
			Name: "Retail Token", Symbol: "RTK", Decimals: 6,
			InitialSupply: "1000000", IssuerParty: "alice::abc",
			InstrumentID: "instr-1", Status: "recorded",
		},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// TestBalance_RegistryFallback_TextShowsSource pins #63 on the CLI text
// path: with no live ledger, the SOURCE column reads "registry" and a
// stderr note warns the amounts are pseudo-balances, not on-ledger.
func TestBalance_RegistryFallback_TextShowsSource(t *testing.T) {
	seedTokenInstance(t, "demo")
	cmd := buildBalance()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--instance", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("balance: %v", err)
	}
	stdout := out.String()
	if !strings.Contains(stdout, "SOURCE") {
		t.Errorf("text output missing SOURCE column header:\n%s", stdout)
	}
	if !strings.Contains(stdout, "registry") {
		t.Errorf("text output missing registry source tag:\n%s", stdout)
	}
	if !strings.Contains(errBuf.String(), "not on-ledger holdings") {
		t.Errorf("missing registry-fallback note on stderr:\n%s", errBuf.String())
	}
}

// TestBalance_RegistryFallback_JSONEnvelope pins the shared wire shape:
// --format json emits types.TokenHoldingsResponse with source="registry"
// and each row carrying the same source — byte-identical to the Web UI's
// holdings endpoint.
func TestBalance_RegistryFallback_JSONEnvelope(t *testing.T) {
	seedTokenInstance(t, "demo")
	cmd := buildBalance()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--instance", "demo", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("balance --format json: %v", err)
	}
	var got types.TokenHoldingsResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON envelope: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
	if got.Source != types.HoldingSourceRegistry {
		t.Errorf("response source = %q, want %q", got.Source, types.HoldingSourceRegistry)
	}
	if len(got.Holdings) != 1 || got.Holdings[0].Source != types.HoldingSourceRegistry {
		t.Errorf("row source drift: %+v", got.Holdings)
	}
	if got.Holdings[0].Amount != "1000000" {
		t.Errorf("issuer pseudo-balance = %q, want 1000000", got.Holdings[0].Amount)
	}
}

// TestBalanceRowJSONParity pins that token.BalanceRow (the neutral
// orchestration shape) and types.TokenHolding (the HTTP/CLI wire shape)
// keep an identical JSON shape. They are duplicated across the import
// boundary — localnet/token mustn't depend on api/types — so only a test
// catches a field/tag drifting on one side and breaking round-tripping.
func TestBalanceRowJSONParity(t *testing.T) {
	src := token.BalanceRow{
		InstrumentSymbol: "RTK",
		InstrumentID:     "instr-1",
		Party:            "alice::abc",
		Amount:           "1000000",
		Source:           token.SourceLedger,
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	var dst types.TokenHolding
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("token.BalanceRow JSON does not fit types.TokenHolding: %v", err)
	}
	rawBack, err := json.Marshal(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(rawBack) {
		t.Errorf("JSON shape drift between BalanceRow and TokenHolding:\n token:  %s\n types:  %s", raw, rawBack)
	}
	if got, want := jsonTags(reflect.TypeOf(types.TokenHolding{})), jsonTags(reflect.TypeOf(token.BalanceRow{})); !reflect.DeepEqual(got, want) {
		t.Errorf("json tag sets differ:\n types: %v\n token: %v", got, want)
	}
}
