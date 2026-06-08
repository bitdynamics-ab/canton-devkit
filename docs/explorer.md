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
| **Contracts** | The Active Contract Set as a table, with a left-hand facet sidebar (templates, parties, time range) and a right-hand detail drawer. |
| **Transactions** | Recent ledger updates from the participant's `UpdateService`. Each row expands inline to show its event tree (creates, archives, exercises). |
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
against the snapshot already loaded:

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

The Transactions view is bounded — it pulls up to 200 recent
updates by default — and is a one-shot fetch, not a live stream.
Refresh the page to pick up new updates.

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

Everything the Explorer shows today is a **snapshot**:

- The Contracts view calls `StateService.GetActiveContracts` at
  the participant's current ledger end.
- The Transactions and Timeline views call `UpdateService` for
  the most recent N updates.

This means: contracts archived after you loaded the page still
appear, new creates do not. **Live streaming** (server-sent
events on top of the same gRPC streams) is planned but not yet
implemented. For now, refresh the tab to re-snapshot.

The status strip at the bottom of the Contracts table reads
"live snapshot" — the wording reflects how fresh the snapshot is
when loaded; it does **not** mean the table updates as new
contracts arrive.

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

# Recent transactions.
canton-devkit localnet contracts tx ls \
  --name demo \
  --endpoint localhost:<ledger-port>
```

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

- **No live streaming.** The Contracts, Transactions, and
  Timeline views all read a snapshot. Refresh to update.
  Server-sent events are planned.
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
