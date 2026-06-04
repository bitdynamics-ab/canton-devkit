// Token model — three lenses over one dataset:
//   TokenDetailScreen     · instrument-first (Overview/Holders/Activity + action rail)
//   HoldingsMatrixScreen  · parties × tokens heatmap (reconciliation view)
//   PartyHoldingsScreen   · holder-first, expands a balance into its Holding contracts
//   TransferActionScreen  · Mint/Transfer/Burn slide-over (the shared action model)
//
// Canton-specific truth baked in: a party's balance of a token is the SUM of
// multiple Holding contracts (UTXO-style), and every balance is projected
// through an explicit (participant, party) pair.

// ── shared bits ──────────────────────────────────────────────────────────
const TOK = {
  CC:   { sym:'CC',   name:'Canton Coin',  color:'#F5BF55', standard:'Splice Amulet', dec:10 },
  RTK:  { sym:'RTK',  name:'Retail Token', color:'#5BD7C5', standard:'CIP-0112 v2',   dec:2  },
  USDX: { sym:'USDX', name:'Stable Test',  color:'#7CB5F7', standard:'CIP-0112 v2',   dec:6  },
  GEM:  { sym:'GEM',  name:'Game Gem',     color:'#C4A8F5', standard:'CIP-0112 v2',   dec:0  },
};

function TokenBadge({ t, size = 32 }) {
  const info = TOK[t] || { sym:t, color: W.dim };
  return (
    <span style={{
      width:size, height:size, borderRadius: size*0.3,
      background:`linear-gradient(135deg, ${info.color}, ${W.brand})`,
      color:'#0B0E13', display:'inline-flex', alignItems:'center', justifyContent:'center',
      fontWeight:700, fontSize: size*0.34, letterSpacing:-0.3, flex:'0 0 auto',
      boxShadow:`0 4px 12px -5px ${info.color}aa`,
    }}>{info.sym}</span>
  );
}

function PartyDot({ name, size = 22 }) {
  const colors = { 'app-provider':'#5BD7C5', 'app-user':'#7CB5F7', sv:'#C4A8F5', DSO:'#F5BF55', alice:'#F08FB5', bob:'#E8A14E' };
  const c = colors[name] || W.dim;
  return (
    <span style={{
      width:size, height:size, borderRadius:'50%',
      background:`linear-gradient(135deg, ${c}, ${W.brand})`,
      color:'#0B0E13', display:'inline-flex', alignItems:'center', justifyContent:'center',
      fontWeight:700, fontSize:size*0.42, flex:'0 0 auto',
    }}>{name[0].toUpperCase()}</span>
  );
}

function Tabs({ items, active }) {
  return (
    <div style={{display:'flex', gap:2, borderBottom:`1px solid ${W.border}`}}>
      {items.map(it => (
        <div key={it} style={{
          padding:'9px 16px', fontSize:12.5, cursor:'pointer',
          fontWeight: it === active ? 600 : 500,
          color: it === active ? W.text : W.dim,
          borderBottom: it === active ? `2px solid ${W.brand}` : '2px solid transparent',
          marginBottom:-1,
        }}>{it}</div>
      ))}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────
// LENS 1 · TOKEN DETAIL
// ─────────────────────────────────────────────────────────────────────────
function HolderRow({ party, participant, balance, pct, contracts, color, last }) {
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'1.4fr 1.2fr 1fr 1.4fr .8fr',
      gap:14, padding:'10px 16px', alignItems:'center',
      borderBottom: last ? 'none' : `1px solid ${W.border}`, cursor:'pointer',
    }}>
      <div style={{display:'flex', alignItems:'center', gap:10}}>
        <PartyDot name={party} />
        <div>
          <div style={{fontWeight:600, fontSize:12.5}}>{party}</div>
          <Mono c={W.dim}>party::{party === 'DSO' ? '1220dso0' : '1220'}…</Mono>
        </div>
      </div>
      <Mono c={W.text2}>{participant}</Mono>
      <div style={{fontFamily:wMono, fontSize:13, fontWeight:600}}>{balance}</div>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <div style={{flex:1, height:6, background:W.surface2, borderRadius:3, overflow:'hidden', maxWidth:120}}>
          <div style={{width:`${pct}%`, height:'100%', background:color, borderRadius:3}} />
        </div>
        <span style={{color:W.dim, fontSize:11.5, fontFamily:wMono, width:42}}>{pct}%</span>
      </div>
      <div style={{textAlign:'right'}}>
        <span className="w-pill" style={{background:W.surface2, color:W.text2, border:`1px solid ${W.border}`, fontFamily:wMono, fontSize:11}}>{contracts} cts</span>
      </div>
    </div>
  );
}

