package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"github.com/spf13/cobra"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// contractsTxSchemaVersion is the wire-stable schema version for
// the JSON output of `contracts ls`, `contracts watch`, `tx ls`,
// `tx replay`. Bumped only on breaking shape changes (renames,
// field removals). Adding a field is non-breaking. Mirrors the
// SchemaVersion constant in internal/api/types for HTTP shapes.
const contractsTxSchemaVersion = 1

// buildContracts wires `dpm localnet contracts <verb>` — the CLI
// surface for the Daml LF v2 Ledger API, backed by the
// internal/canton/ledger client.
//
// Sub-verbs:
//
//	watch — stream Active Contract Set changes (StateService.GetActiveContracts
//	        + UpdateService.GetUpdates for ongoing deltas).
//	ls — paginated snapshot of the current ACS (StateService.GetActiveContracts).
//
// Both default to text output (term.Table); --format json emits a
// stable JSON shape with schema_version suitable for jq / CI
// assertions.
//
// Endpoint discovery: LocalNet's participant gRPC ports are
// network-internal (not host-published) — pass --endpoint
// host:port explicitly.
func buildContracts() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "contracts",
		Short:         "Operate on the participant's Active Contract Set (ACS)",
		Long:          "Subcommands wrap the Daml LF v2 Ledger API's StateService for ACS reads. The same internal/canton/ledger package backs both the CLI and the Web UI surfaces.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(buildContractsLs())
	cmd.AddCommand(buildContractsWatch())
	return cmd
}

