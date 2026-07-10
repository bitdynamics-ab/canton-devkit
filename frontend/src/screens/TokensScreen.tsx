import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  acceptTransfer,
  aliasMapFrom,
  burnToken,
  createParty,
  createToken,
  faucetToken,
  launchDemoToken,
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
import { W, wMono, tableCaps, wideCaps, tint, R, FAST } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";
import {
  Dot,
  IcArrowRight,
  IcArrowUp,
  IcBolt,
  IcCheck,
  IcChevronDown,
  IcChevronRight,
  IcDroplet,
  IcFlame,
  IcPlus,
  IcX,
} from "../components/icons";

function shortParty(p: string): string {
  const i = p.indexOf("::");
  return i > 0 ? p.slice(0, i) : p;
}

// Prefers a registered alias over the `::`-prefix fallback.
function partyLabel(aliases: AliasMap, p: string): string {
  return aliases[p] ?? shortParty(p);
}

// Capability guards keyed on the machine generation tag, not the display
// label: mint requires a native V2 (CIP-0112) instrument created on-ledger.
export function mintDisabledReason(t: InstrumentRef): string | null {
  if (t.generation !== "v2")
    return `${t.symbol} (${t.standard}) has no standard mint. Use the asset's wallet UI.`;
  if (!t.on_ledger)
    return `${t.symbol} is recorded only. Create it on-ledger first.`;
  return null;
}
const BURN_DISABLED_REASON =
  "Burn is only available on a native CIP-0112 v2 token created on this " +
  "instance. Amulet has no burn surface.";

// Remediation for the on-ledger create 412 (TEST_TOKEN_DAR_UNAVAILABLE).
const TOKEN_DAR_UNAVAILABLE_HINT =
  "The test-token DAR isn't published for this instance's Splice version, so on-ledger " +
  "V2 tokens can't be created here. Bring up a token-standard-v2 instance " +
  "(localnet up --version token-standard-v2 --profile tokens-v2) and re-run.";

