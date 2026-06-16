package localnet

import (
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
)

// The party + template filter construction now lives in the neutral
// internal/canton/ledger package so the CLI and the Web UI Explorer
// handlers share one decoder and `--party`/`--template` behave
// identically on both surfaces. These thin wrappers preserve the
// package-local names the CLI commands and tests already call.

// buildEventFormat — see ledger.BuildEventFormat.
func buildEventFormat(parties, templates []string, verbose bool) *lapiv2.EventFormat {
	return ledger.BuildEventFormat(parties, templates, verbose)
}

// buildUpdateFormat wraps an EventFormat in an UpdateFormat configured
// for the ACS-delta flat-transaction shape (the default for `tx ls` /
// `contracts watch`). Tree shape (`tx replay`) builds its UpdateFormat
// inline with the LEDGER_EFFECTS shape.
func buildUpdateFormat(parties, templates []string, verbose bool) *lapiv2.UpdateFormat {
	return ledger.BuildUpdateFormat(parties, templates, verbose,
		lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA)
}

// buildTemplateFilters — see ledger.BuildTemplateFilters. Retained so
// the CLI commands can validate `--template` at the flag layer and
// emit an ExitUserError before dialing.
func buildTemplateFilters(templates []string) (*lapiv2.Filters, error) {
	return ledger.BuildTemplateFilters(templates)
}