func buildContractsLs() *cobra.Command {
	var (
		instance  string
		endpoint  string
		parties   []string
		templates []string
		format    string
		token     string
	)
	cmd := &cobra.Command{
		Use:           "ls",
		Short:         "Snapshot the participant's current Active Contract Set",
		Long:          "Calls StateService.GetActiveContracts at the participant's current ledger end and prints one row per contract. `--party` filters to contracts visible to that party; pass multiple times. `--template` accepts Module:Entity or pkg:Module:Entity.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			if _, err := buildTemplateFilters(templates); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer cleanup()

			end, err := client.LedgerEnd(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ledger end query failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			req := ledger.ActiveContractsRequest{
				ActiveAtOffset: end.Offset,
				EventFormat:    buildEventFormat(parties, templates, true),
			}
			stream, err := client.ActiveContracts(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ACS request failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderACSStream(cmd.OutOrStdout(), instance, parties, templates, end.Offset, format, stream)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from (default: the only registered instance)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (required while auto-discovery is pending)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter; repeat or comma-separate for multi-party. Omit for the JWT's wildcard claim.")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"Module:Entity\" or \"pkg:Module:Entity\". Repeat for multiple.")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT (default: empty — use only with unsafe-signed LocalNet tokens)")
	return cmd
}

func buildContractsWatch() *cobra.Command {
	var (
		instance  string
		endpoint  string
		parties   []string
		templates []string
		format    string
		token     string
		limit     int
	)
	cmd := &cobra.Command{
		Use:           "watch",
		Short:         "Stream ACS changes from the participant's ledger end",
		Long:          "Subscribes to UpdateService.GetUpdates from the current ledger end and prints each transaction/reassignment as it arrives. Ctrl-C to stop. --limit caps the total count (0 = unbounded).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			if _, err := buildTemplateFilters(templates); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer cleanup()

			end, err := client.LedgerEnd(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ledger end query failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			req := ledger.UpdatesRequest{
				BeginExclusive: end.Offset,
				UpdateFormat:   buildUpdateFormat(parties, templates, true),
			}
			stream, err := client.Updates(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "update stream failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderUpdateStream(cmd.OutOrStdout(), instance, parties, format, stream, limit)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (required)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable)")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"Module:Entity\" or \"pkg:Module:Entity\"")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json (NDJSON in stream mode)")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT")
	cmd.Flags().IntVar(&limit, "limit", 0, "Stop after N updates (0 = unbounded; Ctrl-C also works)")
	return cmd
}

// buildTx wires `dpm localnet tx <verb>` — transaction ops sister
// command to `contracts`. Same ledger client, different lens.
//
// Sub-verbs:
//
//	ls     — bounded list of recent transactions (UpdateService.GetUpdates).
//	replay — fetch a single transaction by --id or --offset and render its
//	         events in tree shape (UpdateService.GetUpdateById /
//	         GetUpdateByOffset).
func buildTx() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "tx",
		Short:         "Inspect transactions on the participant ledger",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(buildTxLs())
	cmd.AddCommand(buildTxReplay())
	return cmd
}

func buildTxLs() *cobra.Command {
	var (
		instance  string
		endpoint  string
		parties   []string
		templates []string
		format    string
		token     string
		limit     int
		fromOff   int64
		toOff     int64
	)
	cmd := &cobra.Command{
		Use:           "ls",
		Short:         "List recent transactions",
		Long:          "Calls UpdateService.GetUpdates over a bounded window and prints one row per transaction. Window defaults to (end - limit, end]; use --from / --to for explicit offset ranges.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			if _, err := buildTemplateFilters(templates); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer cleanup()
			end, err := client.LedgerEnd(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ledger end query failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			begin, endIncl, err := resolveOffsetWindow(fromOff, toOff, limit, end.Offset)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			req := ledger.UpdatesRequest{
				BeginExclusive: begin,
				EndInclusive:   endIncl,
				UpdateFormat:   buildUpdateFormat(parties, templates, true),
			}
			stream, err := client.Updates(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "update stream failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderUpdateStream(cmd.OutOrStdout(), instance, parties, format, stream, limit)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (required)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable)")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"Module:Entity\" or \"pkg:Module:Entity\"")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max transactions to return when --from/--to aren't set")
	cmd.Flags().Int64Var(&fromOff, "from", 0, "Begin offset (exclusive) — 0 means use --limit from end")
	cmd.Flags().Int64Var(&toOff, "to", 0, "End offset (inclusive) — 0 means current ledger end")
	return cmd
}

// txReplayLedger is the narrow interface `tx replay` needs from the
// ledger client. Mirrors the token package's LedgerClient seam: keeps
// the production path on *ledger.Client while letting unit tests inject
// a fake without dialing a real participant. Add methods only as new
// orchestration paths come under test.
type txReplayLedger interface {
	UpdateById(ctx context.Context, req *lapiv2.GetUpdateByIdRequest) (*lapiv2.GetUpdateResponse, error)
	UpdateByOffset(ctx context.Context, req *lapiv2.GetUpdateByOffsetRequest) (*lapiv2.GetUpdateResponse, error)
}

// dialTxReplayLedgerFn is the test seam buildTxReplay's RunE dials
// through. Defaults to upcasting dialLedger's concrete *ledger.Client
// to txReplayLedger. Tests reassign to return a fake.
var dialTxReplayLedgerFn = func(ctx context.Context, instance, endpoint, token string) (txReplayLedger, func(), error) {
	c, cleanup, err := dialLedger(ctx, instance, endpoint, token)
	if err != nil {
		return nil, cleanup, err
	}
	return c, cleanup, nil
}

func buildTxReplay() *cobra.Command {
	var (
		instance string
		endpoint string
		parties  []string
		format   string
		token    string
		updateID string
		offset   int64
	)
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Fetch and render a single transaction by id or offset",
		Long: "Calls UpdateService.GetUpdateById (when --id is set) or " +
			"GetUpdateByOffset (when --offset is set) and prints the " +
			"transaction's events. Exactly one of --id or --offset is required. " +
			"--party filters the EventFormat to a per-party visibility " +
			"projection (same shape as tx ls / contracts watch).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			// Mutually exclusive: exactly one of --id / --offset.
			haveID := strings.TrimSpace(updateID) != ""
			haveOff := cmd.Flags().Changed("offset")
			if !haveID && !haveOff {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tx replay: one of --id or --offset is required")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if haveID && haveOff {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tx replay: --id and --offset are mutually exclusive")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if haveOff && offset < 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tx replay: --offset must be >= 0")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			client, cleanup, err := dialTxReplayLedgerFn(cmd.Context(), instance, endpoint, token)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer cleanup()

			// Replay uses the tree (LEDGER_EFFECTS) shape so users see
			// exercised choices, not just the ACS-delta projection. The
			// EventFormat carries the --party filter; nil templates means
			// "no template restriction".
			uf := &lapiv2.UpdateFormat{
				IncludeTransactions: &lapiv2.TransactionFormat{
					EventFormat:      buildEventFormat(parties, nil, true),
					TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS,
				},
			}
			var (
				resp *lapiv2.GetUpdateResponse
				rerr error
			)
			if haveID {
				resp, rerr = client.UpdateById(cmd.Context(), &lapiv2.GetUpdateByIdRequest{
					UpdateId:     updateID,
					UpdateFormat: uf,
				})
			} else {
				resp, rerr = client.UpdateByOffset(cmd.Context(), &lapiv2.GetUpdateByOffsetRequest{
					Offset:       offset,
					UpdateFormat: uf,
				})
			}
			if rerr != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tx replay failed:", rerr)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			if resp == nil || resp.GetTransaction() == nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tx replay: no transaction at the requested id/offset")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return renderTxReplay(cmd.OutOrStdout(), instance, parties, format, resp.GetTransaction())
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (required)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT")
	cmd.Flags().StringVar(&updateID, "id", "", "Transaction (update) ID to fetch — mutually exclusive with --offset")
	cmd.Flags().Int64Var(&offset, "offset", 0, "Ledger offset to fetch — mutually exclusive with --id")
	return cmd
}

// txEventRow is the per-event JSON/text shape for `tx replay`. Mirrors
// contractRow's shape contract so the CLI and the Web UI's
// transaction-detail drawer share one projection.
type txEventRow struct {
	Kind          string   `json:"kind"` // "created" | "exercised" | "archived"
	NodeID        int32    `json:"node_id"`
	ContractID    string   `json:"contract_id"`
	TemplateID    string   `json:"template_id,omitempty"`
	Choice        string   `json:"choice,omitempty"`         // exercised only
	ActingParties []string `json:"acting_parties,omitempty"` // exercised only
	Consuming     bool     `json:"consuming,omitempty"`      // exercised only
	Signatories   []string `json:"signatories,omitempty"`    // created only
	Observers     []string `json:"observers,omitempty"`      // created only
}

func renderTxReplay(out io.Writer, instance string, parties []string, format string, txn *lapiv2.Transaction) error {
	rows := make([]txEventRow, 0, len(txn.Events))
	for _, ev := range txn.Events {
		switch {
		case ev.GetCreated() != nil:
			ce := ev.GetCreated()
			rows = append(rows, txEventRow{
				Kind:        "created",
				NodeID:      ce.NodeId,
				ContractID:  ce.ContractId,
				TemplateID:  identString(ce.TemplateId),
				Signatories: ce.Signatories,
				Observers:   ce.Observers,
			})
		case ev.GetExercised() != nil:
			xe := ev.GetExercised()
			rows = append(rows, txEventRow{
				Kind:          "exercised",
				NodeID:        xe.NodeId,
				ContractID:    xe.ContractId,
				TemplateID:    identString(xe.TemplateId),
				Choice:        xe.Choice,
				ActingParties: xe.ActingParties,
				Consuming:     xe.Consuming,
			})
		case ev.GetArchived() != nil:
			ae := ev.GetArchived()
			rows = append(rows, txEventRow{
				Kind:       "archived",
				NodeID:     ae.NodeId,
				ContractID: ae.ContractId,
				TemplateID: identString(ae.TemplateId),
			})
		}
	}
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": contractsTxSchemaVersion,
			"instance":       instance,
			"parties":        parties,
			"update_id":      txn.UpdateId,
			"offset":         txn.Offset,
			"workflow_id":    txn.WorkflowId,
			"effective_at":   txn.EffectiveAt.AsTime(),
			"event_count":    len(rows),
			"events":         rows,
		})
	}
	return renderTxReplayText(out, instance, parties, txn, rows)
}

