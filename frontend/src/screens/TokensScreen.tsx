import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  acceptTransfer,
  aliasMapFrom,
  burnToken,
  createParty,
  createToken,
  faucetToken,
  fetchActivity,
  fetchHoldingContracts,
  fetchParties,
  removeParty,
  fetchHoldings,
  fetchInstruments,
  fetchInstrumentSummary,
  fetchMatrix,
  mintToken,
  planTransfer,
  transferToken,
  type ActivityEvent,
  type AliasMap,
  type BalanceMatrix,
  type HoldingSource,
  type PartyRef,
  type HoldingContract,
  type InstrumentRef,
  type InstrumentSummary,
  type TokenHolding,
  type TokenRef,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// shortParty trims a fingerprinted id to its readable prefix for display.
function shortParty(p: string): string {
  const i = p.indexOf("::");
  return i > 0 ? p.slice(0, i) : p;
}

// partyLabel prefers a registered alias over the raw prefix:
// `app_user_v2-localparty-1::1220…` → `app-user` when aliased, else the
// `::`-prefix fallback.
function partyLabel(aliases: AliasMap, p: string): string {
  return aliases[p] ?? shortParty(p);
}

// Asset capability guards — verified live:
//   mint  : only the native test token (CIP-0112 v2) we created on-ledger.
//   burn  : no deployable token supports a standalone burn yet (needs
//           AllocationV2/DvP — ).
function mintDisabledReason(t: InstrumentRef): string | null {
  if (t.standard !== "CIP-0112 v2")
    return `${t.symbol} (${t.standard}) has no standard mint — use the asset's wallet UI`;
  if (!t.on_ledger)
    return `${t.symbol} is recorded only — create it on-ledger first`;
  return null;
}
const BURN_DISABLED_REASON =
  "Burn is only available on a native CIP-0112 v2 token created on this " +
  "instance — Amulet has no burn surface.";

// TOKEN_DAR_UNAVAILABLE_HINT is the friendly remediation for the on-ledger
// create 412 (TEST_TOKEN_DAR_UNAVAILABLE): the test-token DAR isn't
// published for this instance's Splice version. Shared by the create modal
// (where on-ledger create surfaces it) and the action-modal banner.
const TOKEN_DAR_UNAVAILABLE_HINT =
  "The test-token DAR isn't published for this instance's Splice version, so on-ledger " +
  "V2 tokens can't be created here. Bring up a token-standard-v2 instance " +
  "(localnet up --version token-standard-v2 --profile tokens-v2) and re-run.";

// createErrorText maps a token-create failure to the message shown in the
// create modal: the actionable DAR remedy for the on-ledger 412
// (TEST_TOKEN_DAR_UNAVAILABLE), otherwise the raw server message (or a
// generic fallback for a non-API error). Exported for unit testing.
export function createErrorText(e: unknown): string {
  if (e instanceof ApiError && e.code === "TEST_TOKEN_DAR_UNAVAILABLE") {
    return TOKEN_DAR_UNAVAILABLE_HINT;
  }
  return e instanceof ApiError ? e.message : "create failed";
}

