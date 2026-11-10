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

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// contractsTxSchemaVersion is the wire-stable schema version for
// the JSON output of `contracts ls`, `contracts watch`, `tx ls`,
// `tx replay`. Bumped only on breaking shape changes (renames,
// field removals). Adding a field is non-breaking. Mirrors the
// SchemaVersion constant in internal/api/types for HTTP shapes.
const contractsTxSchemaVersion = 1

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
		role      string
		parties   []string
		templates []string
		format    string
		token     string
	)
	cmd := &cobra.Command{
		Use:           "ls",
		Short:         "Snapshot the participant's current Active Contract Set",
		Long:          "Calls StateService.GetActiveContracts at the participant's current ledger end and prints one row per contract. `--party` filters to contracts visible to that party; pass multiple times. `--template` selects by template: use a package-name reference `#pkg-name:Module:Entity` (the LF-v2 form a Splice 0.6.x participant resolves by name) or an exact `<package-id>:Module:Entity` pin.",
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
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token, role)
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
			effParties, err := resolveDefaultParties(cmd.Context(), cmd.ErrOrStderr(), client, parties)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			req := ledger.ActiveContractsRequest{
				ActiveAtOffset: end.Offset,
				EventFormat:    buildEventFormat(effParties, templates, true),
			}
			stream, err := client.ActiveContracts(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ACS request failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderACSStream(cmd.OutOrStdout(), instance, effParties, templates, end.Offset, format, stream)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from (default: the only registered instance)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (overrides registry auto-discovery)")
	cmd.Flags().StringVar(&role, "role", "app-provider", "Participant role to read from: sv, app-provider, or app-user (default: app-provider)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter; repeat or comma-separate for multi-party. Omit to project through the JWT's own parties.")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"#pkg-name:Module:Entity\" (LF-v2 package-name reference) or \"<pkg-id>:Module:Entity\" (exact pin). Repeat for multiple.")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT (default: resolved per-role from the registry)")
	return cmd
}

func buildContractsWatch() *cobra.Command {
	var (
		instance  string
		endpoint  string
		role      string
		parties   []string
		templates []string
		format    string
		token     string
		limit     int
	)
	cmd := &cobra.Command{
		Use:           "watch",
		Short:         "Stream ACS changes from the participant's ledger end",
		Long:          "Subscribes to UpdateService.GetUpdates from the current ledger end and prints each create/archive event as it arrives. Ctrl-C to stop. --limit caps the total count (0 = unbounded).",
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
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token, role)
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
			effParties, err := resolveDefaultParties(cmd.Context(), cmd.ErrOrStderr(), client, parties)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			req := ledger.UpdatesRequest{
				BeginExclusive: end.Offset,
				UpdateFormat:   buildUpdateFormat(effParties, templates, true),
			}
			stream, err := client.Updates(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "update stream failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderUpdateStream(cmd.OutOrStdout(), instance, effParties, format, stream, limit)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from (default: the only registered instance)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (overrides registry auto-discovery)")
	cmd.Flags().StringVar(&role, "role", "app-provider", "Participant role to read from: sv, app-provider, or app-user (default: app-provider)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable). Omit to project through the JWT's own parties.")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"#pkg-name:Module:Entity\" (LF-v2 package-name reference) or \"<pkg-id>:Module:Entity\" (exact pin). Repeatable.")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json (NDJSON in stream mode)")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT (default: resolved per-role from the registry)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Stop after N updates (0 = unbounded; Ctrl-C also works)")
	return cmd
}

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
		role      string
		parties   []string
		templates []string
		format    string
		token     string
		limit     int
		fromOff   int64
		toOff     int64
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List recent transactions",
		Long: "Calls UpdateService.GetUpdates over a bounded window and prints the newest --limit transactions. " +
			"By default it scans a generous recent offset window so filtered queries (--party / --template) still " +
			"find matches even though offsets are participant-global (topology events, checkpoints and other " +
			"parties' transactions consume offsets between your matches). Use --from / --to for an explicit offset range.",
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
			client, cleanup, err := dialLedger(cmd.Context(), instance, endpoint, token, role)
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
			fromSet := cmd.Flags().Changed("from")
			toSet := cmd.Flags().Changed("to")
			begin, endIncl, err := resolveOffsetWindow(fromOff, toOff, fromSet, toSet, end.Offset)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			effParties, err := resolveDefaultParties(cmd.Context(), cmd.ErrOrStderr(), client, parties)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			req := ledger.UpdatesRequest{
				BeginExclusive: begin,
				EndInclusive:   endIncl,
				UpdateFormat:   buildUpdateFormat(effParties, templates, true),
			}
			stream, err := client.Updates(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "update stream failed:", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return renderUpdateList(cmd.OutOrStdout(), instance, effParties, format, stream, limit, begin, *endIncl)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from (default: the only registered instance)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (overrides registry auto-discovery)")
	cmd.Flags().StringVar(&role, "role", "app-provider", "Participant role to read from: sv, app-provider, or app-user (default: app-provider)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable). Omit to project through the JWT's own parties.")
	cmd.Flags().StringSliceVar(&templates, "template", nil, "Template filter — \"#pkg-name:Module:Entity\" (LF-v2 package-name reference) or \"<pkg-id>:Module:Entity\" (exact pin). Repeatable.")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT (default: resolved per-role from the registry)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max transactions to return (newest first)")
	cmd.Flags().Int64Var(&fromOff, "from", 0, "Begin offset (exclusive). Unset = a generous recent window; pass 0 to read from genesis.")
	cmd.Flags().Int64Var(&toOff, "to", 0, "End offset (inclusive). Unset = current ledger end.")
	return cmd
}

