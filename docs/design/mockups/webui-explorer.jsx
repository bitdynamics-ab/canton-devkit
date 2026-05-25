// Contract & transaction Explorer — projection selector, live ACS,
// transaction timeline, contract detail drawer.

function ProjectionBar() {
  return (
    <section style={{
      background: W.surface, border: `1px solid ${W.border}`, borderRadius: 10,
      padding: '12px 16px', marginBottom: 14,
      display:'grid', gridTemplateColumns:'auto auto 1fr auto auto', gap: 16, alignItems:'center',
    }}>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <span style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600}}>Projecting through</span>
      </div>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <span className="w-pill" style={{background:W.surface2, border:`1px solid ${W.borderHi}`, color:W.text, fontFamily:wMono, fontSize:11.5, padding:'5px 10px'}}>
          <span style={{color:W.dim}}>participant</span> <span style={{color:W.brand}}>app-provider</span> <span style={{color:W.dim}}>⌄</span>
        </span>
        <span style={{color:W.dim}}>as</span>
        <span className="w-pill" style={{background:W.surface2, border:`1px solid ${W.borderHi}`, color:W.text, fontFamily:wMono, fontSize:11.5, padding:'5px 10px'}}>
          <Dot color="#5BD7C5" /> <span style={{marginLeft:2, color:W.text}}>app-provider</span> <span style={{color:W.dim, fontSize:11}}>party::1220a8d2…</span> <span style={{color:W.dim}}>⌄</span>
        </span>
        <button className="w-btn" style={{padding:'4px 8px', fontSize:11.5}}>swap as bob</button>
      </div>
      <div style={{borderLeft:`1px solid ${W.border}`, paddingLeft:16, color:W.dim, fontSize:11.5, lineHeight:1.4}}>
        <span style={{color:W.text, fontWeight:600}}>1,402</span> active contracts visible to this party · <span style={{color:W.text}}>3,841</span> events in window
      </div>
      {/* View toggle */}
      <div style={{display:'flex', background:W.surface2, borderRadius:8, padding:3, border:`1px solid ${W.border}`}}>
        <button style={{padding:'5px 12px', fontSize:12, borderRadius:5, border:'none', background:W.brand, color:'#082018', fontWeight:600}}>Contracts</button>
        <button style={{padding:'5px 12px', fontSize:12, borderRadius:5, border:'none', background:'transparent', color:W.dim}}>Transactions</button>
        <button style={{padding:'5px 12px', fontSize:12, borderRadius:5, border:'none', background:'transparent', color:W.dim}}>Timeline</button>
      </div>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <Pill color={W.ok}><Dot color={W.ok} pulse /> live · 2s</Pill>
        <button className="w-iconbtn" title="Pause">⏸</button>
        <button className="w-iconbtn" title="Bookmark">⌗</button>
      </div>
    </section>
  );
}

function FilterChip({ label, color, count, active }) {
  return (
    <div className="w-row" style={{
      display:'flex', alignItems:'center', gap:8, padding:'6px 9px',
      borderRadius:6, cursor:'pointer',
      background: active ? W.surface2 : 'transparent',
      borderLeft: active ? `2px solid ${color || W.brand}` : '2px solid transparent',
      paddingLeft: 9,
    }}>
      <span style={{ width:8, height:8, borderRadius:2, background: color || W.dim }} />
      <span style={{flex:1, fontSize:12.5, color: active ? W.text : W.text2, fontWeight: active ? 600 : 500}}>{label}</span>
      <span style={{color:W.dim, fontSize:11.5, fontFamily:wMono}}>{count}</span>
    </div>
  );
}