// TokensScreen — .
//
// V2 Token Standard surface: lists every instrument recorded on the
// selected instance, exposes Mint / Transfer / Burn / Accept actions
// on each, and the Create wizard for a brand-new instrument. The
// holdings table for the selected instrument refreshes whenever the
// user picks a row or completes a mutation.
//
// Errors:
//   • 409 SYMBOL_IN_USE → surfaced in the create modal as a focused
//     "pick a different symbol" hint.
//   • 412 NEEDS_V2_LOCALNET → big yellow remediation banner with the
//     command to bring up the V2 LocalNet.
//   • everything else → red alert with the server message.
export function TokensScreen() {
  const sel = useInstanceSelection();
  const instance = sel.selected;

  const [list, setList] = useState<InstrumentRef[]>([]);
  const [listErr, setListErr] = useState<string | null>(null);
  const [refreshTick, setRefreshTick] = useState(0);
  const [activeSymbol, setActiveSymbol] = useState<string | null>(null);
  const [view, setView] = useState<"instruments" | "matrix">("instruments");

  const [holdings, setHoldings] = useState<TokenHolding[]>([]);
  const [holdingsErr, setHoldingsErr] = useState<string | null>(null);
  // holdingsSource: "ledger" = real on-ledger balances; "registry" =
  // the pseudo-balance fallback shown when no live participant is
  // reachable. Drives the disclaimer banner so a user never mistakes a
  // fabricated row for a real holding.
  const [holdingsSource, setHoldingsSource] = useState<HoldingSource>("ledger");
  const [expanded, setExpanded] = useState<string | null>(null); // party whose UTXOs are shown
  const [contracts, setContracts] = useState<HoldingContract[]>([]);
  // expandSeqRef is the monotonic counter behind toggleExpand's
  // latest-click guard: if the user clicks a new party while a fetch
  // is still in flight, the stale resolution bails before clobbering
  // the newer state. Same pattern as the `cancelled` flags in the
  // useEffect fetches above.
  const expandSeqRef = useRef(0);
  const [matrix, setMatrix] = useState<BalanceMatrix | null>(null);
  const [matrixErr, setMatrixErr] = useState<string | null>(null);
  const [summary, setSummary] = useState<InstrumentSummary | null>(null);
  const [parties, setParties] = useState<PartyRef[]>([]);
  const [showParties, setShowParties] = useState(false);
  const aliases: AliasMap = useMemo(() => aliasMapFrom(parties), [parties]);
  const [detailTab, setDetailTab] = useState<"overview" | "activity">("overview");
  const [activity, setActivity] = useState<ActivityEvent[] | null>(null);
  const [activityErr, setActivityErr] = useState<string | null>(null);

  const [showCreate, setShowCreate] = useState(false);
  const [modal, setModal] = useState<
    | { kind: "mint"; symbol: string }
    | { kind: "transfer"; symbol: string }
    | { kind: "burn"; symbol: string }
    | { kind: "faucet"; symbol: string }
    | { kind: "accept" }
    | null
  >(null);
  const [topNotice, setTopNotice] = useState<{ tone: "ok" | "warn" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (!instance) {
      setList([]);
      setActiveSymbol(null);
      return;
    }
    let cancelled = false;
    // ACS-derived instrument discovery: Amulet + any minted
    // token appear without a state.Tokens seed.
    fetchInstruments(instance)
      .then((items) => {
        if (cancelled) return;
        setList(items);
        setListErr(null);
        const syms = items.map((t) => t.symbol ?? t.instrument_id);
        if (syms.length > 0 && (!activeSymbol || !syms.includes(activeSymbol))) {
          setActiveSymbol(syms[0]);
        }
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setListErr(e instanceof ApiError ? e.message : "failed to load tokens");
      });
    return () => {
      cancelled = true;
    };
    // activeSymbol intentionally NOT in deps — selecting a row shouldn't refetch the list.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [instance, refreshTick]);

  // Matrix lens — one ACS scan, party × instrument.
  useEffect(() => {
    if (!instance || view !== "matrix") return;
    let cancelled = false;
    fetchMatrix(instance)
      .then((m) => {
        if (!cancelled) {
          setMatrix(m);
          setMatrixErr(null);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setMatrixErr(e instanceof ApiError ? e.message : "failed to load matrix");
      });
    return () => {
      cancelled = true;
    };
  }, [instance, view, refreshTick]);

  useEffect(() => {
    if (!instance || !activeSymbol) {
      setHoldings([]);
      return;
    }
    let cancelled = false;
    setExpanded(null);
    setContracts([]);
    fetchHoldings(instance, activeSymbol)
      .then((r) => {
        if (!cancelled) {
          setHoldings(r.holdings);
          setHoldingsSource(r.source ?? "ledger");
          setHoldingsErr(null);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setHoldingsErr(e instanceof ApiError ? e.message : "failed to load holdings");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [instance, activeSymbol, refreshTick]);

  // Party alias registry: one fetch per instance powers the
  // alias labels across every lens and the party manager.
  useEffect(() => {
    if (!instance) {
      setParties([]);
      return;
    }
    let cancelled = false;
    fetchParties(instance)
      .then((p) => {
        if (!cancelled) setParties(p);
      })
      .catch(() => {
        if (!cancelled) setParties([]);
      });
    return () => {
      cancelled = true;
    };
  }, [instance, refreshTick]);

  // Instrument-first KPI summary: supply, holder +
  // contract counts, holder distribution. One ACS scan; best-effort —
  // a failure just hides the KPI strip, the holdings table still loads.
  useEffect(() => {
    if (!instance || !activeSymbol) {
      setSummary(null);
      return;
    }
    let cancelled = false;
    fetchInstrumentSummary(instance, activeSymbol)
      .then((s) => {
        if (!cancelled) setSummary(s);
      })
      .catch(() => {
        if (!cancelled) setSummary(null);
      });
    return () => {
      cancelled = true;
    };
  }, [instance, activeSymbol, refreshTick]);

  // Activity feed: transfer/mint/burn history
  // reconstructed from the ledger transaction stream. Fetched lazily —
  // only when the Activity tab is open — since it's a full historical
  // scan, heavier than the ACS snapshots the other lenses use.
  useEffect(() => {
    if (!instance || !activeSymbol || detailTab !== "activity") return;
    let cancelled = false;
    setActivity(null);
    setActivityErr(null);
    fetchActivity(instance, activeSymbol)
      .then((ev) => {
        if (!cancelled) setActivity(ev);
      })
      .catch((e: unknown) => {
        if (!cancelled) setActivityErr(e instanceof ApiError ? e.message : "failed to load activity");
      });
    return () => {
      cancelled = true;
    };
  }, [instance, activeSymbol, detailTab, refreshTick]);

  // Selecting a different instrument resets the detail tab to Overview.
  useEffect(() => {
    setDetailTab("overview");
  }, [activeSymbol]);

  const active = useMemo(
    () => list.find((t) => (t.symbol ?? t.instrument_id) === activeSymbol) ?? null,
    [list, activeSymbol],
  );

  // Expand a party's balance into its individual Holding contracts (UTXOs).
  // The latest-click guard mirrors the `cancelled` pattern used by the
  // useEffect fetches above: if the user clicks a different party while
  // a fetch is still in flight, the stale resolution must not overwrite
  // the newer state. expandSeq increments on every click; the in-flight
  // closure captures its own seq and bails when it no longer matches.
  function toggleExpand(party: string) {
    if (expanded === party) {
      setExpanded(null);
      setContracts([]);
      return;
    }
    if (!instance || !activeSymbol) return;
    const seq = expandSeqRef.current + 1;
    expandSeqRef.current = seq;
    setExpanded(party);
    setContracts([]);
    fetchHoldingContracts(instance, activeSymbol, party)
      .then((cs) => {
        if (expandSeqRef.current === seq) setContracts(cs);
      })
      .catch(() => {
        if (expandSeqRef.current === seq) setContracts([]);
      });
  }

  function bump() {
    setRefreshTick((n) => n + 1);
  }

  function renderActionError(e: unknown, fallback: string): { tone: "warn" | "err"; text: string } {
    if (e instanceof ApiError && e.code === "NEEDS_V2_LOCALNET") {
      return {
        tone: "warn",
        text:
          "V2 ledger action not yet wired on this instance. Bring up a V2 LocalNet first " +
          "(localnet up --version token-standard-v2 --profile tokens-v2) and re-run.",
      };
    }
    if (e instanceof ApiError && e.code === "TEST_TOKEN_DAR_UNAVAILABLE") {
      return { tone: "warn", text: TOKEN_DAR_UNAVAILABLE_HINT };
    }
    return { tone: "err", text: e instanceof ApiError ? e.message : fallback };
  }

  if (!instance) {
    return (
      <section style={{ padding: 24 }}>
        <Header />
        <p style={{ color: W.dim }}>Select an instance from the topbar to view its tokens.</p>
      </section>
    );
  }
  if (listErr) {
    return (
      <section style={{ padding: 24 }}>
        <Header />
        <p role="alert" style={{ color: W.err }}>{listErr}</p>
      </section>
    );
  }

  return (
    <section style={{ padding: 24, display: "flex", flexDirection: "column", gap: 14 }}>
      <Header right={
        <span style={{ display: "flex", gap: 8 }}>
          <button
            type="button"
            onClick={() => setShowParties(true)}
            style={btnStyle(W.dim, false)}
          >
            ⦿ Parties{parties.length > 0 ? ` (${parties.length})` : ""}
          </button>
          <button
            type="button"
            onClick={() => setShowCreate(true)}
            style={btnStyle(W.brand, false, true)}
          >
            + Create token
          </button>
        </span>
      } />

      {topNotice && (
        <div role="status" style={notice(topNotice.tone)}>{topNotice.text}</div>
      )}

      {/* Lens switcher */}
      <div style={{ display: "flex", gap: 4, background: W.surface2, borderRadius: 8, padding: 3, width: "fit-content", border: `1px solid ${W.border}` }}>
        {(["instruments", "matrix"] as const).map((v) => (
          <button
            key={v}
            onClick={() => setView(v)}
            style={{
              padding: "5px 14px", fontSize: 12, borderRadius: 5, border: "none", cursor: "pointer",
              fontWeight: 600,
              background: view === v ? W.brand : "transparent",
              color: view === v ? "#082018" : W.dim,
            }}
          >
            {v === "instruments" ? "Instruments" : "Holdings matrix"}
          </button>
        ))}
      </div>

      {view === "matrix" ? (
        <MatrixLens matrix={matrix} err={matrixErr} aliases={aliases} />
      ) : list.length === 0 ? (
        <div style={{ color: W.dim, fontSize: 13 }}>
          No instruments on <code>{instance}</code> yet. Click <b>Create token</b> above
          (or run <code>dpm localnet token create --instance {instance}</code>).
        </div>
      ) : (
        <div style={{ display: "grid", gridTemplateColumns: "320px 1fr", gap: 14 }}>
          {/* Left rail: instrument list (ACS-discovered) */}
          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10, overflow: "hidden" }}>
            {list.map((t) => {
              const sym = t.symbol ?? t.instrument_id;
              const isActive = sym === activeSymbol;
              return (
                <button
                  key={sym}
                  onClick={() => setActiveSymbol(sym)}
                  style={{
                    display: "block", width: "100%", textAlign: "left", padding: "10px 14px",
                    background: isActive ? W.surface2 : "transparent", border: "none",
                    borderLeft: `2px solid ${isActive ? W.brand : "transparent"}`, cursor: "pointer",
                  }}
                >
                  <div style={{ fontWeight: 600, fontSize: 13, color: W.text }}>
                    {sym} {t.name && <span style={{ color: W.dim, fontWeight: 400 }}>· {t.name}</span>}
                  </div>
                  <div style={{ color: W.dim, fontSize: 11, marginTop: 2 }}>
                    {t.standard}{t.on_ledger ? " · on-ledger" : " · recorded"}
                  </div>
                </button>
              );
            })}
          </div>

          {/* Right pane: detail + holdings + actions */}
          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10, padding: 16 }}>
            {active && (() => {
              const sym = active.symbol ?? active.instrument_id;
              const mintReason = mintDisabledReason(active);
              return (
              <>
                <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                  <h3 style={{ color: W.text, margin: 0 }}>{active.name ?? sym}</h3>
                  <span style={{ color: W.dim, fontFamily: wMono, fontSize: 12 }}>
                    {sym} · {active.standard}
                  </span>
                  <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
                    <button
                      onClick={() => setModal({ kind: "mint", symbol: sym })}
                      disabled={!!mintReason}
                      title={mintReason ?? "Mint new supply"}
                      style={btnStyle(W.brand, false, false, !!mintReason)}
                    >↑ Mint</button>
                    <button onClick={() => setModal({ kind: "transfer", symbol: sym })} style={btnStyle(W.brand, false)}>→ Transfer</button>
                    <button onClick={() => setModal({ kind: "faucet", symbol: sym })} title="Fund a party from a funded source (auto-accepted)" style={btnStyle(W.brand, false)}>⛲ Faucet</button>
                    <button
                      onClick={() => setModal({ kind: "burn", symbol: sym })}
                      disabled={!!mintReason}
                      title={mintReason ? BURN_DISABLED_REASON : "Burn holdings (archive path)"}
                      style={btnStyle(W.err, false, false, !!mintReason)}
                    >🔥 Burn</button>
                    <button onClick={() => setModal({ kind: "accept" })} style={btnStyle(W.warn, false)}>✓ Accept transfer</button>
                  </span>
                </div>
                <div style={{ color: W.dim, fontSize: 12, marginTop: 4, fontFamily: wMono }}>
                  admin {partyLabel(aliases, active.admin)} · id {active.instrument_id}
                </div>

                {/* Overview / Activity tab switcher */}
                <div style={{ display: "flex", gap: 4, margin: "16px 0 2px", borderBottom: `1px solid ${W.border}` }}>
                  {(["overview", "activity"] as const).map((tab) => (
                    <button
                      key={tab}
                      onClick={() => setDetailTab(tab)}
                      style={{
                        background: "transparent",
                        border: "none",
                        borderBottom: `2px solid ${detailTab === tab ? W.brand : "transparent"}`,
                        color: detailTab === tab ? W.text : W.dim,
                        padding: "6px 12px",
                        fontSize: 13,
                        fontWeight: detailTab === tab ? 600 : 400,
                        cursor: "pointer",
                        textTransform: "capitalize",
                      }}
                    >
                      {tab}
                    </button>
                  ))}
                </div>

                {detailTab === "overview" && (
                  <>
                    {summary && <KpiRow s={summary} />}
                    {summary && summary.holders.length > 0 && <HolderDistribution s={summary} aliases={aliases} />}

                    <h4 style={{ color: W.text2, margin: "18px 0 8px" }}>
                      Holdings <span style={{ color: W.dim, fontWeight: 400, fontSize: 12 }}>· a balance is the sum of its Holding contracts — click a row to expand</span>
                    </h4>
                    {holdingsSource === "registry" && (
                      <div role="status" style={{ ...notice("warn"), marginBottom: 8 }}>
                        No live ledger reachable for <code>{instance}</code>. These are
                        <b> registry pseudo-balances</b> — local bookkeeping that shows the issuer
                        holding the full supply and everyone else zero, <b>not</b> on-ledger holdings.
                        Start the instance to see real balances.
                      </div>
                    )}
                    {holdingsErr && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{holdingsErr}</div>}
                    <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
                      <thead>
                        <tr style={{ color: W.dim, textAlign: "left" }}>
                          <th style={th}>PARTY</th>
                          <th style={th}>AMOUNT</th>
                        </tr>
                      </thead>
                      <tbody>
                        {holdings.map((h, i) => (
                          <>
                            <tr
                              key={i}
                              onClick={() => toggleExpand(h.party)}
                              style={{ cursor: "pointer", background: expanded === h.party ? W.surface2 : "transparent" }}
                            >
                              <td style={td}>
                                <span style={{ color: W.brand, display: "inline-block", width: 14 }}>
                                  {expanded === h.party ? "▾" : "▸"}
                                </span>
                                {partyLabel(aliases, h.party)}
                              </td>
                              <td style={{ ...td, fontFamily: wMono }}>{h.amount}</td>
                            </tr>
                            {expanded === h.party && contracts.map((c) => (
                              <tr key={c.contract_id} style={{ background: W.bg }}>
                                <td style={{ ...td, paddingLeft: 34, fontFamily: wMono, color: W.mag, fontSize: 11 }}>
                                  └ {c.contract_id.slice(0, 16)}…
                                  {c.locked && <span style={{ color: W.warn, marginLeft: 8 }}>locked</span>}
                                </td>
                                <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{c.amount}</td>
                              </tr>
                            ))}
                            {expanded === h.party && contracts.length === 0 && (
                              <tr><td colSpan={2} style={{ ...td, paddingLeft: 34, color: W.dim, fontSize: 11 }}>loading contracts…</td></tr>
                            )}
                          </>
                        ))}
                        {holdings.length === 0 && (
                          <tr><td colSpan={2} style={{ ...td, color: W.dim }}>No holdings yet.</td></tr>
                        )}
                      </tbody>
                    </table>
                  </>
                )}

                {detailTab === "activity" && (
                  <ActivityFeed events={activity} err={activityErr} aliases={aliases} />
                )}
              </>
              );
            })()}
          </div>
        </div>
      )}

      {showParties && (
        <PartyManagerModal
          instance={instance}
          parties={parties}
          onClose={() => setShowParties(false)}
          onChanged={() => bump()}
          onError={(e) => setTopNotice(renderActionError(e, "party action failed"))}
        />
      )}
      {showCreate && (
        <CreateTokenModal
          instance={instance}
          onClose={() => setShowCreate(false)}
          onCreated={(ref) => {
            setShowCreate(false);
            setActiveSymbol(ref.symbol);
            bump();
            setTopNotice({ tone: "ok", text: `Recorded ${ref.symbol} (${ref.name}).` });
          }}
        />
      )}
      {modal?.kind === "mint" && active && (
        <ActionModal
          title={`Mint ${modal.symbol}`}
          fields={[{ label: "To party", key: "to" }, { label: "Amount", key: "amount" }]}
          onClose={() => setModal(null)}
          submit={(v) => mintToken(instance, modal.symbol, v.to, v.amount)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "mint failed"))}
        />
      )}
      {modal?.kind === "transfer" && active && (
        <TransferModal
          instance={instance}
          symbol={modal.symbol}
          onClose={() => setModal(null)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "transfer failed"))}
        />
      )}
      {modal?.kind === "burn" && active && (
        <ActionModal
          title={`Burn ${modal.symbol}`}
          fields={[{ label: "From party", key: "from" }, { label: "Amount", key: "amount" }]}
          onClose={() => setModal(null)}
          submit={(v) => burnToken(instance, modal.symbol, v.from, v.amount)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "burn failed"))}
        />
      )}
      {modal?.kind === "faucet" && active && (
        <ActionModal
          title={`Faucet ${modal.symbol}`}
          fields={[
            { label: "To party", key: "to" },
            { label: "Amount", key: "amount" },
            { label: "Source (optional — defaults to funded party)", key: "source", optional: true },
          ]}
          onClose={() => setModal(null)}
          submit={(v) => faucetToken(instance, modal.symbol, v.to, v.amount, v.source || undefined)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "faucet failed"))}
        />
      )}
      {modal?.kind === "accept" && (
        <ActionModal
          title="Accept transfer"
          fields={[{ label: "Transfer instruction id", key: "id" }]}
          onClose={() => setModal(null)}
          submit={(v) => acceptTransfer(instance, v.id)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "accept failed"))}
        />
      )}
    </section>
  );
}