type txReplayLedger interface {
	UpdateById(ctx context.Context, req *lapiv2.GetUpdateByIdRequest) (*lapiv2.GetUpdateResponse, error)
	UpdateByOffset(ctx context.Context, req *lapiv2.GetUpdateByOffsetRequest) (*lapiv2.GetUpdateResponse, error)
}

var dialTxReplayLedgerFn = func(ctx context.Context, instance, endpoint, token, role string) (txReplayLedger, func(), error) {
	c, cleanup, err := dialLedger(ctx, instance, endpoint, token, role)
	if err != nil {
		return nil, cleanup, err
	}
	return c, cleanup, nil
}

func buildTxReplay() *cobra.Command {
	var (
		instance string
		endpoint string
		role     string
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
			client, cleanup, err := dialTxReplayLedgerFn(cmd.Context(), instance, endpoint, token, role)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer cleanup()

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
	cmd.Flags().StringVar(&instance, "name", "", "Instance to read from (default: the only registered instance)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint host:port (overrides registry auto-discovery)")
	cmd.Flags().StringVar(&role, "role", "app-provider", "Participant role to read from: sv, app-provider, or app-user (default: app-provider)")
	cmd.Flags().StringSliceVar(&parties, "party", nil, "Party ID filter (repeatable)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&token, "token", "", "Bearer JWT (default: resolved per-role from the registry)")
	cmd.Flags().StringVar(&updateID, "id", "", "Transaction (update) ID to fetch — mutually exclusive with --offset")
	cmd.Flags().Int64Var(&offset, "offset", 0, "Ledger offset to fetch — mutually exclusive with --id")
	return cmd
}

func renderTxReplay(out io.Writer, instance string, parties []string, format string, txn *lapiv2.Transaction) error {
	summaries := ledger.ProjectReplayEvents(txn)
	rows := make([]apitypes.TxReplayEvent, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, apitypes.TxReplayEvent{
			Kind:          string(s.Kind),
			NodeID:        s.NodeID,
			ContractID:    s.ContractID,
			TemplateID:    s.TemplateID,
			Choice:        s.Choice,
			ActingParties: s.ActingParties,
			Consuming:     s.Consuming,
			Signatories:   s.Signatories,
			Observers:     s.Observers,
		})
	}
	if format == "json" {
		resp := apitypes.TxReplayResponse{
			SchemaVersion: contractsTxSchemaVersion,
			Instance:      instance,
			Parties:       parties,
			UpdateID:      txn.UpdateId,
			Offset:        txn.Offset,
			WorkflowID:    txn.WorkflowId,
			EventCount:    len(rows),
			Events:        rows,
		}
		if txn.EffectiveAt != nil {
			resp.EffectiveAt = txn.EffectiveAt.AsTime().Format(time.RFC3339)
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	return renderTxReplayText(out, instance, parties, txn, rows)
}

func renderTxReplayText(out io.Writer, instance string, parties []string, txn *lapiv2.Transaction, rows []apitypes.TxReplayEvent) error {
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

func identString(id *lapiv2.Identifier) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", id.PackageId, id.ModuleName, id.EntityName)
}

// defaultTxWindowSpan is how many offsets back from the end `tx ls`
// scans when the user gives neither --from nor --to. It is
// DECOUPLED from --limit on purpose: Canton offsets are
// participant-global, so a filtered query (--party / --template)
// matches only a sparse subset of the offsets in the window — a
// window sized by --limit would often return zero matches with no
// indication anything was missed. A generous fixed span finds the
// recent matches; --limit then caps the rows we keep. The span is
// bounded so we never rescan the whole ledger on a long-lived
// LocalNet.
const defaultTxWindowSpan = 10_000

func resolveOffsetWindow(fromOff, toOff int64, fromSet, toSet bool, ledgerEnd int64) (int64, *int64, error) {
	end := ledgerEnd
	if toSet {
		end = toOff
	}
	var begin int64
	if fromSet {
		begin = fromOff
	} else {
		begin = end - defaultTxWindowSpan
		if begin < 0 {
			begin = 0
		}
	}
	if begin > end {
		return 0, nil, fmt.Errorf("--from %d must be <= --to %d", begin, end)
	}
	return begin, &end, nil
}

func dialLedger(ctx context.Context, instance, endpoint, token, role string) (*ledger.Client, func(), error) {
	if endpoint == "" {
		resolved, err := localnet.ResolveLedgerEndpoint(instance, role)
		if err != nil {
			return nil, func() {}, err
		}
		endpoint = resolved.Endpoint
		if token == "" {
			token = resolved.Token
		}
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

// Why this matters: Splice LocalNet signs user-id JWTs by default. A
// bare FiltersForAnyParty wildcard is accepted by request validation
// but the participant then PermissionDenies the stream for a user-id
// token that lacks a wildcard-read claim — even when a --template is
// present. Resolving the user's concrete parties and filtering by-party
// (with the template filter applied per party) is the shape that
// actually returns data — the same reason the UI does it.
func resolveDefaultParties(
	ctx context.Context,
	errw io.Writer,
	client *ledger.Client,
	parties []string,
) ([]string, error) {
	if len(parties) > 0 {
		return parties, nil
	}
	resolved, err := client.ResolveActAndReadParties(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(errw, term.Dimc(
			"note: could not resolve the JWT's parties ("+err.Error()+
				"); querying with the wildcard claim"))
		return parties, nil
	}
	if len(resolved) == 0 {
		_, _ = fmt.Fprintln(errw, term.Dimc(
			"note: this JWT carries no act/read party rights; querying with the "+
				"wildcard claim (pass --party, or grant rights via UserManagementService)"))
		return parties, nil
	}
	return resolved, nil
}

func renderACSStream(
	out io.Writer,
	instance string,
	parties, templates []string,
	offset int64,
	format string,
	stream <-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse],
) error {
	rows := []apitypes.ContractRow{}
	for item := range stream {
		if item.Err != nil {
			return item.Err
		}
		ac := item.Value.GetActiveContract()
		if ac == nil || ac.CreatedEvent == nil {
			continue
		}
		ev := ac.CreatedEvent
		sigs := ev.GetSignatories()
		if sigs == nil {
			sigs = []string{}
		}
		obs := ev.GetObservers()
		if obs == nil {
			obs = []string{}
		}
		row := apitypes.ContractRow{
			ContractID:  ev.ContractId,
			TemplateID:  identString(ev.TemplateId),
			Signatories: sigs,
			Observers:   obs,
			PackageName: ev.GetPackageName(),
		}
		if ev.CreatedAt != nil {
			row.CreatedAt = ev.CreatedAt.AsTime().Format(time.RFC3339)
		}
		if ev.CreateArguments != nil {
			row.Payload = ledger.RecordToMap(ev.CreateArguments)
		}
		rows = append(rows, row)
	}
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(apitypes.ContractsListResponse{
			SchemaVersion: contractsTxSchemaVersion,
			Instance:      instance,
			Parties:       parties,
			LedgerEnd:     offset,
			Contracts:     rows,
		})
	}
	return renderACSText(out, instance, parties, offset, rows)
}

func renderACSText(out io.Writer, instance string, parties []string, offset int64, rows []apitypes.ContractRow) error {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, term.Dimc("no active contracts"))
		return nil
	}
	cols := []term.Column{
		{Label: "contract id", Width: 24},
		{Label: "template", Width: 0},
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

type updateEventRow struct {
	Kind       string   `json:"kind"`
	ContractID string   `json:"contract_id,omitempty"`
	Template   string   `json:"template,omitempty"`
	Choice     string   `json:"choice,omitempty"`
	Witnesses  []string `json:"witnesses,omitempty"`
}

func eventRowsFrom(txn *lapiv2.Transaction) []updateEventRow {
	summaries := ledger.ProjectTransactionEvents(txn)
	rows := make([]updateEventRow, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, updateEventRow{
			Kind:       string(s.Kind),
			ContractID: s.ContractID,
			Template:   s.TemplateID,
			Choice:     s.Choice,
			Witnesses:  s.Witnesses,
		})
	}
	return rows
}

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
		events := eventRowsFrom(txn)
		if format == "json" {
			_ = jsonEnc.Encode(map[string]any{
				"schema_version": contractsTxSchemaVersion,
				"update_id":      txn.UpdateId,
				"offset":         txn.Offset,
				"effective_at":   txn.EffectiveAt.AsTime(),
				"workflow_id":    txn.WorkflowId,
				"event_count":    len(events),
				"events":         events,
			})
		} else {
			printTxnText(out, txn, events)
		}
		count++
		if limit > 0 && count >= limit {
			return nil
		}
	}
	return nil
}