function ActionField({ label, children, hint }) {
  return (
    <div style={{marginBottom:12}}>
      <label style={{fontSize:10.5, color:W.dim, letterSpacing:1.2, textTransform:'uppercase', fontWeight:600, display:'block', marginBottom:5}}>{label}</label>
      {children}
      {hint && <div style={{color:W.dim, fontSize:11, marginTop:5, lineHeight:1.4}}>{hint}</div>}
    </div>
  );
}

function Selectish({ children, icon }) {
  return (
    <div style={{
      display:'flex', alignItems:'center', gap:8, padding:'8px 11px',
      background:W.bg, border:`1px solid ${W.border}`, borderRadius:7,
      fontSize:12.5, color:W.text, cursor:'pointer',
    }}>
      {icon}
      <span style={{flex:1}}>{children}</span>
      <span style={{color:W.dim}}>⌄</span>
    </div>
  );
}

function TokenDetailScreen() {
  const info = TOK.RTK;
  return (
    <AppShell active="tokens" instance="hubble"
      topRight={<>
        <button className="w-btn">‹ All tokens</button>
        <button className="w-btn">Export holders CSV</button>
        <button className="w-btn primary"><span>+</span> Mint</button>
      </>}>
      {/* Identity header */}
      <section style={{display:'flex', alignItems:'flex-start', gap:16, marginBottom:16}}>
        <TokenBadge t="RTK" size={52} />
        <div style={{flex:1}}>
          <div style={{display:'flex', alignItems:'center', gap:10, flexWrap:'wrap'}}>
            <h1 style={{margin:0, fontSize:22, fontWeight:600, letterSpacing:-0.5}}>{info.name}</h1>
            <span style={{fontFamily:wMono, color:W.dim, fontSize:14}}>{info.sym}</span>
            <Pill color={W.brand}>{info.standard}</Pill>
            <Pill color={W.ok}><Dot color={W.ok} pulse /> live</Pill>
          </div>
          <div style={{color:W.dim, fontSize:12.5, marginTop:4, fontFamily:wMono}}>
            issuer <span style={{color:W.text2}}>app-provider</span> · package <span style={{color:W.text2}}>retail-token@1.1.0</span> · decimals {info.dec} · admin party::1220a8d2…f7c1
          </div>
        </div>
      </section>

      {/* KPI row */}
      <div style={{display:'grid', gridTemplateColumns:'repeat(4,1fr)', gap:14, marginBottom:16}}>
        <Kpi label="Total supply"   value="1,000,000" spark={[700,760,820,900,950,980,1000,1000,1000,1000]} sparkColor={info.color} />
        <Kpi label="In circulation" value="1,000,000" unit="100%" trend="all minted" trendColor={W.dim} spark={[0,200,400,600,800,900,950,1000,1000,1000]} sparkColor={W.info} />
        <Kpi label="Holders"        value="4" unit="parties" trend="▲ 1 / 1h" spark={[1,1,2,2,3,3,3,4,4,4]} sparkColor={W.brand} />
        <Kpi label="Holding contracts" value="9" unit="UTXOs" trend="across 4 parties" trendColor={W.dim} spark={[3,4,5,5,6,7,7,8,9,9]} sparkColor={W.mag} />
      </div>

      <div style={{display:'grid', gridTemplateColumns:'1fr 360px', gap:14, alignItems:'start'}}>
        {/* Left — tabbed: holders */}
        <Card pad={0}>
          <div style={{padding:'0 4px'}}>
            <Tabs items={['Overview','Holders','Activity','Upgrades']} active="Holders" />
          </div>
          <div style={{padding:'12px 16px', display:'flex', alignItems:'center', gap:10}}>
            <span style={{fontWeight:600, fontSize:13}}>Distribution</span>
            <span style={{color:W.dim, fontSize:11.5}}>4 parties · summed across 9 Holding contracts</span>
            <div style={{marginLeft:'auto', display:'flex', gap:6}}>
              <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>balances</span>
              <span className="w-pill" style={{background:'transparent', color:W.dim, border:`1px solid ${W.border}`}}>contracts</span>
            </div>
          </div>
          <div style={{
            display:'grid', gridTemplateColumns:'1.4fr 1.2fr 1fr 1.4fr .8fr',
            gap:14, padding:'8px 16px', color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600,
            borderBottom:`1px solid ${W.border}`, borderTop:`1px solid ${W.border}`,
          }}>
            <span>Party</span><span>Participant</span><span>Balance</span><span>Share of supply</span><span style={{textAlign:'right'}}>Held in</span>
          </div>
          <HolderRow party="app-provider" participant="app-provider-part." balance="998,950" pct={99} contracts={3} color={info.color} />
          <HolderRow party="app-user"     participant="app-user-part."     balance="450"     pct={1}  contracts={3} color={info.color} />
          <HolderRow party="sv"           participant="sv-participant"     balance="100"     pct={1}  contracts={2} color={info.color} />
          <HolderRow party="alice"        participant="app-user-part."     balance="500"     pct={1}  contracts={1} color={info.color} last />
          <div style={{padding:'10px 16px', color:W.dim, fontSize:11.5, display:'flex', justifyContent:'space-between', borderTop:`1px solid ${W.border}`}}>
            <span>Σ 1,000,000 RTK · 9 contracts</span>
            <span>click a holder → its Holding contracts</span>
          </div>
        </Card>

        {/* Right — action rail */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card pad={0}>
            <div style={{display:'flex', padding:4, gap:4, background:W.surface2, margin:12, borderRadius:8}}>
              {['Mint','Transfer','Burn'].map((a,i) => (
                <button key={a} style={{
                  flex:1, padding:'6px', borderRadius:6, border:'none', cursor:'pointer', fontSize:12, fontWeight:600,
                  fontFamily:wSans,
                  background: i === 0 ? W.brand : 'transparent',
                  color: i === 0 ? '#082018' : W.dim,
                }}>{a}</button>
              ))}
            </div>
            <div style={{padding:'0 16px 16px'}}>
              <ActionField label="Mint to">
                <Selectish icon={<PartyDot name="app-user" size={18} />}>app-user <span style={{color:W.dim}}>· party::1220c4f1…</span></Selectish>
              </ActionField>
              <ActionField label="Amount" hint="issued by app-provider · decimals 2">
                <div style={{display:'flex', alignItems:'center', gap:8, padding:'8px 11px', background:W.bg, border:`1px solid ${W.brand}55`, borderRadius:7, boxShadow:`0 0 0 3px ${W.brand}10`}}>
                  <span style={{flex:1, fontFamily:wMono, fontSize:14, fontWeight:600}}>1,000.00<span className="term-cursor" style={{background:W.brand}}/></span>
                  <span style={{color:W.dim, fontFamily:wMono, fontSize:12}}>RTK</span>
                </div>
              </ActionField>
              <ActionField label="Memo (optional)">
                <Selectish>"workshop allocation"</Selectish>
              </ActionField>
              <button className="w-btn primary" style={{width:'100%', justifyContent:'center', marginTop:4, padding:'9px'}}>
                Mint 1,000 RTK → app-user
              </button>
              <div style={{marginTop:10, padding:'8px 10px', background:W.surface2, borderRadius:6}}>
                <Mono c={W.dim}>dpm localnet token mint RTK 1000 --to app-user</Mono>
              </div>
            </div>
          </Card>

          <Card title="Recent · RTK only" pad={0}>
            {[
              ['MINT','+50 → app-user','2m', W.amber],
              ['TRANSFER','500 app-prov → app-user','5m', W.info],
              ['BURN','−12 app-user','18m', W.err],
              ['MINT','+100 → sv','1h', W.amber],
            ].map((r,i) => (
              <div key={i} style={{display:'flex', alignItems:'center', gap:10, padding:'9px 14px', borderBottom: i<3?`1px solid ${W.border}`:'none'}}>
                <span style={{color:r[3], fontWeight:600, fontSize:11, letterSpacing:0.3, width:64}}>{r[0]}</span>
                <span style={{flex:1, fontSize:12, fontFamily:wMono, color:W.text2}}>{r[1]}</span>
                <Mono c={W.dim}>{r[2]}</Mono>
              </div>
            ))}
          </Card>
        </div>
      </div>
    </AppShell>
  );
}

// ─────────────────────────────────────────────────────────────────────────
// LENS 2 · HOLDINGS MATRIX  (parties × tokens)
// ─────────────────────────────────────────────────────────────────────────
function MatrixCell({ value, intensity, sym, color }) {
  if (value === null) {
    return <div style={{padding:'12px 14px', textAlign:'right', color:W.faint, fontFamily:wMono, fontSize:12}}>·</div>;
  }
  return (
    <div className="w-row" style={{
      padding:'12px 14px', textAlign:'right', cursor:'pointer',
      background: `${color}${Math.round(intensity*40).toString(16).padStart(2,'0')}`,
      borderRadius:6,
    }}>
      <div style={{fontFamily:wMono, fontSize:12.5, fontWeight:600, color:W.text}}>{value}</div>
      <div style={{fontFamily:wMono, fontSize:10, color:W.dim, marginTop:1}}>{sym}</div>
    </div>
  );
}

function HoldingsMatrixScreen() {
  // rows = parties grouped by participant; cols = tokens
  const cols = ['CC','RTK','USDX','GEM'];
  const groups = [
    { participant:'app-provider-participant', parties:[
      { name:'app-provider', cells:[{v:'400.00',i:1,},{v:'998,950',i:1},{v:'0',i:0},{v:'1,200',i:0.8}] },
    ]},
    { participant:'app-user-participant', parties:[
      { name:'app-user', cells:[{v:'12.50',i:0.3},{v:'450',i:0.2},{v:'5,000.00',i:1},{v:'300',i:0.3}] },
      { name:'alice',    cells:[{v:'2.10',i:0.1},{v:'500',i:0.2},{v:'1,000.00',i:0.4},{v:null}] },
      { name:'bob',      cells:[{v:null},{v:null},{v:'250.00',i:0.1},{v:'80',i:0.1}] },
    ]},
    { participant:'sv-participant', parties:[
      { name:'sv',  cells:[{v:'0.01',i:0.05},{v:'100',i:0.1},{v:null},{v:null}] },
      { name:'DSO', cells:[{v:'∞ mint',i:0.6},{v:null},{v:null},{v:null}] },
    ]},
  ];
  const gridCols = '200px repeat(4, 1fr)';
  return (
    <AppShell active="tokens" instance="hubble"
      topRight={<>
        <button className="w-btn">balances ⌄</button>
        <button className="w-btn">Export CSV</button>
        <button className="w-btn primary"><span>+</span> Transfer</button>
      </>}>
      <section style={{marginBottom:14, display:'flex', alignItems:'flex-end', justifyContent:'space-between', gap:16, flexWrap:'wrap'}}>
        <div>
          <div style={{display:'flex', alignItems:'center', gap:10, marginBottom:4}}>
            <h1 style={{margin:0, fontSize:20, fontWeight:600, letterSpacing:-0.4}}>Holdings Matrix</h1>
            <Pill color={W.dim}>6 parties × 4 tokens</Pill>
          </div>
          <div style={{color:W.dim, fontSize:12.5}}>Every party that holds anything, across every instrument on <span style={{color:W.text2}}>hubble</span>. Cell shading = share of that token's supply.</div>
        </div>
        <div style={{display:'flex', gap:8, alignItems:'center'}}>
          <span style={{color:W.dim, fontSize:11.5}}>view</span>
          <div style={{display:'flex', background:W.surface2, borderRadius:7, padding:3, border:`1px solid ${W.border}`}}>
            <button style={{padding:'5px 11px', fontSize:11.5, borderRadius:5, border:'none', background:W.brand, color:'#082018', fontWeight:600}}>Balances</button>
            <button style={{padding:'5px 11px', fontSize:11.5, borderRadius:5, border:'none', background:'transparent', color:W.dim}}>Contracts</button>
            <button style={{padding:'5px 11px', fontSize:11.5, borderRadius:5, border:'none', background:'transparent', color:W.dim}}>% supply</button>
          </div>
        </div>
      </section>

      <Card pad={0}>
        {/* Header row */}
        <div style={{display:'grid', gridTemplateColumns:gridCols, gap:8, padding:'12px 16px', borderBottom:`1px solid ${W.border}`, alignItems:'center'}}>
          <span style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600}}>Party ╲ Token</span>
          {cols.map(c => (
            <div key={c} style={{display:'flex', alignItems:'center', gap:8, justifyContent:'flex-end'}}>
              <TokenBadge t={c} size={24} />
              <div style={{textAlign:'right'}}>
                <div style={{fontSize:12, fontWeight:600}}>{c}</div>
                <div style={{color:W.dim, fontSize:10, fontFamily:wMono}}>{TOK[c].standard.split(' ')[0]}</div>
              </div>
            </div>
          ))}
        </div>

        {/* Groups */}
        {groups.map((g, gi) => (
          <div key={gi}>
            <div style={{padding:'7px 16px', background:W.bg, borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:8}}>
              <span style={{width:6, height:6, borderRadius:'50%', background:W.brand}} />
              <Mono c={W.text2}>{g.participant}</Mono>
              <span style={{color:W.dim, fontSize:11}}>· {g.parties.length} {g.parties.length===1?'party':'parties'}</span>
            </div>
            {g.parties.map((p, pi) => (
              <div key={pi} style={{display:'grid', gridTemplateColumns:gridCols, gap:8, padding:'6px 12px', alignItems:'center', borderBottom:`1px solid ${W.border}`}}>
                <div style={{display:'flex', alignItems:'center', gap:9, paddingLeft:4}}>
                  <PartyDot name={p.name} />
                  <div>
                    <div style={{fontWeight:600, fontSize:12.5}}>{p.name}</div>
                    <Mono c={W.dim}>party::{p.name==='DSO'?'1220dso0':'1220'}…</Mono>
                  </div>
                </div>
                {p.cells.map((cell, ci) => (
                  <MatrixCell key={ci} value={cell.v} intensity={cell.i || 0} sym={cols[ci]} color={TOK[cols[ci]].color} />
                ))}
              </div>
            ))}
          </div>
        ))}

        {/* Totals */}
        <div style={{display:'grid', gridTemplateColumns:gridCols, gap:8, padding:'12px 16px', background:W.bg, alignItems:'center'}}>
          <span style={{color:W.brand, fontSize:11, letterSpacing:1, textTransform:'uppercase', fontWeight:700}}>Total supply</span>
          {[['412.21','CC'],['1,000,000','RTK'],['6,500.00','USDX'],['1,860','GEM']].map(([v,s],i)=>(
            <div key={i} style={{textAlign:'right'}}>
              <div style={{fontFamily:wMono, fontSize:13, fontWeight:700, color:W.text}}>{v}</div>
              <div style={{fontFamily:wMono, fontSize:10, color:W.dim}}>{s}</div>
            </div>
          ))}
        </div>
      </Card>

      <div style={{display:'flex', gap:14, marginTop:12, alignItems:'center', color:W.dim, fontSize:11.5}}>
        <span>Shading</span>
        <span style={{display:'flex', alignItems:'center', gap:6}}>
          low <div style={{width:80, height:8, background:`linear-gradient(90deg, ${W.brand}10, ${W.brand}88)`, borderRadius:4}} /> high share of supply
        </span>
        <span style={{marginLeft:'auto'}}>· <Mono c={W.text2}>·</Mono> = party does not hold this token (no Holding contract visible to its participant)</span>
      </div>
    </AppShell>
  );
}