func renderTxReplayText(out io.Writer, instance string, parties []string, txn *lapiv2.Transaction, rows []txEventRow) error {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, term.Dimc("no events in this transaction"))
		return nil
	}
	cols := []term.Column{
		{Label: "node", Width: 4},
		{Label: "kind", Width: 9},
		{Label: "contract id", Width: 24},
		{Label: "template", Width: 0},
		{Label: "detail", Width: 0},
	}
	body := make([][]string, 0, len(rows))
	for _, r := range rows {
		detail := ""
		switch r.Kind {
		case "exercised":
			detail = r.Choice
			if r.Consuming {
				detail += " (consuming)"
			}
			if len(r.ActingParties) > 0 {
				detail += " by " + strings.Join(r.ActingParties, ",")
			}
		case "created":
			detail = strings.Join(r.Signatories, ",")
		}
		body = append(body, []string{
			fmt.Sprintf("%d", r.NodeID),
			r.Kind,
			truncMiddle(r.ContractID, 24),
			truncTail(r.TemplateID, 40),
			truncTail(detail, 40),
		})
	}
	header := "tx replay · " + instance
	if len(parties) > 0 {
		header += " · " + strings.Join(parties, ",")
	}
	right := fmt.Sprintf("%s @ offset %d · %d events",
		truncTail(txn.UpdateId, 16), txn.Offset, len(rows))
	tbl := term.Table(cols, body)
	_, _ = fmt.Fprintln(out, term.Section(header, right, tbl, 0))
	return nil
}