func printTxnText(out io.Writer, txn *lapiv2.Transaction, events []updateEventRow) {
	_, _ = fmt.Fprintf(out, "%s  offset=%d  events=%d  %s\n",
		term.Brandc(truncTail(txn.UpdateId, 16)),
		txn.Offset, len(events),
		term.Dimc(txn.EffectiveAt.AsTime().Format(time.RFC3339)))
	for _, e := range events {
		detail := ""
		if e.Choice != "" {
			detail = " " + e.Choice
		}
		_, _ = fmt.Fprintf(out, "    %-9s %s  %s%s\n",
			e.Kind,
			truncMiddle(e.ContractID, 24),
			term.Dimc(truncTail(e.Template, 48)),
			detail)
	}
}

func renderUpdateList(
	out io.Writer,
	instance string,
	parties []string,
	format string,
	stream <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse],
	limit int,
	beginExclusive, endInclusive int64,
) error {
	type txnRecord struct {
		txn    *lapiv2.Transaction
		events []updateEventRow
	}
	var ring []txnRecord
	ringCap := limit
	if ringCap > 0 {
		ring = make([]txnRecord, 0, ringCap)
	}
	head, count := 0, 0
	var buf []txnRecord
	if ringCap > 0 {
		buf = make([]txnRecord, ringCap)
	}
	for item := range stream {
		if item.Err != nil {
			return item.Err
		}
		txn := item.Value.GetTransaction()
		if txn == nil {
			continue
		}
		rec := txnRecord{txn: txn, events: eventRowsFrom(txn)}
		if ringCap <= 0 {
			ring = append(ring, rec)
			continue
		}
		buf[head] = rec
		head = (head + 1) % ringCap
		if count < ringCap {
			count++
		}
	}
	if ringCap > 0 {
		ring = ring[:0]
		start := (head - count + ringCap) % ringCap
		for i := 0; i < count; i++ {
			ring = append(ring, buf[(start+i)%ringCap])
		}
	}
	for i, j := 0, len(ring)-1; i < j; i, j = i+1, j-1 {
		ring[i], ring[j] = ring[j], ring[i]
	}

	if format == "json" {
		txns := make([]map[string]any, 0, len(ring))
		for _, r := range ring {
			txns = append(txns, map[string]any{
				"update_id":    r.txn.UpdateId,
				"offset":       r.txn.Offset,
				"effective_at": r.txn.EffectiveAt.AsTime(),
				"workflow_id":  r.txn.WorkflowId,
				"event_count":  len(r.events),
				"events":       r.events,
			})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version":  contractsTxSchemaVersion,
			"instance":        instance,
			"parties":         parties,
			"scanned_from":    beginExclusive,
			"scanned_to":      endInclusive,
			"transaction_cnt": len(ring),
			"transactions":    txns,
		})
	}

	if len(ring) == 0 {
		_, _ = fmt.Fprintln(out, term.Dimc(fmt.Sprintf(
			"no matching transactions in scanned offsets (%d, %d]",
			beginExclusive, endInclusive)))
		return nil
	}
	for _, r := range ring {
		printTxnText(out, r.txn, r.events)
	}
	_, _ = fmt.Fprintln(out, term.Dimc(fmt.Sprintf(
		"— %d transaction(s); scanned offsets (%d, %d]",
		len(ring), beginExclusive, endInclusive)))
	return nil
}

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