export function createErrorText(e: unknown): string {
  if (e instanceof ApiError && e.code === "TEST_TOKEN_DAR_UNAVAILABLE") {
    return TOKEN_DAR_UNAVAILABLE_HINT;
  }
  return e instanceof ApiError ? e.message : "create failed";
}

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
  // "ledger" = real on-ledger balances; "registry" = pseudo-balance fallback
  // when no live participant is reachable. Drives the disclaimer banner.
  const [holdingsSource, setHoldingsSource] = useState<HoldingSource>("ledger");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [contracts, setContracts] = useState<HoldingContract[]>([]);
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
    | { kind: "accept"; id?: string; party?: string }
    | null
  >(null);
  const [topNotice, setTopNotice] = useState<{ tone: "ok" | "warn" | "err"; text: string } | null>(null);
  const [demoBusy, setDemoBusy] = useState(false);

  useEffect(() => {
    if (!instance) {
      setList([]);
      setActiveSymbol(null);
      return;
    }
    let cancelled = false;
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

  // Best-effort: a failure just hides the KPI strip; holdings still load.
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

  // Lazy: only fetched when the Activity tab is open, since it's a full
  // historical scan, heavier than the other lenses' ACS snapshots.
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

  // Each click bumps expandSeq; the in-flight closure bails when its seq
  // no longer matches, so a stale fetch can't overwrite a newer click.
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

  // Server composes issuer-party → create → mint → faucet-a-holder.
  async function launchDemo() {
    if (!instance) return;
    setDemoBusy(true);
    setTopNotice(null);
    try {
      const res = await launchDemoToken(instance);
      setActiveSymbol(res.token.symbol);
      bump();
      setTopNotice({
        tone: "ok",
        text: res.seeded
          ? `Launched ${res.token.symbol}. Supply minted to ${res.issuer.alias}, ${res.holder?.alias ?? "a holder"} funded. Try a transfer.`
          : `Launched ${res.token.symbol}. Supply minted to ${res.issuer.alias}.`,
      });
    } catch (e) {
      setTopNotice(renderActionError(e, "demo launch failed"));
    } finally {
      setDemoBusy(false);
    }
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
          <Button variant="secondary" size="sm" onClick={() => setShowParties(true)}>
            Parties{parties.length > 0 ? ` (${parties.length})` : ""}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            icon={<IcPlus />}
            onClick={() => setShowCreate(true)}
          >
            Create token
          </Button>
          <Button
            variant={view !== "matrix" && list.length === 0 ? "secondary" : "primary"}
            size="sm"
            icon={<IcBolt />}
            disabled={demoBusy}
            onClick={() => void launchDemo()}
            title="Provision a live, transferable demo token in one click"
          >
            {demoBusy ? "Launching…" : "Launch demo"}
          </Button>
        </span>
      } />

      {topNotice && (
        <div role="status" style={notice(topNotice.tone)}>{topNotice.text}</div>
      )}

      <div style={{ display: "flex", gap: 4, background: W.surface2, borderRadius: 4, padding: 3, width: "fit-content", border: `1px solid ${W.border}` }}>
        {(["instruments", "matrix"] as const).map((v) => (
          <button
            key={v}
            onClick={() => setView(v)}
            style={{
              padding: "5px 14px", fontSize: 12, borderRadius: 2, border: "none", cursor: "pointer",
              fontWeight: 600,
              background: view === v ? W.brand : "transparent",
              color: view === v ? W.onAccent : W.dim,
            }}
          >
            {v === "instruments" ? "Instruments" : "Holdings matrix"}
          </button>
        ))}
      </div>

      {view === "matrix" ? (
        <MatrixLens matrix={matrix} err={matrixErr} aliases={aliases} />
      ) : list.length === 0 ? (
        <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 4, padding: 24, textAlign: "center" }}>
          <div style={{ color: W.text, fontSize: 15, fontWeight: 600, marginBottom: 4 }}>
            No tokens on <code>{instance}</code> yet
          </div>
          <div style={{ color: W.dim, fontSize: 13, marginBottom: 16 }}>
            Go from empty to a live, transferable token in one click. No party ids to paste.
          </div>
          <div style={{ display: "flex", gap: 10, justifyContent: "center", flexWrap: "wrap" }}>
            <Button variant="primary" size="md" icon={<IcBolt />} disabled={demoBusy} onClick={() => void launchDemo()}>
              {demoBusy ? "Launching demo…" : "Launch demo token"}
            </Button>
            <Button variant="secondary" size="md" icon={<IcPlus />} onClick={() => setShowCreate(true)}>
              Create token manually
            </Button>
          </div>
          <div style={{ color: W.faint, fontSize: 11.5, marginTop: 14 }}>
            One click provisions an issuer party, a DEMO instrument with supply, and a funded holder.
            Or run <code>dpm localnet token demo --instance {instance}</code>.
          </div>
        </div>
      ) : (
        <div style={{ display: "grid", gridTemplateColumns: "320px 1fr", gap: 14 }}>
          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 4, overflow: "hidden" }}>
            {list.map((t) => {
              const sym = t.symbol ?? t.instrument_id;
              const isActive = sym === activeSymbol;
              return (
                <button
                  key={sym}
                  onClick={() => setActiveSymbol(sym)}
                  style={{
                    display: "block", width: "100%", textAlign: "left", padding: "10px 14px",
                    background: isActive ? tint(W.brand, 12) : "transparent", border: "none",
                    cursor: "pointer",
                    transition: `background-color ${FAST}`,
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

          <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 4, padding: 16 }}>
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
                    <Button
                      variant="secondary"
                      size="sm"
                      icon={<IcArrowUp />}
                      onClick={() => setModal({ kind: "mint", symbol: sym })}
                      disabled={!!mintReason}
                      title={mintReason ?? "Mint new supply"}
                    >Mint</Button>
                    <Button variant="secondary" size="sm" icon={<IcArrowRight />} onClick={() => setModal({ kind: "transfer", symbol: sym })}>Transfer</Button>
                    <Button variant="secondary" size="sm" icon={<IcDroplet />} onClick={() => setModal({ kind: "faucet", symbol: sym })} title="Fund a party from a funded source (auto-accepted)">Faucet</Button>
                    <Button
                      variant="danger"
                      size="sm"
                      icon={<IcFlame />}
                      onClick={() => setModal({ kind: "burn", symbol: sym })}
                      disabled={!!mintReason}
                      title={mintReason ? BURN_DISABLED_REASON : "Burn holdings (archive path)"}
                    >Burn</Button>
                    <Button variant="secondary" size="sm" icon={<IcCheck />} onClick={() => setModal({ kind: "accept" })}>Accept transfer</Button>
                  </span>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 6, color: W.dim, fontSize: 12, marginTop: 4, fontFamily: wMono }}>
                  <span>admin {partyLabel(aliases, active.admin)}</span>
                  <span>· id</span>
                  <MonoId value={active.instrument_id} size={12} color={W.dim} />
                </div>

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

                    <h4 style={{ color: W.text2, margin: "16px 0 8px" }}>
                      Holdings <span style={{ color: W.dim, fontWeight: 400, fontSize: 12 }}>· a balance sums its Holding contracts. Click a row to expand.</span>
                    </h4>
                    {holdingsSource === "registry" && (
                      <div role="status" style={{ ...notice("warn"), marginBottom: 8 }}>
                        No live ledger reachable for <code>{instance}</code>. These are
                        <b> registry pseudo-balances</b>. Local bookkeeping that shows the issuer
                        holding the full supply and everyone else zero, <b>not</b> on-ledger holdings.
                        Start the instance to see real balances.
                      </div>
                    )}
                    {holdingsErr && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{holdingsErr}</div>}
                    <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
                      <thead>
                        <tr style={{ color: W.dim, textAlign: "left" }}>
                          <th style={th}>PARTY</th>
                          <th style={thNum}>AMOUNT</th>
                        </tr>
                      </thead>
                      <tbody>
                        {holdings.map((h, i) => (
                          <>
                            <tr
                              key={i}
                              onClick={() => toggleExpand(h.party)}
                              style={{ cursor: "pointer", background: expanded === h.party ? tint(W.brand, 12) : "transparent", transition: `background-color ${FAST}` }}
                            >
                              <td style={td}>
                                <span style={{ color: W.brand, display: "inline-flex", alignItems: "center", width: 16, verticalAlign: "middle" }}>
                                  {expanded === h.party ? <IcChevronDown size={12} /> : <IcChevronRight size={12} />}
                                </span>
                                {partyLabel(aliases, h.party)}
                              </td>
                              <td style={tdNum}>{h.amount}</td>
                            </tr>
                            {expanded === h.party && contracts.map((c) => (
                              <tr key={c.contract_id} style={{ background: W.bg }}>
                                <td style={{ ...td, paddingLeft: 34, fontSize: 11 }}>
                                  <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                                    <span style={{ color: W.dim, fontFamily: wMono }}>└</span>
                                    <MonoId value={c.contract_id} size={11} color={W.mag} />
                                    {c.locked && (
                                      <span style={{ display: "inline-flex", alignItems: "center", gap: 4, color: W.warn, fontSize: 10, border: `1px solid ${tint(W.warn, 34)}`, background: tint(W.warn, 13), borderRadius: R.control, padding: "0 5px", height: 16 }}>
                                        <Dot color={W.warn} size={5} /> Locked
                                      </span>
                                    )}
                                  </span>
                                </td>
                                <td style={{ ...tdNum, color: W.text2 }}>{c.amount}</td>
                              </tr>
                            ))}
                            {expanded === h.party && contracts.length === 0 && (
                              <tr><td colSpan={2} style={{ ...td, paddingLeft: 34, color: W.dim, fontSize: 11 }}>Loading contracts…</td></tr>
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
          parties={parties}
          onPartiesChanged={() => bump()}
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
          fields={[{ label: "To party", key: "to", party: true }, { label: "Amount", key: "amount" }]}
          instance={instance}
          parties={parties}
          onPartiesChanged={() => bump()}
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
          parties={parties}
          onPartiesChanged={() => bump()}
          onClose={() => setModal(null)}
          onDone={(offered) => {
            bump();
            if (offered) {
              // Hand the id to a prefilled Accept modal so the receiver settles it.
              setTopNotice({ tone: "ok", text: `Transfer offered. Accept instruction ${offered.instructionId.slice(0, 12)}… to settle it.` });
              setModal({ kind: "accept", id: offered.instructionId, party: offered.receiver });
            } else {
              setModal(null);
            }
          }}
          onError={(e) => setTopNotice(renderActionError(e, "transfer failed"))}
        />
      )}
      {modal?.kind === "burn" && active && (
        <ActionModal
          title={`Burn ${modal.symbol}`}
          fields={[{ label: "From party", key: "from", party: true }, { label: "Amount", key: "amount" }]}
          instance={instance}
          parties={parties}
          onPartiesChanged={() => bump()}
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
            { label: "To party", key: "to", party: true },
            { label: "Amount", key: "amount" },
            { label: "Source (optional, defaults to funded party)", key: "source", optional: true, party: true },
          ]}
          instance={instance}
          parties={parties}
          onPartiesChanged={() => bump()}
          onClose={() => setModal(null)}
          submit={(v) => faucetToken(instance, modal.symbol, v.to, v.amount, v.source || undefined)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "faucet failed"))}
        />
      )}
      {modal?.kind === "accept" && (
        <ActionModal
          title="Accept transfer"
          initial={{ id: modal.id ?? "", party: modal.party ?? "" }}
          fields={[
            { label: "Transfer instruction id", key: "id" },
            { label: "Receiver party", key: "party", party: true },
          ]}
          instance={instance}
          parties={parties}
          onClose={() => setModal(null)}
          submit={(v) => acceptTransfer(instance, v.id, v.party || undefined)}
          onDone={() => { setModal(null); bump(); }}
          onError={(e) => setTopNotice(renderActionError(e, "accept failed"))}
        />
      )}
    </section>
  );
}