// identString stringifies a gRPC Identifier as pkg:Module:Entity (same
// shape contractRow.TemplateID uses).
func identString(id *lapiv2.Identifier) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", id.PackageId, id.ModuleName, id.EntityName)
}

// resolveOffsetWindow implements the --from / --to / --limit
// precedence. Explicit --from and --to win; if either is zero we
// substitute end/end-limit.
//
// Returns (beginExclusive, endInclusive, error). When --from >
// --to we fail loudly rather than silently producing an empty
// window.
func resolveOffsetWindow(fromOff, toOff int64, limit int, ledgerEnd int64) (int64, *int64, error) {
	end := toOff
	if end == 0 {
		end = ledgerEnd
	}
	begin := fromOff
	if fromOff == 0 {
		if limit > 0 {
			begin = end - int64(limit)
			if begin < 0 {
				begin = 0
			}
		}
	}
	if begin > end {
		return 0, nil, fmt.Errorf("--from %d must be <= --to %d", begin, end)
	}
	return begin, &end, nil
}

// dialLedger resolves the participant endpoint + dials the ledger
// client. Returns a (client, cleanup) pair so the caller can
// `defer cleanup()` to close the gRPC conn.
func dialLedger(ctx context.Context, instance, endpoint, token string) (*ledger.Client, func(), error) {
	if endpoint == "" {
		if instance != "" {
			if _, err := registry.Read(instance); err == nil {
				return nil, func() {}, fmt.Errorf(
					"participant gRPC endpoint not yet exposed for instance %q — "+
						"pass --endpoint host:port explicitly (host port not always exposed for v2 instances)",
					instance)
			}
		}
		return nil, func() {}, fmt.Errorf("--endpoint is required (e.g. --endpoint localhost:5001)")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	opts := ledger.DialOptions{
		Endpoint:  endpoint,
		PlainText: true,
	}
	if token != "" {
		opts.Token = ledger.StaticToken(token)
	}
	client, err := ledger.Dial(dialCtx, opts)
	if err != nil {
		return nil, func() {}, fmt.Errorf("dial ledger %s: %w", endpoint, err)
	}
	cleanup := func() { _ = client.Close() }
	return client, cleanup, nil
}

// contractRow is the per-contract JSON/text shape. Mirrors the
// Web UI handler's projection (handlers/contracts.go) so the CLI and
// browser surfaces always emit the same JSON shape. The field name is
// `template_id`, matching Canton/Daml convention and the gRPC
// Identifier message.
type contractRow struct {
	ContractID  string   `json:"contract_id"`
	TemplateID  string   `json:"template_id"`
	Signatories []string `json:"signatories,omitempty"`
	Observers   []string `json:"observers,omitempty"`
}

func renderACSStream(
	out io.Writer,
	instance string,
	parties, templates []string,
	offset int64,
	format string,
	stream <-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse],
) error {
	rows := []contractRow{}
	for item := range stream {
		if item.Err != nil {
			return item.Err
		}
		ac := item.Value.GetActiveContract()
		if ac == nil || ac.CreatedEvent == nil {
			continue
		}
		ev := ac.CreatedEvent
		row := contractRow{
			ContractID:  ev.ContractId,
			Signatories: ev.Signatories,
			Observers:   ev.Observers,
		}
		if ev.TemplateId != nil {
			row.TemplateID = fmt.Sprintf("%s:%s:%s",
				ev.TemplateId.PackageId,
				ev.TemplateId.ModuleName,
				ev.TemplateId.EntityName)
		}
		rows = append(rows, row)
	}
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": contractsTxSchemaVersion,
			"instance":       instance,
			"parties":        parties,
			"templates":      templates,
			"at_offset":      offset,
			"contracts":      rows,
			"contract_count": len(rows),
		})
	}
	return renderACSText(out, instance, parties, offset, rows)
}

