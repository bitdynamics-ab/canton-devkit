# Explorer Usage

The Explorer is the Web UI's window into a running LocalNet's
ledger. It reads the Active Contract Set (ACS) and recent
transactions from the participant's gRPC ledger API, so you can
see what's on the ledger without writing a script.

This guide covers what the Explorer can do today, the equivalent
CLI commands for scripted workflows, and which features are
planned but not yet shipped — so you can tell at a glance whether
the Explorer fits your task.

---

## 1. What you see

Open the Web UI (`canton-devkit localnet ui --name <n>` or whatever
port the bundled UI is running on) and switch to the **Explorer**
tab. The screen has three views, selectable from the toggle in the
top bar:

| View | What it shows |
|---|---|
| **Contracts** | The Active Contract Set as a live table — an initial snapshot kept current by a server-sent-events delta stream — with a left-hand facet sidebar (templates, parties, manual refresh) and a right-hand detail drawer. |
| **Transactions** | Recent ledger updates from the participant's `UpdateService`, with party / template / offset-range filters (mirroring the CLI `tx ls`). Each row expands inline to show its event tree (creates, archives, exercises) and can be **replayed** as a per-party visibility projection. |
| **Timeline** | A density strip and per-update glyph row. Hover a glyph to preview, click to pin a selection. Useful for "what happened in the last minute". |

All three views project through the **same instance** and **the
same role**, picked from the top-bar selector.

---

## 2. Choosing the instance and role

The top-bar **Projecting through** widget controls two things:

1. **Instance** — which LocalNet's participant the Explorer talks
   to. The selection comes from the global instance picker and
   sticks to the URL's `?instance=` query parameter, so it
   survives navigating between tabs and reloading the page.
2. **Role** — which Splice JWT to authenticate with. The choices
   are `app-user`, `app-provider`, and `sv`. Each role is a
   different user in the participant's user management; the
   contracts you see are the ones that role's user has rights to
   read.

Switching either control re-runs the ACS snapshot. There is no
client-side filtering across roles — you get a fresh view from the
participant each time.

If the Web UI shows **"No instance selected"**, create one from the
Dashboard tab or pass `?instance=<name>` in the URL.

---

## 3. Filtering

The Contracts view has three filter surfaces, all client-side
against the (live) snapshot already loaded:

- **Templates sidebar.** Click a template chip to restrict the
  table to that template. Click again to clear. Multiple chips
  combine as OR. Templates are rendered as `Module:Entity`; hover
  to see the fully-qualified `package_id:Module:Entity` form.
- **Parties sidebar.** Same pattern, but filters to contracts where
  the chosen party appears as signatory or observer.
- **Search box** (top-right of the table, focus with `/`). Free-text
  search across template name, contract ID, payload JSON, and the
  party lists. Useful when you know a substring of the contract ID
  or a value in the payload.

Filters compose: template chip AND party chip AND search needle
all have to match.

These are client-side facets over the loaded ACS. The
**Transactions** view, by contrast, filters **server-side** over the
participant's offset window — see §5 — so a transaction outside the
loaded row cap can still be found by narrowing the query.

A few keyboard shortcuts inside the Contracts view:

- `/` focuses the search box.
- `Esc` clears the selected row in the detail drawer.

---

## 4. Inspecting a contract

Click any row in the ACS table. The right-hand **detail drawer**
shows:

- **Template** in long form (`Module:Entity` from the template ID),
  plus the originating `package_name` if the participant reported
  one.
- **Contract ID** (full, monospace, copyable).
- **Payload** as pretty-printed JSON. Records, lists, optionals,
  primitives, parties, and contract IDs all render natively;
  variants/enums/maps fall back to a textual proto form (a typed
  decoder is planned).
- **Signatories** and **Observers** as separate lists.
- **Created** with the RFC 3339 timestamp the participant recorded
  and a human-readable "Xs/m/h/d ago".

The detail drawer is read-only — there is no "exercise choice" UI in
the Explorer. Exercising choices is a CLI / SDK action; see
`canton-devkit localnet token …` for the prebuilt CIP-0112 flows or
build a regular Daml/SDK client.

---

## 5. Transactions view

Switching to **Transactions** runs a recent-updates query against
the participant's `UpdateService`. Each row shows:

- **Kind** — `transaction`, `reassignment`, or `topology`.
- **Offset** — the participant's ledger offset.
- **Command ID / Update ID** — whichever is present.
- **Workflow ID** or synchronizer, when populated.
- **Record time** — `HH:MM:SS` of when the participant recorded
  the update.
- **Event count** — number of events in the update.

Click a row to expand its event tree (create / archive / exercise
nodes with template + contract ID).

### Filters

The filter bar above the table mirrors the CLI `tx ls` flags and is
applied **server-side** over the participant's offset window:

- **party** — comma-separate to project through specific parties.
  Omit to project through the role JWT's own parties.
- **template** — `Module:Entity` or `pkg:Module:Entity`,
  comma-separated for multiple.
- **from / to** — bound the scanned ledger-offset window (`from` is
  exclusive, `to` inclusive). Leave blank for a generous recent
  window.

Press **Apply** (or Enter in any field) to re-query; **Clear** resets
to the default window. The header shows the scanned offset range and
flags a **partial window** when the scan hit its cap before draining
the window — the rows are then the newest of a clipped scan.

### Replay (per-party visibility projection)