// From/To/Amount plus a live coin-selection preview: dry-runs the transfer
// as From + Amount fill in, showing which Holding contracts get consumed.
function TransferModal({
  instance, symbol, parties, onPartiesChanged, onClose, onDone, onError,
}: {
  instance: string;
  symbol: string;
  parties: PartyRef[];
  onPartiesChanged?: () => void;
  onClose: () => void;
  // offered set when a pending TransferInstruction needs routing to Accept;
  // undefined when the transfer already settled.
  onDone: (offered?: { instructionId: string; receiver: string }) => void;
  onError: (e: unknown) => void;
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [autoAccept, setAutoAccept] = useState(true);
  const [busy, setBusy] = useState(false);
  const [plan, setPlan] = useState<import("../api").TransferPlan | null>(null);

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
      const res = await transferToken(instance, symbol, from, to, amount, reason || undefined, undefined, autoAccept);
      // A non-auto-accept Offer returns an instruction id; anything settled just closes.
      onDone(!res.settled && res.transferInstructionId
        ? { instructionId: res.transferInstructionId, receiver: to }
        : undefined);
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title={`Transfer ${symbol}`} onClose={onClose}>
      <form onSubmit={onSubmit} style={{ display: "grid", gap: 10 }}>
        <Field label="From party">
          <PartyPicker instance={instance} parties={parties} value={from} onChange={setFrom} onPartiesChanged={onPartiesChanged} placeholder="Select sender" />
        </Field>
        <Field label="To party">
          <PartyPicker instance={instance} parties={parties} value={to} onChange={setTo} onPartiesChanged={onPartiesChanged} placeholder="Select recipient" />
        </Field>
        <Field label="Amount"><input value={amount} onChange={(e) => setAmount(e.target.value)} style={input} required /></Field>
        <Field label="Reason (optional)"><input value={reason} onChange={(e) => setReason(e.target.value)} style={input} /></Field>
        <label style={{ display: "flex", alignItems: "center", gap: 8, color: W.text2, fontSize: 12, cursor: "pointer" }}>
          <input type="checkbox" checked={autoAccept} onChange={(e) => setAutoAccept(e.target.checked)} />
          Auto-accept (settle in one step. You own the receiver on LocalNet.)
        </label>

        {plan && (
          <div style={{ background: W.surface2, border: `1px solid ${W.border}`, borderRadius: 4, padding: "10px 12px" }}>
            <div style={{ color: W.dim, fontSize: 12, fontWeight: 500, marginBottom: 6 }}>
              Coin selection preview
            </div>
            {plan.sufficient ? (
              <div style={{ display: "grid", gap: 4, fontFamily: wMono, fontSize: 11.5 }}>
                {plan.inputs.map((i) => (
                  <div key={i.contract_id} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
                    <span style={{ display: "inline-flex", alignItems: "center", gap: 6, color: W.text2 }}>
                      <MonoId value={i.contract_id} size={11.5} color={W.text2} /> consume
                    </span>
                    <span style={{ color: W.err, fontVariantNumeric: "tabular-nums" }}>−{i.amount}</span>
                  </div>
                ))}
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: W.text2 }}>→ {shortParty(to || from)} receive</span>
                  <span style={{ color: W.ok, fontVariantNumeric: "tabular-nums" }}>+{amount}</span>
                </div>
                {Number(plan.change) > 0 && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: W.text2 }}>→ {shortParty(from)} change</span>
                    <span style={{ color: W.ok, fontVariantNumeric: "tabular-nums" }}>+{plan.change}</span>
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
          <Button variant="ghost" size="md" onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            size="md"
            type="submit"
            disabled={busy || !from || !to || !amount || (plan ? !plan.sufficient : false)}
          >
            {busy ? "Submitting…" : "Transfer"}
          </Button>
        </div>
      </form>
    </ModalShell>
  );
}

// KPI strip from one ACS scan. Circulating == total supply on a UTXO ledger.
function KpiRow({ s }: { s: InstrumentSummary }) {
  const cards: Array<{ label: string; value: string; full?: string; hint?: string }> = [
    { label: "Total supply", value: statAmount(s.total_supply), full: s.total_supply },
    { label: "In circulation", value: statAmount(s.total_supply), full: s.total_supply, hint: "sum of all holdings" },
    { label: "Holders", value: String(s.holder_count) },
    { label: "Holding contracts", value: String(s.contract_count), hint: "UTXOs" },
  ];
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))",
        gap: 12,
        margin: "16px 0 4px",
      }}
    >
      {cards.map((c) => (
        <div
          key={c.label}
          style={{
            background: W.bg,
            border: `1px solid ${W.border}`,
            borderRadius: 4,
            padding: "10px 12px",
          }}
        >
          <div style={{ ...wideCaps, color: W.dim, fontSize: 11, marginBottom: 6 }}>
            {c.label}
          </div>
          <div
            title={c.full && c.full !== c.value ? c.full : undefined}
            style={{
              color: W.text,
              fontSize: 20,
              fontFamily: wMono,
              fontVariantNumeric: "tabular-nums",
              // Ellipsize an oversized value (full precision in the title).
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {c.value}
          </div>
          {c.hint && <div style={{ color: W.dim, fontSize: 10, marginTop: 2 }}>{c.hint}</div>}
        </div>
      ))}
    </div>
  );
}