// TransferModal — From/To/Amount + a live coin-selection preview
//. As the user fills From + Amount, it dry-runs the transfer
// (planTransfer) and shows which Holding contracts would be consumed and
// the change returned — the Canton UTXO reality, before any submit.
function TransferModal({
  instance, symbol, onClose, onDone, onError,
}: {
  instance: string;
  symbol: string;
  onClose: () => void;
  onDone: () => void;
  onError: (e: unknown) => void;
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [autoAccept, setAutoAccept] = useState(true);
  const [busy, setBusy] = useState(false);
  const [plan, setPlan] = useState<import("../api").TransferPlan | null>(null);

  // Debounced dry-run whenever from + amount are both present.
  useEffect(() => {
    if (!from || !amount) {
      setPlan(null);
      return;
    }
    let cancelled = false;
    const t = setTimeout(() => {
      planTransfer(instance, symbol, from, amount)
        .then((p) => { if (!cancelled) setPlan(p); })
        .catch(() => { if (!cancelled) setPlan(null); });
    }, 350);
    return () => { cancelled = true; clearTimeout(t); };
  }, [instance, symbol, from, amount]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await transferToken(instance, symbol, from, to, amount, reason || undefined, undefined, autoAccept);
      onDone();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title={`Transfer ${symbol}`} onClose={onClose}>
      <form onSubmit={onSubmit} style={{ display: "grid", gap: 10 }}>
        <Field label="From party"><input value={from} onChange={(e) => setFrom(e.target.value)} style={input} required /></Field>
        <Field label="To party"><input value={to} onChange={(e) => setTo(e.target.value)} style={input} required /></Field>
        <Field label="Amount"><input value={amount} onChange={(e) => setAmount(e.target.value)} style={input} required /></Field>
        <Field label="Reason (optional)"><input value={reason} onChange={(e) => setReason(e.target.value)} style={input} /></Field>
        <label style={{ display: "flex", alignItems: "center", gap: 8, color: W.text2, fontSize: 12, cursor: "pointer" }}>
          <input type="checkbox" checked={autoAccept} onChange={(e) => setAutoAccept(e.target.checked)} />
          Auto-accept (settle in one step — you own the receiver on LocalNet)
        </label>

        {plan && (
          <div style={{ background: W.surface2, border: `1px solid ${W.border}`, borderRadius: 8, padding: "10px 12px" }}>
            <div style={{ color: W.dim, fontSize: 10.5, letterSpacing: 1, textTransform: "uppercase", fontWeight: 600, marginBottom: 6 }}>
              Coin selection preview
            </div>
            {plan.sufficient ? (
              <div style={{ display: "grid", gap: 4, fontFamily: wMono, fontSize: 11.5 }}>
                {plan.inputs.map((i) => (
                  <div key={i.contract_id} style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: W.text2 }}>{i.contract_id.slice(0, 14)}… consume</span>
                    <span style={{ color: W.err }}>−{i.amount}</span>
                  </div>
                ))}
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: W.text2 }}>→ {shortParty(to || from)} receive</span>
                  <span style={{ color: W.ok }}>+{amount}</span>
                </div>
                {Number(plan.change) > 0 && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: W.text2 }}>→ {shortParty(from)} change</span>
                    <span style={{ color: W.ok }}>+{plan.change}</span>
                  </div>
                )}
                <div style={{ height: 1, background: W.border, margin: "3px 0" }} />
                <div style={{ display: "flex", justifyContent: "space-between", color: W.dim }}>
                  <span>{plan.inputs.length} input {plan.inputs.length === 1 ? "contract" : "contracts"} · total {plan.total_input}</span>
                </div>
              </div>
            ) : (
              <div style={{ color: W.warn, fontSize: 12 }}>
                Insufficient: holds {plan.total_input}, short {plan.shortfall}.
              </div>
            )}
          </div>
        )}

        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6 }}>
          <button type="button" onClick={onClose} style={btnStyle(W.dim, false)}>Cancel</button>
          <button
            type="submit"
            disabled={busy || (plan ? !plan.sufficient : false)}
            style={btnStyle(W.brand, busy, true, plan ? !plan.sufficient : false)}
          >
            {busy ? "Submitting…" : "Transfer"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

// KpiRow — the instrument-first KPI strip. Supply,
// circulating (= supply on a UTXO ledger), holder count, and the number
// of Holding contracts backing it. All derived from one ACS scan.
function KpiRow({ s }: { s: InstrumentSummary }) {
  const cards: Array<{ label: string; value: string; hint?: string }> = [
    { label: "Total supply", value: s.total_supply },
    { label: "In circulation", value: s.total_supply, hint: "sum of all holdings" },
    { label: "Holders", value: String(s.holder_count) },
    { label: "Holding contracts", value: String(s.contract_count), hint: "UTXOs" },
  ];
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))",
        gap: 10,
        margin: "16px 0 4px",
      }}
    >
      {cards.map((c) => (
        <div
          key={c.label}
          style={{
            background: W.bg,
            border: `1px solid ${W.border}`,
            borderRadius: 8,
            padding: "10px 12px",
          }}
        >
          <div style={{ color: W.dim, fontSize: 11, textTransform: "uppercase", letterSpacing: 0.4 }}>
            {c.label}
          </div>
          <div style={{ color: W.text, fontSize: 20, fontFamily: wMono, marginTop: 4 }}>{c.value}</div>
          {c.hint && <div style={{ color: W.dim, fontSize: 10, marginTop: 2 }}>{c.hint}</div>}
        </div>
      ))}
    </div>
  );
}