function AcsRow({ tpl, cid, party, sigObs, age, amount, sym, active, archived }) {
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'1.8fr 1.2fr 1fr .8fr .8fr',
      gap:14, padding:'9px 14px', alignItems:'center',
      background: active ? W.selRow : 'transparent',
      borderLeft: active ? `2px solid ${W.brand}` : '2px solid transparent',
      paddingLeft: active ? 12 : 14,
      borderBottom:`1px solid ${W.border}`, cursor:'pointer',
      opacity: archived ? 0.4 : 1, textDecoration: archived ? 'line-through' : 'none',
    }}>
      <div style={{display:'flex', alignItems:'center', gap:8, minWidth:0}}>
        <span style={{width:6, height:6, borderRadius:'50%', background: archived ? W.err : W.ok}} />
        <span style={{fontSize:12.5, fontWeight: active ? 600 : 500, overflow:'hidden', textOverflow:'ellipsis', whiteSpace:'nowrap'}}>{tpl}</span>
      </div>
      <Mono c={W.mag}>{cid}</Mono>
      <Mono c={W.text2}>{party}</Mono>
      <div style={{fontFamily:wMono, fontSize:12, color:W.text}}>{amount} <span style={{color:W.dim}}>{sym}</span></div>
      <div style={{display:'flex', alignItems:'center', gap:8, justifyContent:'space-between'}}>
        <span style={{color:W.dim, fontSize:11.5}}>{age}</span>
        <span style={{color:W.dim, fontSize:11.5}}>{sigObs}</span>
      </div>
    </div>
  );
}

function TxTreeNode({ kind, tpl, cid, args, witnesses, indent = 0, last }) {
  const c = { Create: W.ok, Exercise: W.info, Archive: W.err, Fetch: W.dim }[kind] || W.dim;
  return (
    <div style={{paddingLeft: indent * 16, marginBottom: 4}}>
      <div style={{display:'flex', alignItems:'baseline', gap:8}}>
        {indent > 0 && <span style={{color:W.faint, marginLeft:-12, fontFamily:wMono}}>{last ? '└─' : '├─'}</span>}
        <span style={{color: c, fontWeight:600, fontSize:12}}>{kind}</span>
        <span style={{color: W.text, fontFamily: wMono, fontSize:12}}>{tpl}</span>
        <span style={{color: W.mag, fontFamily: wMono, fontSize:11}}>{cid}</span>
      </div>
      {args && <div style={{paddingLeft: 28, color:W.text2, fontFamily:wMono, fontSize:11.5, marginTop:1}}>
        <span style={{color:W.dim}}>args  </span>{args}
      </div>}
      {witnesses && <div style={{paddingLeft: 28, color:W.dim, fontFamily:wMono, fontSize:11.5, marginTop:1}}>
        <span style={{color:W.dim}}>witnesses  </span><span style={{color:W.text2}}>{witnesses}</span>
      </div>}
    </div>
  );
}