// Group thousands, cap at two decimals; non-numeric strings pass through.
function statAmount(raw: string): string {
  const n = Number(raw);
  if (!Number.isFinite(n)) return raw;
  return n.toLocaleString("en-US", { maximumFractionDigits: 2 });
}

// Per-holder stake table (balance, share, UTXO count); backend sorts biggest-first.
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
            <th style={thNum}>BALANCE</th>
            <th style={th}>SHARE</th>
            <th style={thNum}>UTXOS</th>
          </tr>
        </thead>
        <tbody>
          {s.holders.map((h) => {
            const pct = Number(h.pct_of_supply) || 0;
            return (
              <tr key={h.party}>
                <td style={td}>{partyLabel(aliases, h.party)}</td>
                <td style={tdNum}>{h.balance}</td>
                <td style={td}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <div style={{ flex: 1, height: 6, background: W.surface2, borderRadius: 2, minWidth: 40 }}>
                      <div
                        style={{
                          width: `${Math.min(100, pct)}%`,
                          height: "100%",
                          background: W.brand,
                          borderRadius: 2,
                        }}
                      />
                    </div>
                    <span style={{ fontFamily: wMono, fontVariantNumeric: "tabular-nums", color: W.text2, fontSize: 12, minWidth: 44, textAlign: "right" }}>
                      {h.pct_of_supply}%
                    </span>
                  </div>
                </td>
                <td style={{ ...tdNum, color: W.text2 }}>{h.contract_count}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}

// Transfer/mint/burn history from the ledger stream; one netted transaction per row.
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
  const kindLabel: Record<ActivityEvent["kind"], string> = {
    mint: "Mint",
    burn: "Burn",
    transfer: "Transfer",
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
          <th style={thNum}>AMOUNT</th>
          <th style={th}>FROM</th>
          <th style={th}>TO</th>
        </tr>
      </thead>
      <tbody>
        {events.map((e) => (
          <tr key={e.offset}>
            <td style={{ ...td, color: W.dim, fontSize: 11, fontFamily: wMono, fontVariantNumeric: "tabular-nums" }}>
              {e.record_time ? e.record_time.replace("T", " ").slice(0, 19) : `@${e.offset}`}
            </td>
            <td style={td}>
              <span
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 5,
                  color: tone[e.kind],
                  border: `1px solid ${tint(tone[e.kind], 34)}`,
                  background: tint(tone[e.kind], 13),
                  borderRadius: R.control,
                  padding: "1px 7px",
                  fontSize: 11,
                }}
              >
                <Dot color={tone[e.kind]} size={5} />
                {kindLabel[e.kind]}
              </span>
            </td>
            <td style={tdNum}>{e.amount}</td>
            <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{fmtParties(e.senders)}</td>
            <td style={{ ...td, fontFamily: wMono, color: W.text2 }}>{fmtParties(e.receivers)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// Party × instrument balance table from one ACS scan; only parties the
// role's JWT can read appear.
function MatrixLens({ matrix, err, aliases }: { matrix: BalanceMatrix | null; err: string | null; aliases: AliasMap }) {
  if (err) return <div role="alert" style={{ color: W.err, fontSize: 13 }}>{err}</div>;
  if (!matrix) return <div style={{ color: W.dim, fontSize: 13 }}>Scanning ACS…</div>;

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
    <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: 4, padding: 16, overflowX: "auto" }}>
      <div style={{ color: W.dim, fontSize: 12, marginBottom: 10 }}>
        {parties.length} {parties.length === 1 ? "party" : "parties"} × {syms.length} {syms.length === 1 ? "instrument" : "instruments"}.
        Every readable party's balance of every instrument, in one ACS scan.
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
                <td key={s} style={{ ...tdNum, color: amt[p]?.[s] ? W.text : W.dim }}>
                  {amt[p]?.[s] ?? "·"}
                </td>
              ))}
            </tr>
          ))}
          <tr>
            <td style={{ ...td, color: W.dim, fontWeight: 600, fontSize: 12 }}>Σ total</td>
            {syms.map((s) => (
              <td key={s} style={{ ...tdNum, fontWeight: 600 }}>{totals[s] ?? ""}</td>
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
      <span style={{ color: W.dim, fontSize: 12 }}>Token Standard instruments + actions</span>
      <span style={{ marginLeft: "auto" }}>{right}</span>
    </header>
  );
}

// List/allocate/forget aliased parties. New parties are immediately visible
// in the matrix/activity (the scan grants read-as for every registered party).
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
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() => remove(p.alias)}
                  title="Forget this alias (the on-ledger party persists)"
                >
                  forget
                </Button>
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
          style={{ flex: 1, background: W.bg, border: `1px solid ${W.border}`, borderRadius: 2, padding: "8px 10px", color: W.text, fontSize: 13 }}
        />
        <Button variant="primary" size="md" type="submit" icon={<IcPlus />} disabled={busy || !alias.trim()}>
          {busy ? "…" : "Allocate"}
        </Button>
      </form>
    </ModalShell>
  );
}