Each `transaction` row has a **replay** button. It opens a drawer
that re-fetches that transaction with the `LEDGER_EFFECTS` shape
(exercised choices, not just the ACS delta) projected through a party
set. The **visible to** selector lets you ask "what did party *P* see
in this transaction?" — the same id projected through different
parties yields different event sets. This is the Web UI counterpart of
`canton-devkit localnet tx replay --id <update-id>`.

The Transactions view pulls up to 200 recent updates by default.

---

## 6. Timeline view

The Timeline groups the same updates into 60 time buckets and
renders two strips:

- A **density strip** with bar height proportional to the bucket's
  update count.
- A **glyph row** with one coloured cell per update (green =
  transaction, blue = reassignment, purple = topology).

Hover a glyph to preview the update in the side panel; click to
pin the selection so you can read its event tree without keeping
the cursor over the strip. `Esc` clears the pinned selection.

The Timeline is the fastest way to answer "did anything just
happen?" and "where in the last few minutes was the spike?".

---

## 7. Snapshot vs. live

The **Contracts** view is live:

1. It first calls `StateService.GetActiveContracts` at the
   participant's current ledger end (the snapshot).
2. It then opens a server-sent-events stream
   (`GET .../contracts/stream`) resuming from the snapshot's
   `ledger_end`, applying create/archive deltas in place. The
   handoff is a single atomic offset boundary, so no event between
   the snapshot and the stream is missed.
3. A 30-second timer re-snapshots quietly to reconcile any drift
   (a suspended laptop, a dropped connection, a backend restart),
   and the **Refresh snapshot** button in the sidebar forces one
   immediately.

The stream-status pill in the top bar and the table sub-header
report the real connection state — `live`, `reconnecting`,
`truncated` (the backend capped the stream; reconciliation takes
over), or `idle`. The wording is honest: it tracks the stream, not
a hard-coded label.

The **Transactions** and **Timeline** views are still snapshots —
they call `UpdateService` for the most recent N updates. Re-apply
the filters (or switch tabs back) to pull a fresh window.

---

## 8. Using the CLI side-by-side

Every view in the Explorer has a CLI equivalent that returns the
same data as JSON, suitable for `jq` and scripting:

```bash
# Snapshot the ACS. --party is repeatable; --template accepts
# Module:Entity or pkg:Module:Entity.
canton-devkit localnet contracts ls \
  --name demo \
  --endpoint localhost:<ledger-port> \
  --party alice \
  --template Token:Holding

# Stream ACS changes from the current ledger end.
canton-devkit localnet contracts watch \
  --name demo \
  --endpoint localhost:<ledger-port>

# Recent transactions. --party / --template / --from / --to are the
# same filters the Web UI Transactions view exposes.
canton-devkit localnet tx ls \
  --name demo \
  --endpoint localhost:<ledger-port> \
  --party alice \
  --template Token:Holding

# Replay one transaction's per-party visibility projection — the
# CLI mirror of the Transactions view's "replay" button.
canton-devkit localnet tx replay \
  --name demo \
  --id <update-id> \
  --party alice
```

The `contracts ls --format json` output now includes the decoded
contract `payload` (the same field the Web UI drawer shows), so a
`jq` consumer can read field values, not just contract IDs.

A typical workflow: use the Explorer to navigate, pick out a
template ID or contract ID, then drop into the CLI to pipe the
data into a script. The CLI accepts the same role-scoped JWTs as
the Explorer.

The participant ledger port isn't host-published by default for
every Splice profile — `localnet status --name <n>` lists the
exposed ports under entries like `participant_ledger_app-user`.

---

## 9. Known limits

Things the Explorer does **not** do today:

- **Transactions / Timeline are not live.** Only the Contracts view
  streams (snapshot + SSE deltas). The Transactions and Timeline
  views read a bounded snapshot; re-apply the filters to refresh.
- **No exercise/create UI.** The drawer is read-only. Use the CLI
  or your app to write to the ledger.
- **No cross-instance comparison.** One instance at a time.
- **Reassignments are skipped in the ACS view.** In-flight
  reassignments are filtered out of the Contracts table; they
  still appear in Transactions and Timeline as their own update
  kind.
- **Variants, enums, maps fall back to a textual proto form** in
  the payload preview. Records, lists, primitives, parties, and
  contract IDs decode natively. The full typed decoder using
  Daml-LF metadata is planned.

If the Explorer can't show what you need, the CLI usually can —
or the underlying gRPC API directly via the SDK.

---

## 10. Error states you might see

| What you see | What it means | What to do |
|---|---|---|
| **"Participant ports not recorded"** | The instance was started by an older DevKit version that didn't capture the participant ledger port. | `canton-devkit localnet down --name <n>` then `up --name <n>` again. The newer `up` flow records all Canton API ports. |
| **"JWT lacks party-rights for ACS"** | The role's JWT is a user-id token (Splice default) whose user has no `actAs` / `readAs` rights on any party. | Grant rights via `UserManagementService`, or use a different role whose user already has them. |
| **"No instance selected"** | The Web UI doesn't have an instance picked yet. | Create one from the Dashboard or set `?instance=<name>` in the URL. |
| **"No contracts match the current filters"** | The snapshot loaded fine but every contract was filtered out. | Clear template/party chips or empty the search box. |

---

## 11. See also

- [docs/getting-started.md](getting-started.md) — starting a
  LocalNet and finding its participant ports.
- [docs/tokens.md](tokens.md) — driving CIP-0112 token flows from
  the CLI; useful to populate the ACS with realistic contracts
  while you explore.
- [docs/troubleshooting.md](troubleshooting.md) — port-recapture
  and JWT-related fixes.
