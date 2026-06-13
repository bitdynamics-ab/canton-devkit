package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

// isExitCode reports whether err is an ExitCodeError matching code.
func isExitCode(err error, code int) bool {
	var ec localnet.ExitCodeError
	if !errors.As(err, &ec) {
		return false
	}
	return int(ec) == code
}

// fakeReplayLedger satisfies txReplayLedger for unit tests. Each
// public field controls one method's behaviour: a nil err returns the
// resp verbatim; a non-nil err is bubbled up.
type fakeReplayLedger struct {
	byIDResp     *lapiv2.GetUpdateResponse
	byIDErr      error
	byOffsetResp *lapiv2.GetUpdateResponse
	byOffsetErr  error
	lastByID     *lapiv2.GetUpdateByIdRequest
	lastByOffset *lapiv2.GetUpdateByOffsetRequest
}

func (f *fakeReplayLedger) UpdateById(_ context.Context, req *lapiv2.GetUpdateByIdRequest) (*lapiv2.GetUpdateResponse, error) {
	f.lastByID = req
	return f.byIDResp, f.byIDErr
}

func (f *fakeReplayLedger) UpdateByOffset(_ context.Context, req *lapiv2.GetUpdateByOffsetRequest) (*lapiv2.GetUpdateResponse, error) {
	f.lastByOffset = req
	return f.byOffsetResp, f.byOffsetErr
}

// withFakeReplayLedger swaps the dial seam for the duration of a test.
// Restoring on cleanup keeps tests safely parallelisable-with-themselves
// (they still must not run with -parallel because the seam is package-level).
func withFakeReplayLedger(t *testing.T, fake txReplayLedger) {
	t.Helper()
	prev := dialTxReplayLedgerFn
	dialTxReplayLedgerFn = func(_ context.Context, _, _, _, _ string) (txReplayLedger, func(), error) {
		return fake, func() {}, nil
	}
	t.Cleanup(func() { dialTxReplayLedgerFn = prev })
}

func sampleTx() *lapiv2.Transaction {
	return &lapiv2.Transaction{
		UpdateId:    "tx-abc-123",
		Offset:      42,
		WorkflowId:  "wf-1",
		EffectiveAt: timestamppb.Now(),
		Events: []*lapiv2.Event{
			{
				Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
					NodeId:      0,
					ContractId:  "00cid-create",
					TemplateId:  &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
					Signatories: []string{"alice"},
				}},
			},
			{
				Event: &lapiv2.Event_Exercised{Exercised: &lapiv2.ExercisedEvent{
					NodeId:        1,
					ContractId:    "00cid-exercise",
					TemplateId:    &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
					Choice:        "Transfer",
					ActingParties: []string{"alice"},
					Consuming:     true,
				}},
			},
			{
				Event: &lapiv2.Event_Archived{Archived: &lapiv2.ArchivedEvent{
					NodeId:     2,
					ContractId: "00cid-archive",
					TemplateId: &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				}},
			},
		},
	}
}

// runReplay executes the buildTxReplay command with the given args and
// returns (stdout, stderr, runErr). The fake is wired via the seam.
func runReplay(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := buildTxReplay()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestTxReplay_RequiresIDOrOffset(t *testing.T) {
	// No fake needed — flag validation happens before dial.
	_, stderr, err := runReplay(t, "--endpoint", "localhost:1")
	if err == nil {
		t.Fatal("expected error when neither --id nor --offset is set")
	}
	if !isExitCode(err, localnet.ExitUserError) {
		t.Errorf("err = %v, want ExitUserError", err)
	}
	if !strings.Contains(stderr, "one of --id or --offset") {
		t.Errorf("stderr missing guidance: %q", stderr)
	}
}

func TestTxReplay_RejectsBothIDAndOffset(t *testing.T) {
	_, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--id", "tx-1",
		"--offset", "10",
	)
	if err == nil {
		t.Fatal("expected error when both --id and --offset are set")
	}
	if !isExitCode(err, localnet.ExitUserError) {
		t.Errorf("err = %v, want ExitUserError", err)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Errorf("stderr missing guidance: %q", stderr)
	}
}

