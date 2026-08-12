package token

import (
	"context"
	"strings"
	"testing"
)

func TestRunTransferPlanValidatesBeforeDial(t *testing.T) {
	tests := []struct {
		name string
		opts TransferOptions
		want string
	}{
		{
			name: "invalid amount",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", From: "alice::fingerprint", Amount: "not a number"},
			want: "not a valid decimal",
		},
		{
			name: "missing sender",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", Amount: "1"},
			want: "sender party is required",
		},
		{
			name: "missing instance",
			opts: TransferOptions{Instrument: "TOK", From: "alice::fingerprint", Amount: "1"},
			want: "instance is required",
		},
		{
			name: "missing instrument",
			opts: TransferOptions{Instance: "demo", From: "alice::fingerprint", Amount: "1"},
			want: "instrument is required",
		},
		{
			name: "missing amount",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", From: "alice::fingerprint"},
			want: "amount is required",
		},
		{
			name: "invalid sender",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", From: "not a party", Amount: "1"},
			want: "not a valid party id",
		},
		{
			name: "zero amount",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", From: "alice::fingerprint", Amount: "0"},
			want: "must be greater than zero",
		},
		{
			name: "valid input without endpoint",
			opts: TransferOptions{Instance: "demo", Instrument: "TOK", From: "alice::fingerprint", Amount: "1"},
			want: ErrUnresolvedLedgerEndpoint.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunTransferPlan(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunTransferPlan() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
