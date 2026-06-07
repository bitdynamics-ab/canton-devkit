# DevKit reviewer kit (M1 adoption)

The M1 adoption metric is: **≥3 external companies/teams have reviewed
canton-devkit and tested LocalNet setup + lifecycle.** This kit is
everything you need to recruit and run those reviews. The recruiting
itself — identifying and contacting teams — is a human step; this page
makes it turnkey once you have a contact.

> **Status of the metric is people, not code.** Securing 3 external teams
> is outreach work. Track the actual reviewers in the table at the bottom.

## Who to approach

Good first reviewers are teams who already touch Canton/Daml and feel the
LocalNet pain canton-devkit removes:

- Daml app developers who currently hand-roll `docker compose` against
  Splice.
- Teams on the CIP-0112 token path.
- Canton Foundation ecosystem contacts (co-marketing).
- Internal teams at partner orgs already piloting Canton.

## The ask (copy-paste outreach template)

> Subject: 15-minute LocalNet review — canton-devkit
>
> Hi <name> — we built **canton-devkit**, a single-binary tool that brings
> up a full Canton LocalNet (sequencers, mediators, participants, Splice
> apps) in one command, with a CLI + Web UI for the whole lifecycle and
> CIP-0112 token tooling.
>
> Would you spend ~15 minutes taking it zero-to-running and telling us
> where it's rough? Everything you need is one page:
> `docs/getting-started.md`, and there's a self-timing harness
> (`scripts/validate-zero-to-localnet.sh`) if you want it.
>
> We're specifically validating "new user → running LocalNet in under 10
> minutes." Your friction notes are the whole point — no prep needed.

## What to send them

1. **Install + first run** — [getting-started.md](../getting-started.md).
2. **The checklist** — [validation-checklist.md](../validation-checklist.md)
   (manual boxes + the timed harness).
3. **A token walkthrough** (optional, for CIP-0112 teams) —
   [tokens.md](../tokens.md) or `scripts/demo.sh --with-tokens`.
4. **Where to file feedback** — a GitHub issue with the `doctor` output +
   their platform, or the structured form below.

## Feedback form (what to collect per reviewer)

```
Company / team:
Reviewer:
Platform (OS + arch):
Docker memory:
Cache: cold | warm
zero-to-LocalNet wall-clock:
Result: pass | fail (which step)
Top 3 friction points:
Would they use it again? (y/n + why)
CIP-0112 token flow tried? (y/n)
OK to attribute publicly? (y/n)
```

## Tracking

| # | Company / team | Contact | Date | Result | Friction notes | Public-OK |
|---|---|---|---|---|---|---|
| 1 | _TBD_ | | | | | |
| 2 | _TBD_ | | | | | |
| 3 | _TBD_ | | | | | |

Three rows filled with a `pass` (or a fixed `fail`) closes the M1
adoption metric. Feed the friction notes into the UX polish backlog
and the aggregate into the M4 adoption transparency update.
