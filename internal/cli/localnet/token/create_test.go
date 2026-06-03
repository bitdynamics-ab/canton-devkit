package token

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	localtoken "github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestRunWizard_HappyPath drives the six-field interactive wizard from a
// scripted reader and asserts every field lands in CreateOptions and the
// "y" confirmation returns cleanly.
func TestRunWizard_HappyPath(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		"Retail Token", // name
		"RTK",          // symbol
		"6",            // decimals
		"1000000",      // initial supply
		"alice",        // issuer
		"y",            // confirm
	}, "\n") + "\n")

	var out bytes.Buffer
	opts := &localtoken.CreateOptions{}
	if err := runWizard(in, &out, opts); err != nil {
		t.Fatalf("runWizard: %v", err)
	}
	if opts.Name != "Retail Token" || opts.Symbol != "RTK" || opts.Decimals != 6 ||
		opts.InitialSupply != "1000000" || opts.Issuer != "alice" {
		t.Errorf("unexpected opts: %+v", opts)
	}
}

func TestRunWizard_AbortOnNo(t *testing.T) {
	in := strings.NewReader("Retail Token\nRTK\n6\n1000000\nalice\nn\n")
	err := runWizard(in, &bytes.Buffer{}, &localtoken.CreateOptions{})
	if !errors.Is(err, errUserAbort) {
		t.Fatalf("want errUserAbort, got %v", err)
	}
}

func TestRunWizard_RejectsBadDecimals(t *testing.T) {
	in := strings.NewReader("Retail Token\nRTK\nabc\n")
	err := runWizard(in, &bytes.Buffer{}, &localtoken.CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "decimals must be an integer") {
		t.Fatalf("want decimals error, got %v", err)
	}
}

func TestRunWizard_RequiresNonEmptyField(t *testing.T) {
	in := strings.NewReader("Retail Token\n\n") // blank symbol
	err := runWizard(in, &bytes.Buffer{}, &localtoken.CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "is required") {
		t.Fatalf("want required-field error, got %v", err)
	}
}

// TestRunWizard_NonTTYRefusal: a non-interactive stdin (a regular file is
// not a char device) must be refused with a pointer at --non-interactive,
// rather than silently consuming EOF.
func TestRunWizard_NonTTYRefusal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "wizard-stdin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("Retail Token\nRTK\n6\n1000000\nalice\ny\n")
	_, _ = f.Seek(0, 0)

	err = runWizard(f, &bytes.Buffer{}, &localtoken.CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("want non-tty refusal, got %v", err)
	}
}