function CreateTokenModal({
  instance,
  parties,
  onPartiesChanged,
  onClose,
  onCreated,
}: {
  instance: string;
  parties: PartyRef[];
  onPartiesChanged?: () => void;
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
    if (!issuer) {
      setErr("pick or create an issuer party");
      return;
    }
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
        <Field label="Issuer party">
          <PartyPicker
            instance={instance}
            parties={parties}
            value={issuer}
            onChange={setIssuer}
            onPartiesChanged={onPartiesChanged}
            placeholder="Select issuer party"
          />
        </Field>
        {err && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{err}</div>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6 }}>
          <Button variant="ghost" size="md" onClick={onClose}>Cancel</Button>
          <Button variant="primary" size="md" type="submit" disabled={busy}>{busy ? "Creating…" : "Create"}</Button>
        </div>
      </form>
    </ModalShell>
  );
}

function ActionModal({
  title,
  fields,
  instance,
  parties,
  onPartiesChanged,
  initial,
  onClose,
  submit,
  onDone,
  onError,
}: {
  title: string;
  fields: { label: string; key: string; optional?: boolean; party?: boolean }[];
  instance: string;
  parties: PartyRef[];
  onPartiesChanged?: () => void;
  initial?: Record<string, string>;
  onClose: () => void;
  submit: (values: Record<string, string>) => Promise<void>;
  onDone: () => void;
  onError: (e: unknown) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(initial ?? {});
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
            {f.party ? (
              <PartyPicker
                instance={instance}
                parties={parties}
                value={values[f.key] ?? ""}
                onChange={(v) => setValues((vv) => ({ ...vv, [f.key]: v }))}
                onPartiesChanged={onPartiesChanged}
                placeholder={f.optional ? "Select party (optional)" : "Select party"}
              />
            ) : (
              <input
                value={values[f.key] ?? ""}
                onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                style={input}
                required={!f.optional}
              />
            )}
          </Field>
        ))}
        {err && <div role="alert" style={{ color: W.err, fontSize: 12 }}>{err}</div>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6 }}>
          <Button variant="ghost" size="md" onClick={onClose}>Cancel</Button>
          <Button variant="primary" size="md" type="submit" disabled={busy}>{busy ? "Submitting…" : "Submit"}</Button>
        </div>
      </form>
    </ModalShell>
  );
}