// ─────────────────────────────────────────────────────────────────────────
// LENS 3 · PARTY HOLDINGS  (holder-first, expand into Holding contracts)
// ─────────────────────────────────────────────────────────────────────────
function HoldingContract({ cid, amount, sym, createdTx, age, locked, last }) {
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'24px 1.2fr 1fr 1.3fr .8fr',
      gap:12, padding:'8px 16px 8px 40px', alignItems:'center',
      borderBottom: last ? 'none' : `1px solid ${W.border}`,
      background: W.bg,
    }}>
      <span style={{color:W.faint, fontFamily:wMono, fontSize:11}}>└</span>
      <Mono c={W.mag}>{cid}</Mono>
      <div style={{fontFamily:wMono, fontSize:12.5, fontWeight:600}}>{amount} <span style={{color:W.dim, fontWeight:400}}>{sym}</span></div>
      <Mono c={W.dim}>from {createdTx}</Mono>
      <div style={{textAlign:'right'}}>
        {locked
          ? <span className="w-pill" style={{background:`${W.warn}1A`, color:W.warn, border:`1px solid ${W.warn}44`, fontSize:10.5}}>locked</span>
          : <span style={{color:W.dim, fontSize:11}}>{age}</span>}
      </div>
    </div>
  );
}

function HoldingGroup({ t, balance, contractCount, expanded, children }) {
  const info = TOK[t];
  return (
    <div style={{borderBottom:`1px solid ${W.border}`}}>
      <div className="w-row" style={{
        display:'grid', gridTemplateColumns:'24px 1.6fr 1fr 1fr auto',
        gap:12, padding:'11px 16px', alignItems:'center', cursor:'pointer',
        background: expanded ? W.surface2 : 'transparent',
      }}>
        <span style={{color:W.brand, transform: expanded?'rotate(90deg)':'none', display:'inline-block', transition:'.15s', fontSize:11}}>▸</span>
        <div style={{display:'flex', alignItems:'center', gap:10}}>
          <TokenBadge t={t} size={28} />
          <div>
            <div style={{fontWeight:600, fontSize:13}}>{info.name}</div>
            <Mono c={W.dim}>{info.standard} · decimals {info.dec}</Mono>
          </div>
        </div>
        <div style={{fontFamily:wMono, fontSize:14, fontWeight:600}}>{balance} <span style={{color:W.dim, fontWeight:400, fontSize:11}}>{info.sym}</span></div>
        <div>
          <span className="w-pill" style={{background:W.surface2, color:W.text2, border:`1px solid ${W.border}`, fontFamily:wMono, fontSize:11}}>
            {contractCount} Holding {contractCount===1?'contract':'contracts'}
          </span>
        </div>
        <div style={{display:'flex', gap:6, justifyContent:'flex-end'}}>
          <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5}}>Send</button>
          <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5}}>Burn</button>
        </div>
      </div>
      {expanded && children}
    </div>
  );
}