// renderACSText delegates to term.Table so the column layout
// matches the rest of the CLI surface (status, list, container
// list).
func renderACSText(out io.Writer, instance string, parties []string, offset int64, rows []contractRow) error {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, term.Dimc("no active contracts"))
		return nil
	}
	cols := []term.Column{
		{Label: "contract id", Width: 24},
		{Label: "template", Width: 0}, // flex
		{Label: "signatories", Width: 0},
	}
	body := make([][]string, 0, len(rows))
	for _, r := range rows {
		body = append(body, []string{
			truncMiddle(r.ContractID, 24),
			truncTail(r.TemplateID, 60),
			strings.Join(r.Signatories, ","),
		})
	}
	header := "contracts · " + instance
	if len(parties) > 0 {
		header += " · " + strings.Join(parties, ",")
	}
	right := fmt.Sprintf("%d contracts @ offset %d", len(rows), offset)
	tbl := term.Table(cols, body)
	_, _ = fmt.Fprintln(out, term.Section(header, right, tbl, 0))
	return nil
}

// renderUpdateStream prints rows for the UpdateService stream.
// Limit caps the count (after which we cancel by returning).
// JSON mode is NDJSON so consumers can `dpm localnet contracts
// watch --format json | jq` stream-style.
func renderUpdateStream(
	out io.Writer,
	instance string,
	parties []string,
	format string,
	stream <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse],
	limit int,
) error {
	_ = instance
	_ = parties
	count := 0
	jsonEnc := json.NewEncoder(out)
	for item := range stream {
		if item.Err != nil {
			return item.Err
		}
		txn := item.Value.GetTransaction()
		if txn == nil {
			continue
		}
		if format == "json" {
			_ = jsonEnc.Encode(map[string]any{
				"schema_version": contractsTxSchemaVersion,
				"update_id":      txn.UpdateId,
				"offset":         txn.Offset,
				"effective_at":   txn.EffectiveAt.AsTime(),
				"workflow_id":    txn.WorkflowId,
				"event_count":    len(txn.Events),
			})
		} else {
			_, _ = fmt.Fprintf(out, "%s  offset=%d  events=%d  %s\n",
				term.Brandc(truncTail(txn.UpdateId, 16)),
				txn.Offset, len(txn.Events),
				term.Dimc(txn.EffectiveAt.AsTime().Format(time.RFC3339)))
		}
		count++
		if limit > 0 && count >= limit {
			return nil
		}
	}
	return nil
}

// truncTail / truncMiddle keep wide IDs from blowing column
// widths. truncTail appends "…"; truncMiddle elides the middle
// (preserves prefix + suffix — useful for contract IDs where the
// last chars distinguish entries with the same prefix).
func truncTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func truncMiddle(s string, n int) string {
	if len(s) <= n || n < 5 {
		return truncTail(s, n)
	}
	keep := (n - 1) / 2
	return s[:keep] + "…" + s[len(s)-(n-1-keep):]
}
