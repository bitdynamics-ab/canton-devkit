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