function PartyHoldingsScreen() {
  return (
    <AppShell active="tokens" instance="hubble"
      topRight={<>
        <button className="w-btn">Switch party ⌄</button>
        <button className="w-btn">Copy party ID</button>
        <button className="w-btn primary"><span>↗</span> Open wallet</button>
      </>}>
      {/* Party identity */}
      <section style={{display:'flex', alignItems:'center', gap:14, marginBottom:14}}>
        <PartyDot name="app-user" size={46} />
        <div style={{flex:1}}>
          <div style={{display:'flex', alignItems:'center', gap:10, flexWrap:'wrap'}}>
            <h1 style={{margin:0, fontSize:21, fontWeight:600, letterSpacing:-0.4}}>app-user</h1>
            <Pill color={W.info}>hosted on app-user-participant</Pill>
            <Pill color={W.ok}><Dot color={W.ok} pulse /> ready</Pill>
          </div>
          <div style={{color:W.dim, fontSize:12.5, marginTop:3, fontFamily:wMono}}>
            party::1220c4f1c89a4e2b…aa90 · projected through app-user-participant
          </div>
        </div>
        <div style={{textAlign:'right'}}>
          <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600}}>Holds</div>
          <div style={{fontSize:22, fontWeight:600, letterSpacing:-0.4}}>4 <span style={{color:W.dim, fontSize:13, fontWeight:500}}>tokens · 9 contracts</span></div>
        </div>
      </section>

      {/* Projection note */}
      <div style={{
        display:'flex', alignItems:'center', gap:10, padding:'9px 14px', marginBottom:14,
        background:`${W.info}0E`, border:`1px solid ${W.info}33`, borderRadius:8,
        fontSize:12, color:W.text2,
      }}>
        <span style={{color:W.info}}>ⓘ</span>
        <span>A balance is the <strong>sum of multiple Holding contracts</strong> (UTXO-style). Expand a token to see the individual contracts a transfer would draw from.</span>
      </div>

      <Card pad={0} title="Holdings" subtitle="grouped by instrument · expand for contract-level detail">
        <HoldingGroup t="USDX" balance="5,000.00" contractCount={4} expanded>
          <HoldingContract cid="0x91aa01…" amount="2,000.00" sym="USDX" createdTx="0xc1d2…" age="2m" />
          <HoldingContract cid="0x91aa02…" amount="2,000.00" sym="USDX" createdTx="0xc1d2…" age="2m" />
          <HoldingContract cid="0x91aa03…" amount="800.00"   sym="USDX" createdTx="0xa933…" age="14m" />
          <HoldingContract cid="0x91aa04…" amount="200.00"   sym="USDX" createdTx="0xa933…" age="14m" locked last />
        </HoldingGroup>
        <HoldingGroup t="RTK" balance="450" contractCount={3} />
        <HoldingGroup t="CC"  balance="12.50" contractCount={1} />
        <HoldingGroup t="GEM" balance="300" contractCount={1} />
      </Card>

      <div style={{color:W.dim, fontSize:11.5, marginTop:10, display:'flex', justifyContent:'space-between'}}>
        <span>CLI: <Mono c={W.text2}>dpm localnet token balance --party app-user --json</Mono></span>
        <span>locked = held by an active proposal / allocation</span>
      </div>
    </AppShell>
  );
}

