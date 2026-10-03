package localnet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/poc/godamlprobe"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func stubEnsureLedgerReadyOK(t *testing.T) {
	t.Helper()
	prev := ensureLedgerReadyFn
	ensureLedgerReadyFn = func(context.Context, string, map[string]int, map[string]registry.Credential) (godamlprobe.Result, error) {
		return godamlprobe.Result{
			Endpoint:         "localhost:9",
			LedgerAPIVersion: "test",
			Offset:           0,
		}, nil
	}
	t.Cleanup(func() { ensureLedgerReadyFn = prev })
}

func TestLedgerEndpointFromPorts(t *testing.T) {
	t.Parallel()
	ep, err := ledgerEndpointFromPorts(map[string]int{
		"participant_ledger_app-provider": 61169,
	}, string(splice.RoleAppProvider))
	if err != nil {
		t.Fatalf("ledgerEndpointFromPorts: %v", err)
	}
	if ep != "localhost:61169" {
		t.Fatalf("endpoint = %q, want localhost:61169", ep)
	}

	_, err = ledgerEndpointFromPorts(map[string]int{}, string(splice.RoleAppProvider))
	if err == nil {
		t.Fatal("missing port: want error")
	}
	if !strings.Contains(err.Error(), "participant_ledger_app-provider") {
		t.Fatalf("error = %q, want missing-key hint", err.Error())
	}
}

func TestEnsureLedgerReady_Success(t *testing.T) {
	oldProbe := probeLedgerFn
	t.Cleanup(func() { probeLedgerFn = oldProbe })

	var calls int
	probeLedgerFn = func(_ context.Context, opts godamlprobe.Options) (godamlprobe.Result, error) {
		calls++
		if opts.Endpoint != "localhost:5001" {
			t.Fatalf("endpoint = %q, want localhost:5001", opts.Endpoint)
		}
		if opts.Token != "tok" {
			t.Fatalf("token = %q, want tok", opts.Token)
		}
		return godamlprobe.Result{
			Endpoint:         opts.Endpoint,
			LedgerAPIVersion: "3.3.0",
			Offset:           42,
		}, nil
	}

	res, err := ensureLedgerReady(context.Background(), "", map[string]int{
		"participant_ledger_app-provider": 5001,
	}, map[string]registry.Credential{
		string(splice.RoleAppProvider): {JWT: "tok"},
	})
	if err != nil {
		t.Fatalf("ensureLedgerReady: %v", err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	if res.Offset != 42 || res.LedgerAPIVersion != "3.3.0" {
		t.Fatalf("result = %+v", res)
	}
}

func TestEnsureLedgerReady_RetriesThenSucceeds(t *testing.T) {
	oldProbe := probeLedgerFn
	oldWait := ledgerReadyPollWait
	t.Cleanup(func() {
		probeLedgerFn = oldProbe
		ledgerReadyPollWait = oldWait
	})
	ledgerReadyPollWait = 5 * time.Millisecond

	var calls int
	probeLedgerFn = func(context.Context, godamlprobe.Options) (godamlprobe.Result, error) {
		calls++
		if calls < 3 {
			return godamlprobe.Result{}, errors.New("connection refused")
		}
		return godamlprobe.Result{Endpoint: "localhost:5001", Offset: 1}, nil
	}

	res, err := ensureLedgerReady(context.Background(), "", map[string]int{
		"participant_ledger_app-provider": 5001,
	}, map[string]registry.Credential{
		string(splice.RoleAppProvider): {JWT: "tok"},
	})
	if err != nil {
		t.Fatalf("ensureLedgerReady: %v", err)
	}
	if calls != 3 {
		t.Fatalf("probe calls = %d, want 3", calls)
	}
	if res.Endpoint != "localhost:5001" {
		t.Fatalf("endpoint = %q", res.Endpoint)
	}
}

func TestEnsureLedgerReady_Timeout(t *testing.T) {
	oldProbe := probeLedgerFn
	oldWait := ledgerReadyPollWait
	oldTimeout := ledgerReadyTimeout
	t.Cleanup(func() {
		probeLedgerFn = oldProbe
		ledgerReadyPollWait = oldWait
		ledgerReadyTimeout = oldTimeout
	})
	ledgerReadyPollWait = 5 * time.Millisecond
	ledgerReadyTimeout = 40 * time.Millisecond

	probeLedgerFn = func(context.Context, godamlprobe.Options) (godamlprobe.Result, error) {
		return godamlprobe.Result{}, errors.New("Unavailable")
	}

	_, err := ensureLedgerReady(context.Background(), "", map[string]int{
		"participant_ledger_app-provider": 5001,
	}, map[string]registry.Credential{
		string(splice.RoleAppProvider): {JWT: "tok"},
	})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "app-provider") {
		t.Fatalf("error should name role, got %q", err.Error())
	}
}
