import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  acceptTransfer,
  createToken,
  fetchHoldingContracts,
  fetchHoldings,
  fetchInstruments,
  fetchMatrix,
  mintToken,
  planTransfer,
  transferToken,
  type BalanceMatrix,
  type HoldingContract,
  type InstrumentRef,
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

// Asset capability guards — verified live (BIT-219):
//   mint  : only the native test token (CIP-0112 v2) we created on-ledger.
//   burn  : no deployable token supports a standalone burn yet (needs
//           AllocationV2/DvP — BIT-216).
function mintDisabledReason(t: InstrumentRef): string | null {
  if (t.standard !== "CIP-0112 v2")
    return `${t.symbol} (${t.standard}) has no standard mint — use the asset's wallet UI`;
  if (!t.on_ledger)
    return `${t.symbol} is recorded only — create it on-ledger first`;
  return null;
}
const BURN_DISABLED_REASON =
  "Burn isn't wired yet: no deployable V2 token supports a standalone burn " +
  "(it requires the AllocationV2 / DvP settlement flow — tracked in BIT-216).";

// TokensScreen — BIT-140.
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
  const [expanded, setExpanded] = useState<string | null>(null); // party whose UTXOs are shown
  const [contracts, setContracts] = useState<HoldingContract[]>([]);
  const [matrix, setMatrix] = useState<BalanceMatrix | null>(null);
  const [matrixErr, setMatrixErr] = useState<string | null>(null);

  const [showCreate, setShowCreate] = useState(false);
  const [modal, setModal] = useState<
    | { kind: "mint"; symbol: string }
    | { kind: "transfer"; symbol: string }
    | { kind: "burn"; symbol: string }
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
    // ACS-derived instrument discovery (BIT-219): Amulet + any minted
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

  const active = useMemo(
    () => list.find((t) => (t.symbol ?? t.instrument_id) === activeSymbol) ?? null,
    [list, activeSymbol],
  );

  // Expand a party's balance into its individual Holding contracts (UTXOs).
  function toggleExpand(party: string) {
    if (expanded === party) {
      setExpanded(null);
      setContracts([]);
      return;
    }
    if (!instance || !activeSymbol) return;
    setExpanded(party);
    setContracts([]);
    fetchHoldingContracts(instance, activeSymbol, party)
      .then((cs) => setContracts(cs))
      .catch(() => setContracts([]));
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
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          style={btnStyle(W.brand, false, true)}
        >
          + Create token
        </button>
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
        <MatrixLens matrix={matrix} err={matrixErr} />
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
                    <button
                      disabled
                      title={BURN_DISABLED_REASON}
                      style={btnStyle(W.err, false, false, true)}
                    >🔥 Burn</button>
                    <button onClick={() => setModal({ kind: "accept" })} style={btnStyle(W.warn, false)}>✓ Accept transfer</button>
                  </span>
                </div>
                <div style={{ color: W.dim, fontSize: 12, marginTop: 4, fontFamily: wMono }}>
                  admin {shortParty(active.admin)} · id {active.instrument_id}
                </div>

                <h4 style={{ color: W.text2, margin: "18px 0 8px" }}>
                  Holdings <span style={{ color: W.dim, fontWeight: 400, fontSize: 12 }}>· a balance is the sum of its Holding contracts — click a row to expand</span>
                </h4>
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
                            {shortParty(h.party)}
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
              );
            })()}
          </div>
        </div>
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
// (BIT-219). As the user fills From + Amount, it dry-runs the transfer
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
      await transferToken(instance, symbol, from, to, amount, reason || undefined);
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
                {plan.change !== "0.0000000000" && (
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

// MatrixLens — the god-mode party × instrument balance table (BIT-219 /
// BIT-215 #2). One ACS scan; rows = parties, columns = instruments,
// plus a totals row. Only the parties the role's JWT can read appear.
function MatrixLens({ matrix, err }: { matrix: BalanceMatrix | null; err: string | null }) {
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
              <td style={td}>{shortParty(p)}</td>
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
      setErr(e instanceof ApiError ? e.message : "create failed");
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