function ExplorerScreen() {
  return (
    <AppShell active="explorer" instance="hubble"
      topRight={<>
        <button className="w-btn"><span style={{color:W.brand}}>⌗</span> Saved (4)</button>
        <button className="w-btn">Share view</button>
        <button className="w-btn">Export CSV</button>
      </>}>
      <section style={{marginBottom:14}}>
        <h1 style={{margin:'0 0 3px', fontSize:20, fontWeight:600, letterSpacing:-0.4}}>Explorer</h1>
        <div style={{color:W.dim, fontSize:12.5}}>Live Active Contract Set, transaction history, and per-party visibility.</div>
      </section>

      <ProjectionBar />

      <div style={{display:'grid', gridTemplateColumns:'232px 1fr 380px', gap:14, alignItems:'start'}}>
        {/* Filters */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card title="Templates" subtitle="8 in view" pad={8}>
            <FilterChip label="Retail.Token.Token" color="#5BD7C5" count={142} active />
            <FilterChip label="Token.TokenProposal" color="#7CB5F7" count={18} />
            <FilterChip label="Token.Mint"          color="#E8A14E" count={4} />
            <FilterChip label="Onboarding.AppRequest" color="#C4A8F5" count={6} />
            <FilterChip label="Splice.Amulet"      color="#F5BF55" count={1208} />
            <FilterChip label="DSO.ScheduledTask"  color="#F08FB5" count={22} />
            <FilterChip label="CNS.Registration"   color="#62E2A0" count={2} />
          </Card>
          <Card title="Parties" subtitle="filter visibility" pad={8}>
            <FilterChip label="app-provider" color="#5BD7C5" count={142} active />
            <FilterChip label="app-user"     color="#7CB5F7" count={86} />
            <FilterChip label="sv"           color="#C4A8F5" count={1208} />
            <FilterChip label="DSO"          color="#F5BF55" count={1402} />
          </Card>
          <Card title="Time range" pad={10}>
            <div style={{display:'flex', flexDirection:'column', gap:6}}>
              <div style={{display:'flex', gap:6}}>
                <button className="w-btn" style={{flex:1, padding:'5px', fontSize:11.5}}>Live</button>
                <button className="w-btn" style={{flex:1, padding:'5px', fontSize:11.5}}>5m</button>
                <button className="w-btn primary" style={{flex:1, padding:'5px', fontSize:11.5}}>1h</button>
                <button className="w-btn" style={{flex:1, padding:'5px', fontSize:11.5}}>24h</button>
              </div>
              <div style={{color:W.dim, fontSize:11, fontFamily:wMono, marginTop:4}}>
                offsets 0/1240 → 0/3812
              </div>
            </div>
          </Card>
        </div>

        {/* Center — ACS table */}
        <Card pad={0}
          title="Active Contract Set" subtitle="142 contracts · streaming creates and archives"
          action={
            <div style={{display:'flex', gap:8, alignItems:'center'}}>
              <input placeholder="filter cid, payload, party…" style={{
                background:W.bg, border:`1px solid ${W.border}`, color:W.text, fontFamily:wSans,
                fontSize:12, padding:'5px 10px', borderRadius:6, width:220,
              }} />
              <span className="w-kbd">/</span>
            </div>
          }>
          <div style={{
            display:'grid', gridTemplateColumns:'1.8fr 1.2fr 1fr .8fr .8fr',
            gap:14, padding:'9px 14px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
            borderBottom:`1px solid ${W.border}`,
          }}>
            <span>Template</span><span>Cid</span><span>Owner / signatory</span><span>Amount</span>
            <span style={{display:'flex', justifyContent:'space-between'}}><span>Age</span><span>Sig · Obs</span></span>
          </div>
          <div className="w-flash"><AcsRow tpl="Retail.Token.Token" cid="0x77c1ab2e…" party="app-user"    amount="50"  sym="RTK" age="0:02s"  sigObs="1·2" /></div>
          <AcsRow tpl="Retail.Token.Token"     cid="0x77bfac21…" party="app-user"     amount="500" sym="RTK" age="0:09s"  sigObs="1·2" active />
          <AcsRow tpl="Retail.Token.Token"     cid="0x77beae09…" party="app-provider" amount="120" sym="USD" age="0:13s"  sigObs="1·2" />
          <AcsRow tpl="Retail.Token.Token"     cid="0x77bd1c4f…" party="app-provider" amount="500" sym="RTK" age="0:13s"  sigObs="1·2" archived />
          <AcsRow tpl="Token.TokenProposal"    cid="0x77c0fa3a…" party="app-user"     amount="50"  sym="RTK" age="0:06s"  sigObs="2·0" />
          <AcsRow tpl="Retail.Token.Token"     cid="0x65aa9911…" party="app-provider" amount="999,450" sym="RTK" age="2m 14s" sigObs="1·1" />
          <AcsRow tpl="Splice.Amulet"          cid="0x4a01bb22…" party="app-provider" amount="412.21" sym="CC" age="3m 02s" sigObs="1·1" />
          <AcsRow tpl="Splice.Amulet"          cid="0x4a01cc33…" party="app-user"     amount="12.50" sym="CC" age="4m 18s" sigObs="1·1" />
          <AcsRow tpl="Splice.AmuletRules"     cid="0x33fbaa01…" party="DSO"          amount="—" sym="" age="1h ago" sigObs="1·∞" />
          <AcsRow tpl="CNS.Registration"       cid="0x88aabb01…" party="app-provider" amount="hubble.dso" sym="" age="1h ago" sigObs="1·1" />
          <div style={{padding:'10px 14px', color:W.dim, fontSize:11.5, display:'flex', justifyContent:'space-between'}}>
            <span>Showing 10 of 142 · streaming</span>
            <span>↑↓ navigate · ↵ open · / focus search · b bookmark</span>
          </div>
        </Card>

        {/* Right — Detail drawer */}
        <Card pad={0}>
          <header style={{padding:'14px 16px', borderBottom:`1px solid ${W.border}`}}>
            <div style={{display:'flex', alignItems:'center', gap:8}}>
              <Pill color={W.brand}>active</Pill>
              <Pill color={W.info}>visible to 2 parties</Pill>
            </div>
            <div style={{marginTop:8, display:'flex', alignItems:'baseline', gap:8, flexWrap:'wrap'}}>
              <span style={{fontWeight:600, fontSize:14}}>Retail.Token.Token</span>
              <span style={{color:W.dim, fontSize:11.5, fontFamily:wMono}}>retail-token@1.1.0</span>
            </div>
            <div style={{marginTop:4, color:W.mag, fontFamily:wMono, fontSize:12}}>0x77bfac21cd…ee01</div>
          </header>

          <div style={{padding:'12px 16px', borderBottom:`1px solid ${W.border}`}}>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom:6}}>Payload</div>
            <pre style={{margin:0, color:W.text2, fontFamily:wMono, fontSize:11.5, lineHeight:1.55, whiteSpace:'pre-wrap'}}>{`{
  issuer    : app-provider
  owner     : app-user
  amount    : 500
  symbol    : "RTK"
  metadata  : Some "first-batch"
}`}</pre>
          </div>

          <div style={{padding:'12px 16px', borderBottom:`1px solid ${W.border}`}}>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom:6}}>Visibility · projection</div>
            <div style={{display:'flex', flexDirection:'column', gap:6}}>
              <div style={{display:'flex', alignItems:'center', gap:8}}>
                <Pill color={W.ok}>● signatory</Pill>
                <span style={{color:W.text, fontFamily:wMono, fontSize:11.5}}>app-provider</span>
              </div>
              <div style={{display:'flex', alignItems:'center', gap:8}}>
                <Pill color={W.info}>○ observer</Pill>
                <span style={{color:W.text, fontFamily:wMono, fontSize:11.5}}>app-user</span>
              </div>
              <div style={{display:'flex', alignItems:'center', gap:8}}>
                <Pill color={W.dim}>· not visible</Pill>
                <span style={{color:W.dim, fontFamily:wMono, fontSize:11.5}}>sv, DSO</span>
              </div>
            </div>
          </div>

          <div style={{padding:'12px 16px'}}>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom:6}}>Lifecycle</div>
            <TxTreeNode kind="Create" tpl="Retail.Token.Token" cid="0x77bfac…" args="from tx 0xa102b1…" />
            <TxTreeNode kind="Exercise" tpl="Token.Transfer" cid="0x77bfac…" args="{ newOwner = carol, memo = 'gift' }" indent={1} />
            <TxTreeNode kind="Archive" tpl="Retail.Token.Token" cid="0x77bfac…" indent={2} witnesses="app-provider, app-user" last />
            <div style={{marginTop:8, padding:'8px 10px', background:W.surface2, borderRadius:6, color:W.text2, fontSize:11.5, fontFamily:wSans}}>
              <span style={{color:W.brand}}>↗</span> <span style={{marginLeft:6}}>Open in CLI</span>  <span style={{color:W.dim, marginLeft:6, fontFamily:wMono, fontSize:11}}>tx replay 0xa102b1…</span>
            </div>
          </div>
        </Card>
      </div>
    </AppShell>
  );
}

Object.assign(window, { ExplorerScreen });