// HolderDistribution — per-holder stake table: balance,
// share of supply (with an inline bar), and how many Holding contracts
// back each holder. Sorted biggest-first by the backend.
function HolderDistribution({ s, aliases }: { s: InstrumentSummary; aliases: AliasMap }) {
  return (
    <>
      <h4 style={{ color: W.text2, margin: "16px 0 6px" }}>
        Holder distribution{" "}
        <span style={{ color: W.dim, fontWeight: 400, fontSize: 12 }}>· share of total supply</span>
      </h4>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
        <thead>
          <tr style={{ color: W.dim, textAlign: "left" }}>
            <th style={th}>HOLDER</th>
            <th style={th}>BALANCE</th>
            <th style={th}>SHARE</th>
            <th style={th}>UTXOS</th>
          </tr>
        </thead>
        <tbody>
          {s.holders.map((h) => {
            const pct = Number(h.pct_of_supply) || 0;
            return (
              <tr key={h.party}>
                <td style={td}>{partyLabel(aliases, h.party)}</td>
                <td style={{ ...td, fontFamily: wMono }}>{h.balance}</td>
                <td style={td}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <div style={{ flex: 1, height: 6, background: W.surface2, borderRadius: 3, minWidth: 40 }}>
                      <div
                        style={{
                          width: `${Math.min(100, pct)}%`,
                          height: "100%",
                          background: W.brand,
                          borderRadius: 3,
                        }}
                      />
                    </div>
                    <span style={{ fontFamily: wMono, color: W.text2, fontSize: 12, minWidth: 44, textAlign: "right" }}>
                      {h.pct_of_supply}%
                    </span>
                  </div>
                </td>
                <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{h.contract_count}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}

// ActivityFeed — the instrument's transfer/mint/burn history (
// Activity tab), reconstructed from the ledger transaction stream. Each
// row is one netted transaction: kind, amount, and who sent → received.
function ActivityFeed({ events, err, aliases }: { events: ActivityEvent[] | null; err: string | null; aliases: AliasMap }) {
  if (err) return <div role="alert" style={{ color: W.err, fontSize: 13, marginTop: 12 }}>{err}</div>;
  if (events === null) return <div style={{ color: W.dim, fontSize: 13, marginTop: 12 }}>Scanning ledger history…</div>;
  if (events.length === 0)
    return <div style={{ color: W.dim, fontSize: 13, marginTop: 12 }}>No activity for this instrument yet.</div>;

  const tone: Record<ActivityEvent["kind"], string> = {
    mint: W.brand,
    burn: W.err,
    transfer: W.warn,
  };
  const fmtParties = (ps?: { party: string; amount: string }[]) =>
    !ps || ps.length === 0
      ? "·"
      : ps.map((p) => `${partyLabel(aliases, p.party)} ${p.amount}`).join(", ");
  return (
    <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13, marginTop: 12 }}>
      <thead>
        <tr style={{ color: W.dim, textAlign: "left" }}>
          <th style={th}>TIME</th>
          <th style={th}>KIND</th>
          <th style={th}>AMOUNT</th>
          <th style={th}>FROM</th>
          <th style={th}>TO</th>
        </tr>
      </thead>
      <tbody>
        {events.map((e) => (
          <tr key={e.offset}>
            <td style={{ ...td, color: W.dim, fontSize: 11, fontFamily: wMono }}>
              {e.record_time ? e.record_time.replace("T", " ").slice(0, 19) : `@${e.offset}`}
            </td>
            <td style={td}>
              <span
                style={{
                  color: tone[e.kind],
                  border: `1px solid ${tone[e.kind]}`,
                  borderRadius: 4,
                  padding: "1px 6px",
                  fontSize: 11,
                  textTransform: "uppercase",
                }}
              >
                {e.kind}
              </span>
            </td>
            <td style={{ ...td, fontFamily: wMono }}>{e.amount}</td>
            <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{fmtParties(e.senders)}</td>
            <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{fmtParties(e.receivers)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// MatrixLens — the god-mode party × instrument balance table (/
// #2). One ACS scan; rows = parties, columns = instruments,
// plus a totals row. Only the parties the role's JWT can read appear.
function MatrixLens({ matrix, err, aliases }: { matrix: BalanceMatrix | null; err: string | null; aliases: AliasMap }) {
  if (err) return <div role="alert" style={{ color: W.err, fontSize: 13 }}>{err}</div>;
  if (!matrix) return <div style={{ color: W.dim, fontSize: 13 }}>Loading matrix…</div>;

  const syms = matrix.instruments.map((i) => i.symbol ?? i.instrument_id);
  const symByInst: Record<string, string> = {};
  matrix.instruments.forEach((i) => { symByInst[i.instrument_id] = i.symbol ?? i.instrument_id; });
  const amt: Record<string, Record<string, string>> = {};
  matrix.cells.forEach((c) => {
    (amt[c.party] ??= {})[symByInst[c.instrument_id]] = c.amount;
  });
  const totals: Record<string, string> = {};
  matrix.totals.forEach((t) => { totals[symByInst[t.instrument_id]] = t.amount; });
  const parties = [...matrix.parties].sort();

  return (
    <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10, padding: 16, overflowX: "auto" }}>
      <div style={{ color: W.dim, fontSize: 12, marginBottom: 10 }}>
        {parties.length} {parties.length === 1 ? "party" : "parties"} × {syms.length} {syms.length === 1 ? "instrument" : "instruments"} —
        every readable party's balance of every instrument, in one ACS scan.
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
        <thead>
          <tr style={{ color: W.dim, textAlign: "left" }}>
            <th style={th}>PARTY ╲ TOKEN</th>
            {syms.map((s) => <th key={s} style={{ ...th, textAlign: "right" }}>{s}</th>)}
          </tr>
        </thead>
        <tbody>
          {parties.map((p) => (
            <tr key={p}>
              <td style={td}>{partyLabel(aliases, p)}</td>
              {syms.map((s) => (
                <td key={s} style={{ ...td, textAlign: "right", fontFamily: wMono, color: amt[p]?.[s] ? W.text : W.dim }}>
                  {amt[p]?.[s] ?? "·"}
                </td>
              ))}
            </tr>
          ))}
          <tr>
            <td style={{ ...td, color: W.brand, fontWeight: 700, textTransform: "uppercase", fontSize: 11 }}>Σ total</td>
            {syms.map((s) => (
              <td key={s} style={{ ...td, textAlign: "right", fontFamily: wMono, fontWeight: 700 }}>{totals[s] ?? ""}</td>
            ))}
          </tr>
          {parties.length === 0 && (
            <tr><td colSpan={syms.length + 1} style={{ ...td, color: W.dim }}>No holdings visible to this role.</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Header({ right }: { right?: React.ReactNode }) {
  return (
    <header style={{ display: "flex", alignItems: "center", gap: 12 }}>
      <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>Tokens</h2>
      <span style={{ color: W.dim, fontSize: 12 }}>V2 Token Standard instruments + actions</span>
      <span style={{ marginLeft: "auto" }}>{right}</span>
    </header>
  );
}

// PartyManagerModal — the god-mode party registry: list the
// instance's aliased parties, allocate a new one by name, or forget an
// alias. New parties immediately become visible in the matrix / activity
// (the scan grants read-as for every registered party).
function PartyManagerModal({
  instance,
  parties,
  onClose,
  onChanged,
  onError,
}: {
  instance: string;
  parties: PartyRef[];
  onClose: () => void;
  onChanged: () => void;
  onError: (e: unknown) => void;
}) {
  const [alias, setAlias] = useState("");
  const [busy, setBusy] = useState(false);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    if (!alias.trim()) return;
    setBusy(true);
    try {
      await createParty(instance, alias.trim());
      setAlias("");
      onChanged();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function remove(a: string) {
    setBusy(true);
    try {
      await removeParty(instance, a);
      onChanged();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="Parties" onClose={onClose}>
      <p style={{ color: W.dim, fontSize: 12, marginTop: 0 }}>
        On LocalNet you own every party. Name one here and use the alias anywhere
        a party is accepted — it appears in the matrix and activity automatically.
      </p>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13, marginBottom: 12 }}>
        <thead>
          <tr style={{ color: W.dim, textAlign: "left" }}>
            <th style={th}>ALIAS</th>
            <th style={th}>ROLE</th>
            <th style={th}></th>
          </tr>
        </thead>
        <tbody>
          {parties.map((p) => (
            <tr key={p.alias}>
              <td style={{ ...td, fontWeight: 600 }}>{p.alias}</td>
              <td style={{ ...td, color: W.dim }}>{p.role}</td>
              <td style={{ ...td, textAlign: "right" }}>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => remove(p.alias)}
                  title="Forget this alias (the on-ledger party persists)"
                  style={{ background: "transparent", border: "none", color: W.err, cursor: "pointer", fontSize: 12 }}
                >
                  forget
                </button>
              </td>
            </tr>
          ))}
          {parties.length === 0 && (
            <tr><td colSpan={3} style={{ ...td, color: W.dim }}>No parties registered yet.</td></tr>
          )}
        </tbody>
      </table>
      <form onSubmit={add} style={{ display: "flex", gap: 8 }}>
        <input
          value={alias}
          onChange={(e) => setAlias(e.target.value)}
          placeholder="new alias (e.g. bob)"
          style={{ flex: 1, background: W.bg, border: `1px solid ${W.border}`, borderRadius: 6, padding: "8px 10px", color: W.text, fontSize: 13 }}
        />
        <button type="submit" disabled={busy || !alias.trim()} style={btnStyle(W.brand, busy, true, !alias.trim())}>
          {busy ? "…" : "+ Allocate"}
        </button>
      </form>
    </ModalShell>
  );
}

function CreateTokenModal({
  instance,
  onClose,
  onCreated,
}: {
  instance: string;
  onClose: () => void;
  onCreated: (ref: TokenRef) => void;
}) {
  const [name, setName] = useState("");
  const [symbol, setSymbol] = useState("");
  const [decimals, setDecimals] = useState(6);
  const [initialSupply, setInitialSupply] = useState("");
  const [issuer, setIssuer] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const ref = await createToken(instance, {
        name, symbol, decimals, initial_supply: initialSupply, issuer,
      });
      onCreated(ref);
    } catch (e) {
      setErr(createErrorText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <ModalShell title="Create V2 instrument" onClose={onClose}>
      <form onSubmit={onSubmit} style={{ display: "grid", gap: 8 }}>
        <Field label="Name"><input value={name} onChange={(e) => setName(e.target.value)} style={input} required /></Field>
        <Field label="Symbol"><input value={symbol} onChange={(e) => setSymbol(e.target.value)} style={input} required /></Field>
        <Field label="Decimals">
          <input type="number" min={0} max={18} value={decimals} onChange={(e) => setDecimals(Number(e.target.value))} style={input} />
        </Field>
        <Field label="Initial supply"><input value={initialSupply} onChange={(e) => setInitialSupply(e.target.value)} style={input} required /></Field>
        <Field label="Issuer party id"><input value={issuer} onChange={(e) => setIssuer(e.target.value)} style={input} required /></Field>
        {err && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{err}</div>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6 }}>
          <button type="button" onClick={onClose} style={btnStyle(W.dim, false)}>Cancel</button>
          <button type="submit" disabled={busy} style={btnStyle(W.brand, busy, true)}>{busy ? "Creating…" : "Create"}</button>
        </div>
      </form>
    </ModalShell>
  );
}

function ActionModal({
  title,
  fields,
  onClose,
  submit,
  onDone,
  onError,
}: {
  title: string;
  fields: { label: string; key: string; optional?: boolean }[];
  onClose: () => void;
  submit: (values: Record<string, string>) => Promise<void>;
  onDone: () => void;
  onError: (e: unknown) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await submit(values);
      onDone();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "failed");
      onError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <ModalShell title={title} onClose={onClose}>
      <form onSubmit={onSubmit} style={{ display: "grid", gap: 8 }}>
        {fields.map((f) => (
          <Field key={f.key} label={f.label}>
            <input
              value={values[f.key] ?? ""}
              onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
              style={input}
              required={!f.optional}
            />
          </Field>
        ))}
        {err && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{err}</div>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6 }}>
          <button type="button" onClick={onClose} style={btnStyle(W.dim, false)}>Cancel</button>
          <button type="submit" disabled={busy} style={btnStyle(W.brand, busy, true)}>{busy ? "Submitting…" : "Submit"}</button>
        </div>
      </form>
    </ModalShell>
  );
}

function ModalShell({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div role="dialog" aria-label={title} style={{
      position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)",
      display: "grid", placeItems: "center", zIndex: 50,
    }}>
      <div style={{
        background: W.surface, border: `1px solid ${W.border}`,
        borderRadius: 12, padding: 18, width: 420, maxWidth: "92vw",
      }}>
        <div style={{ display: "flex", alignItems: "center", marginBottom: 12 }}>
          <h3 style={{ color: W.text, margin: 0, fontSize: 14 }}>{title}</h3>
          <button onClick={onClose} aria-label="close" style={{
            marginLeft: "auto", background: "transparent", color: W.dim,
            border: "none", cursor: "pointer", fontSize: 18,
          }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: "grid", gap: 4 }}>
      <span style={{ color: W.dim, fontSize: 11 }}>{label}</span>
      {children}
    </label>
  );
}

const input: React.CSSProperties = {
  background: W.surface2, color: W.text, border: `1px solid ${W.border}`,
  borderRadius: 6, padding: "6px 8px", fontSize: 13,
};
const th: React.CSSProperties = { padding: "6px 8px", borderBottom: `1px solid ${W.border}`, fontSize: 11 };
const td: React.CSSProperties = { padding: "6px 8px", borderBottom: `1px solid ${W.border}`, color: W.text };

function notice(tone: "ok" | "warn" | "err"): React.CSSProperties {
  const c = tone === "ok" ? W.ok : tone === "warn" ? W.warn : W.err;
  return {
    background: `${c}10`, color: c, border: `1px solid ${c}`,
    borderRadius: 8, padding: "8px 12px", fontSize: 12.5,
  };
}

function btnStyle(accent: string, busy: boolean, filled = false, disabled = false): React.CSSProperties {
  if (disabled) {
    return {
      background: "transparent", color: W.dim, border: `1px solid ${W.border}`,
      borderRadius: 6, padding: filled ? "5px 12px" : "4px 10px",
      fontSize: filled ? 12 : 11.5, fontWeight: 600, cursor: "not-allowed", opacity: 0.6,
    };
  }
  return filled
    ? {
        background: busy ? W.surface2 : accent,
        color: busy ? W.dim : "#0B0E13",
        border: "none", borderRadius: 6, padding: "5px 12px",
        fontSize: 12, fontWeight: 600, cursor: busy ? "wait" : "pointer",
      }
    : {
        background: "transparent",
        color: busy ? W.dim : accent,
        border: `1px solid ${busy ? W.dim : accent}`,
        borderRadius: 6, padding: "4px 10px", fontSize: 11.5,
        fontWeight: 600, cursor: busy ? "wait" : "pointer",
      };
}