// Alias-aware party selector: pick a registered alias, create one inline,
// or type a raw id. Always emits the resolved party_id (ResolveAlias passes
// it through unchanged, so it's correct on both create and action paths).
function PartyPicker({
  instance,
  parties,
  value,
  onChange,
  role = "app-user",
  onPartiesChanged,
  placeholder = "Select party",
}: {
  instance: string;
  parties: PartyRef[];
  value: string;
  onChange: (partyId: string) => void;
  role?: string;
  onPartiesChanged?: () => void;
  placeholder?: string;
}) {
  const [mode, setMode] = useState<"select" | "create" | "raw">("select");
  const [newAlias, setNewAlias] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // Locally-created parties show instantly, before the parent's refetch lands.
  const [extra, setExtra] = useState<PartyRef[]>([]);

  const all = useMemo(() => {
    const seen = new Set(parties.map((p) => p.party_id));
    return [...parties, ...extra.filter((e) => !seen.has(e.party_id))];
  }, [parties, extra]);
  const known = all.some((p) => p.party_id === value);

  async function createNew() {
    const a = newAlias.trim();
    if (!a) return;
    setBusy(true);
    setErr(null);
    try {
      const ref = await createParty(instance, a, role);
      setExtra((x) => [...x, ref]);
      onChange(ref.party_id);
      onPartiesChanged?.();
      setNewAlias("");
      setMode("select");
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "could not create party");
    } finally {
      setBusy(false);
    }
  }

  if (mode === "create") {
    return (
      <div style={{ display: "grid", gap: 6 }}>
        <div style={{ display: "flex", gap: 6 }}>
          <input
            autoFocus
            value={newAlias}
            onChange={(e) => setNewAlias(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void createNew();
              }
            }}
            placeholder="new alias (e.g. bob)"
            style={{ ...input, flex: 1 }}
          />
          <Button
            variant="secondary"
            size="sm"
            disabled={busy || !newAlias.trim()}
            onClick={() => void createNew()}
          >
            {busy ? "…" : "Create"}
          </Button>
        </div>
        {err && <span role="alert" style={{ color: W.err, fontSize: 11 }}>{err}</span>}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => { setMode("select"); setErr(null); }}
          style={{ justifySelf: "start" }}
        >
          back to list
        </Button>
      </div>
    );
  }

  if (mode === "raw" || (value !== "" && !known)) {
    return (
      <div style={{ display: "grid", gap: 4 }}>
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="party id (alias::fingerprint)"
          style={{ ...input, fontFamily: wMono, fontSize: 12 }}
        />
        {all.length > 0 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => { onChange(""); setMode("select"); }}
            style={{ justifySelf: "start" }}
          >
            pick a registered party
          </Button>
        )}
      </div>
    );
  }

  return (
    <select
      value={value}
      onChange={(e) => {
        const v = e.target.value;
        if (v === "__create__") { setMode("create"); return; }
        if (v === "__raw__") { setMode("raw"); onChange(""); return; }
        onChange(v);
      }}
      style={{ ...input, cursor: "pointer" }}
    >
      <option value="">{placeholder}</option>
      {all.map((p) => (
        <option key={p.party_id} value={p.party_id}>
          {p.alias} · {shortParty(p.party_id)}
        </option>
      ))}
      <option value="__create__">＋ create new party…</option>
      <option value="__raw__">↳ enter raw party id…</option>
    </select>
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
        borderRadius: 8, padding: 18, width: 420, maxWidth: "92vw",
      }}>
        <div style={{ display: "flex", alignItems: "center", marginBottom: 12 }}>
          <h3 style={{ color: W.text, margin: 0, fontSize: 14 }}>{title}</h3>
          <Button
            variant="ghost"
            size="sm"
            icon={<IcX />}
            aria-label="close"
            onClick={onClose}
            style={{ marginLeft: "auto" }}
          />
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
  borderRadius: 2, padding: "6px 8px", fontSize: 13,
};
const th: React.CSSProperties = { ...tableCaps, padding: "6px 10px", borderBottom: `1px solid ${W.border}`, fontSize: 11 };
const td: React.CSSProperties = { padding: "6px 10px", borderBottom: `1px solid ${W.border}`, color: W.text };
const thNum: React.CSSProperties = { ...th, textAlign: "right" };
const tdNum: React.CSSProperties = { ...td, textAlign: "right", fontFamily: wMono, fontVariantNumeric: "tabular-nums" };

function notice(tone: "ok" | "warn" | "err"): React.CSSProperties {
  const c = tone === "ok" ? W.ok : tone === "warn" ? W.warn : W.err;
  return {
    background: tint(c, 10), color: c, border: `1px solid ${tint(c, 40)}`,
    borderRadius: R.control, padding: "8px 12px", fontSize: 12.5,
  };
}
