import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  acceptTransfer,
  burnToken,
  createToken,
  fetchHoldings,
  fetchTokens,
  mintToken,
  transferToken,
  type TokenHolding,
  type TokenRef,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

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

  const [list, setList] = useState<TokenRef[]>([]);
  const [listErr, setListErr] = useState<string | null>(null);
  const [refreshTick, setRefreshTick] = useState(0);
  const [activeSymbol, setActiveSymbol] = useState<string | null>(null);

  const [holdings, setHoldings] = useState<TokenHolding[]>([]);
  const [holdingsErr, setHoldingsErr] = useState<string | null>(null);

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
    fetchTokens(instance)
      .then((r) => {
        if (cancelled) return;
        setList(r.tokens);
        setListErr(null);
        if (r.tokens.length > 0 && (!activeSymbol || !r.tokens.find((t) => t.symbol === activeSymbol))) {
          setActiveSymbol(r.tokens[0].symbol);
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

  useEffect(() => {
    if (!instance || !activeSymbol) {
      setHoldings([]);
      return;
    }
    let cancelled = false;
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
    () => list.find((t) => t.symbol === activeSymbol) ?? null,
    [list, activeSymbol],
  );

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

      {list.length === 0 ? (
        <div style={{ color: W.dim, fontSize: 13 }}>
          No instruments recorded yet on <code>{instance}</code>. Click <b>Create token</b> above
          (or run <code>dpm localnet token create --instance {instance}</code>).
        </div>
      ) : (
        <div style={{ display: "grid", gridTemplateColumns: "320px 1fr", gap: 14 }}>
          {/* Left rail: instrument list */}
          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10, overflow: "hidden" }}>
            {list.map((t) => {
              const isActive = t.symbol === activeSymbol;
              return (
                <button
                  key={t.symbol}
                  onClick={() => setActiveSymbol(t.symbol)}
                  style={{
                    display: "block",
                    width: "100%",
                    textAlign: "left",
                    padding: "10px 14px",
                    background: isActive ? W.surface2 : "transparent",
                    border: "none",
                    borderLeft: `2px solid ${isActive ? W.brand : "transparent"}`,
                    cursor: "pointer",
                  }}
                >
                  <div style={{ fontWeight: 600, fontSize: 13, color: W.text }}>
                    {t.symbol} <span style={{ color: W.dim, fontWeight: 400 }}>· {t.name}</span>
                  </div>
                  <div style={{ color: W.dim, fontSize: 11, marginTop: 2 }}>
                    supply {t.initial_supply} · {t.decimals}d · {t.status}
                  </div>
                </button>
              );
            })}
          </div>

          {/* Right pane: detail + holdings + actions */}
          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10, padding: 16 }}>
            {active && (
              <>
                <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                  <h3 style={{ color: W.text, margin: 0 }}>{active.name}</h3>
                  <span style={{ color: W.dim, fontFamily: wMono, fontSize: 12 }}>
                    {active.symbol} · {active.instrument_id.slice(0, 12)}…
                  </span>
                  <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
                    <button onClick={() => setModal({ kind: "mint", symbol: active.symbol })} style={btnStyle(W.brand, false)}>↑ Mint</button>
                    <button onClick={() => setModal({ kind: "transfer", symbol: active.symbol })} style={btnStyle(W.brand, false)}>→ Transfer</button>
                    <button onClick={() => setModal({ kind: "burn", symbol: active.symbol })} style={btnStyle(W.err, false)}>🔥 Burn</button>
                    <button onClick={() => setModal({ kind: "accept" })} style={btnStyle(W.warn, false)}>✓ Accept transfer</button>
                  </span>
                </div>
                <div style={{ color: W.dim, fontSize: 12, marginTop: 4 }}>
                  Issuer {active.issuer_party} · created {active.created_at}
                </div>
                <h4 style={{ color: W.text2, margin: "18px 0 8px" }}>Holdings</h4>
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
                      <tr key={i}>
                        <td style={td}>{h.party}</td>
                        <td style={{ ...td, fontFamily: wMono }}>{h.amount}</td>
                      </tr>
                    ))}
                    {holdings.length === 0 && (
                      <tr><td colSpan={2} style={{ ...td, color: W.dim }}>No holdings yet.</td></tr>
                    )}
                  </tbody>
                </table>
              </>
            )}
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
        <ActionModal
          title={`Transfer ${modal.symbol}`}
          fields={[
            { label: "From party", key: "from" },
            { label: "To party", key: "to" },
            { label: "Amount", key: "amount" },
            { label: "Reason (optional)", key: "reason", optional: true },
          ]}
          onClose={() => setModal(null)}
          submit={(v) => transferToken(instance, modal.symbol, v.from, v.to, v.amount, v.reason || undefined)}
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

function btnStyle(accent: string, busy: boolean, filled = false): React.CSSProperties {
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