// ─────────────────────────────────────────────────────────────────────────
// SHARED ACTION MODEL · Transfer slide-over (also Mint/Burn)
// ─────────────────────────────────────────────────────────────────────────
function TransferSlideOver() {
  return (
    <div style={{position:'absolute', inset:0, zIndex:20}}>
      <div style={{position:'absolute', inset:0, background:'rgba(7,10,14,0.5)', backdropFilter:'blur(3px)'}} />
      <div style={{
        position:'absolute', top:0, right:0, bottom:0, width:440,
        background:W.surface, borderLeft:`1px solid ${W.borderHi}`,
        boxShadow:'-30px 0 60px -20px rgba(0,0,0,.6)',
        display:'flex', flexDirection:'column',
      }}>
        {/* header */}
        <div style={{padding:'16px 20px', borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:12}}>
          <TokenBadge t="RTK" size={32} />
          <div style={{flex:1}}>
            <div style={{fontWeight:600, fontSize:14.5}}>Transfer RTK</div>
            <div style={{color:W.dim, fontSize:11.5}}>Retail Token · CIP-0112 v2</div>
          </div>
          <button className="w-iconbtn">✕</button>
        </div>

        {/* tabs */}
        <div style={{display:'flex', padding:'10px 20px 0', gap:4}}>
          {['Mint','Transfer','Burn'].map((a,i)=>(
            <button key={a} style={{
              padding:'7px 16px', borderRadius:'7px 7px 0 0', border:'none', cursor:'pointer',
              fontSize:12.5, fontWeight:600, fontFamily:wSans,
              background: a==='Transfer'? W.bg : 'transparent',
              color: a==='Transfer'? W.text : W.dim,
              borderBottom: a==='Transfer'? `2px solid ${W.brand}` : '2px solid transparent',
            }}>{a}</button>
          ))}
        </div>

        <div className="w-scroll" style={{flex:1, overflow:'auto', padding:'18px 20px', background:W.bg}}>
          <ActionField label="From party" hint="draws from 3 Holding contracts">
            <Selectish icon={<PartyDot name="app-provider" size={18}/>}>app-provider <span style={{color:W.dim}}>· 998,950 RTK</span></Selectish>
          </ActionField>

          {/* directional arrow */}
          <div style={{display:'flex', justifyContent:'center', margin:'2px 0'}}>
            <span style={{
              width:28, height:28, borderRadius:'50%', background:W.surface2, border:`1px solid ${W.border}`,
              display:'flex', alignItems:'center', justifyContent:'center', color:W.brand, fontSize:14,
            }}>↓</span>
          </div>

          <ActionField label="To party">
            <Selectish icon={<PartyDot name="app-user" size={18}/>}>app-user <span style={{color:W.dim}}>· party::1220c4f1…</span></Selectish>
          </ActionField>

          <ActionField label="Amount">
            <div style={{display:'flex', alignItems:'center', gap:8, padding:'10px 12px', background:W.surface, border:`1px solid ${W.brand}55`, borderRadius:7, boxShadow:`0 0 0 3px ${W.brand}10`}}>
              <span style={{flex:1, fontFamily:wMono, fontSize:18, fontWeight:600}}>500.00<span className="term-cursor" style={{background:W.brand}}/></span>
              <button className="w-btn" style={{padding:'3px 9px', fontSize:11}}>max</button>
              <span style={{color:W.dim, fontFamily:wMono, fontSize:13}}>RTK</span>
            </div>
          </ActionField>

          <ActionField label="Memo (optional)">
            <Selectish>"settlement pay-001"</Selectish>
          </ActionField>

          {/* coin-selection preview — the Canton-specific bit */}
          <div style={{background:W.surface, border:`1px solid ${W.border}`, borderRadius:8, padding:'12px 14px', marginTop:6}}>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600, marginBottom:8}}>Coin selection preview</div>
            <div style={{display:'flex', flexDirection:'column', gap:6, fontFamily:wMono, fontSize:11.5}}>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.text2}}>0x65aa…  consume</span><span style={{color:W.err}}>−500,000</span></div>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.text2}}>→ app-user create</span><span style={{color:W.ok}}>+500</span></div>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.text2}}>→ app-provider change</span><span style={{color:W.ok}}>+499,500</span></div>
              <div style={{height:1, background:W.border, margin:'2px 0'}} />
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.dim}}>est. CC fee</span><span style={{color:W.text2}}>0.03 CC</span></div>
            </div>
          </div>
        </div>

        {/* footer */}
        <div style={{padding:'14px 20px', borderTop:`1px solid ${W.border}`}}>
          <button className="w-btn primary" style={{width:'100%', justifyContent:'center', padding:'10px', marginBottom:8}}>
            Transfer 500 RTK → app-user
          </button>
          <Mono c={W.dim}>dpm localnet token transfer RTK 500 --from app-provider --to app-user</Mono>
        </div>
      </div>
    </div>
  );
}

function TransferActionScreen() {
  return <div style={{position:'relative', width:'100%', height:'100%'}}><TokenDetailScreen /><TransferSlideOver /></div>;
}

Object.assign(window, {
  TokenDetailScreen, HoldingsMatrixScreen, PartyHoldingsScreen, TransferActionScreen,
});