func TestTxReplay_HappyPath_TextByID(t *testing.T) {
	fake := &fakeReplayLedger{
		byIDResp: &lapiv2.GetUpdateResponse{
			Update: &lapiv2.GetUpdateResponse_Transaction{Transaction: sampleTx()},
		},
	}
	withFakeReplayLedger(t, fake)

	stdout, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--id", "tx-abc-123",
		"--party", "alice",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v (stderr=%q)", err, stderr)
	}
	if fake.lastByID == nil || fake.lastByID.UpdateId != "tx-abc-123" {
		t.Errorf("UpdateById not called with the right id; got %+v", fake.lastByID)
	}
	// Tree shape is what makes replay useful (exercised choices visible).
	if fake.lastByID.UpdateFormat == nil ||
		fake.lastByID.UpdateFormat.IncludeTransactions.TransactionShape !=
			lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS {
		t.Errorf("expected LEDGER_EFFECTS shape, got %v",
			fake.lastByID.UpdateFormat.GetIncludeTransactions().GetTransactionShape())
	}
	// Text output mentions each event kind.
	for _, want := range []string{"created", "exercised", "archived", "Transfer"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n---\n%s", want, stdout)
		}
	}
}

func TestTxReplay_HappyPath_JSONByOffset(t *testing.T) {
	fake := &fakeReplayLedger{
		byOffsetResp: &lapiv2.GetUpdateResponse{
			Update: &lapiv2.GetUpdateResponse_Transaction{Transaction: sampleTx()},
		},
	}
	withFakeReplayLedger(t, fake)

	stdout, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--offset", "42",
		"--format", "json",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v (stderr=%q)", err, stderr)
	}
	if fake.lastByOffset == nil || fake.lastByOffset.Offset != 42 {
		t.Errorf("UpdateByOffset not called with --offset 42; got %+v", fake.lastByOffset)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	if got["update_id"] != "tx-abc-123" {
		t.Errorf("update_id = %v, want tx-abc-123", got["update_id"])
	}
	if got["schema_version"].(float64) != float64(contractsTxSchemaVersion) {
		t.Errorf("schema_version = %v, want %d", got["schema_version"], contractsTxSchemaVersion)
	}
	events, ok := got["events"].([]any)
	if !ok || len(events) != 3 {
		t.Fatalf("events = %v, want 3 entries", got["events"])
	}
}

func TestTxReplay_LedgerErrorIsRuntimeFailure(t *testing.T) {
	fake := &fakeReplayLedger{
		byIDErr: errors.New("rpc: NOT_FOUND"),
	}
	withFakeReplayLedger(t, fake)

	_, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--id", "missing-tx",
	)
	if err == nil {
		t.Fatal("expected runtime failure")
	}
	if !isExitCode(err, localnet.ExitRuntimeFailure) {
		t.Errorf("err = %v, want ExitRuntimeFailure", err)
	}
	if !strings.Contains(stderr, "NOT_FOUND") {
		t.Errorf("stderr should surface the underlying error: %q", stderr)
	}
}

func TestTxReplay_NilTransactionIsUserError(t *testing.T) {
	// Participant returned a GetUpdateResponse with no Transaction
	// (e.g. the offset pointed at a reassignment / topology event, or
	// the response was empty). Caller asked for a transaction;
	// telling them "no transaction at offset" is the right shape.
	fake := &fakeReplayLedger{
		byOffsetResp: &lapiv2.GetUpdateResponse{},
	}
	withFakeReplayLedger(t, fake)

	_, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--offset", "99",
	)
	if err == nil {
		t.Fatal("expected user error for empty response")
	}
	if !isExitCode(err, localnet.ExitUserError) {
		t.Errorf("err = %v, want ExitUserError", err)
	}
	if !strings.Contains(stderr, "no transaction") {
		t.Errorf("stderr missing guidance: %q", stderr)
	}
}

func TestTxReplay_NegativeOffsetRejected(t *testing.T) {
	_, stderr, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--offset", "-1",
	)
	if err == nil {
		t.Fatal("expected user error for negative offset")
	}
	if !isExitCode(err, localnet.ExitUserError) {
		t.Errorf("err = %v, want ExitUserError", err)
	}
	if !strings.Contains(stderr, "--offset must be >= 0") {
		t.Errorf("stderr missing guidance: %q", stderr)
	}
}

func TestTxReplay_InvalidFormatRejected(t *testing.T) {
	_, _, err := runReplay(t,
		"--endpoint", "localhost:1",
		"--id", "tx-1",
		"--format", "yaml",
	)
	if err == nil {
		t.Fatal("expected user error for unknown --format")
	}
}
