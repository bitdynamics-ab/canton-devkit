// Tokens / Faucet surface, ⌘K Command Palette overlay, Multi-instance switcher.
// Built atop the existing AppShell + dashboard primitives.

// ─── TOKEN CARD ───────────────────────────────────────────────────────────
function TokenCard({ symbol, name, color, supply, holders, decimals, standard, sparkData, holdings, status = 'live', featured }) {
  return (
    <Card pad={0} style={{ position:'relative', overflow:'hidden' }}>
      {featured && (
        <div style={{
          position:'absolute', top:0, right:0,
          background:`linear-gradient(135deg, ${W.brand}33, transparent)`,
          padding:'4px 12px', fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
          color:W.brand, borderBottomLeftRadius: 10,
        }}>featured</div>
      )}
      <div style={{padding:'16px 18px 12px', display:'flex', alignItems:'center', gap:12}}>
        <div style={{
          width:42, height:42, borderRadius:12,
          background: `linear-gradient(135deg, ${color} 0%, ${W.brand} 140%)`,
          color: '#0B0E13',
          display:'flex', alignItems:'center', justifyContent:'center',
          fontWeight: 700, fontSize: 13, letterSpacing: -0.4,
          boxShadow: `0 6px 14px -6px ${color}88`,
        }}>{symbol}</div>
        <div style={{flex:1, minWidth:0}}>
          <div style={{display:'flex', alignItems:'center', gap:8}}>
            <span style={{fontWeight:600, fontSize:14, letterSpacing:-0.2}}>{name}</span>
            <Pill color={status==='live'? W.ok : W.warn}><Dot color={status==='live'? W.ok : W.warn} pulse={status==='live'} /> {status}</Pill>
          </div>
          <Mono c={W.dim}>{standard}  ·  decimals {decimals}</Mono>
        </div>
        <button className="w-iconbtn" title="more">···</button>
      </div>

      <div style={{padding:'0 18px 12px'}}>
        <div style={{display:'grid', gridTemplateColumns:'1fr 1fr 1fr', gap:10, padding:'12px 0', borderTop:`1px solid ${W.border}`, borderBottom:`1px solid ${W.border}`}}>
          <div>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600, marginBottom:2}}>Supply</div>
            <div style={{fontFamily:wMono, fontSize:13, color:W.text, fontWeight:600}}>{supply}</div>
          </div>
          <div>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600, marginBottom:2}}>Holders</div>
            <div style={{fontFamily:wMono, fontSize:13, color:W.text, fontWeight:600}}>{holders}</div>
          </div>
          <div>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase', fontWeight:600, marginBottom:2}}>24h tx</div>
            <div style={{fontFamily:wMono, fontSize:13, color:W.text, fontWeight:600}}>412 <span style={{color:W.ok, fontSize:11, fontWeight:500}}>▲14</span></div>
          </div>
        </div>

        <div style={{padding:'10px 0 6px'}}>
          <Sparkline data={sparkData} height={36} color={color} />
        </div>

        {/* holdings breakdown */}
        <div style={{display:'flex', flexDirection:'column', gap:4, padding:'4px 0 10px'}}>
          {holdings.map((h, i) => (
            <div key={i} style={{display:'flex', alignItems:'center', gap:8, fontSize:11.5}}>
              <span style={{width:6, height:6, borderRadius:'50%', background: h.color}} />
              <span style={{color:W.text2, fontFamily:wMono, flex:1}}>{h.party}</span>
              <span style={{color:W.text, fontFamily:wMono, fontWeight:600}}>{h.amount}</span>
              <span style={{color:W.dim, width:38, textAlign:'right', fontFamily:wMono}}>{h.pct}%</span>
            </div>
          ))}
        </div>
      </div>

      {/* actions */}
      <div style={{display:'grid', gridTemplateColumns:'1fr 1fr 1fr 1fr', borderTop:`1px solid ${W.border}`}}>
        {['Mint', 'Transfer', 'Burn', 'Inspect'].map((a, i) => (
          <button key={a} style={{
            padding:'10px 6px', fontSize:12, fontWeight:500,
            background:'transparent', border:'none', color:W.text2,
            borderRight: i < 3 ? `1px solid ${W.border}` : 'none',
            cursor:'pointer', fontFamily:wSans,
          }}>{a}</button>
        ))}
      </div>
    </Card>
  );
}

function FaucetRow({ wallet, party, color, balance, sym, lastDrip }) {
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'1.5fr 1.5fr 1fr .8fr auto',
      gap:14, padding:'10px 14px', alignItems:'center', borderBottom:`1px solid ${W.border}`,
    }}>
      <div style={{display:'flex', alignItems:'center', gap:10}}>
        <div style={{
          width:28, height:28, borderRadius:'50%',
          background:`linear-gradient(135deg, ${color}, ${W.brand})`,
          color:'#0B0E13', display:'flex', alignItems:'center', justifyContent:'center',
          fontWeight:700, fontSize:11,
        }}>{wallet[0].toUpperCase()}</div>
        <div>
          <div style={{fontWeight:600, fontSize:12.5}}>{wallet}</div>
          <Mono c={W.dim}>{party}</Mono>
        </div>
      </div>
      <Mono c={W.text2}>{party}</Mono>
      <Mono c={W.text}>{balance} <span style={{color:W.dim}}>{sym}</span></Mono>
      <span style={{color:W.dim, fontSize:11.5}}>{lastDrip}</span>
      <div style={{display:'flex', gap:6, justifyContent:'flex-end'}}>
        <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5}}>+100 RTK</button>
        <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5}}>+10 CC</button>
      </div>
    </div>
  );
}

function TokensScreen() {
  return (
    <AppShell active="tokens" instance="hubble"
      topRight={<>
        <span className="w-pill" style={{background:`${W.brand}1A`, color:W.brandText, border:`1px solid ${W.brand}33`, fontFamily:wMono}}>CIP-0112</span>
        <button className="w-btn">Drip all faucets</button>
        <button className="w-btn primary"><span>+</span> Create token</button>
      </>}>
      <section style={{marginBottom:14, display:'flex', alignItems:'flex-end', justifyContent:'space-between', gap:16}}>
        <div>
          <h1 style={{margin:'0 0 3px', fontSize:20, fontWeight:600, letterSpacing:-0.4}}>Tokens</h1>
          <div style={{color:W.dim, fontSize:12.5}}>Create test instruments and drip them to LocalNet wallets. CIP-0112 first, with optional CIP-56 compatibility.</div>
        </div>
        <div style={{display:'flex', gap:8, alignItems:'center'}}>
          <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>3 tokens · 1,000,892 RTK in circulation</span>
        </div>
      </section>

      {/* Token cards */}
      <div style={{display:'grid', gridTemplateColumns:'repeat(3, 1fr)', gap:14, marginBottom:14}}>
        <TokenCard
          symbol="RTK" name="Retail Token" color="#5BD7C5"
          supply="1,000,000" holders="3" decimals="2" standard="CIP-0112 v2"
          sparkData={[120,180,220,260,300,340,380,440,480,520,560,580,600,620]}
          holdings={[
            { party:'app-provider', amount:'999,450', pct:'99.9', color:'#5BD7C5' },
            { party:'app-user',     amount:'450',     pct:'0.0',  color:'#7CB5F7' },
            { party:'sv',           amount:'100',     pct:'0.0',  color:'#C4A8F5' },
          ]}
          featured
        />
        <TokenCard
          symbol="CC" name="Canton Coin" color="#F5BF55"
          supply="412.21" holders="4" decimals="10" standard="Splice Amulet · native"
          sparkData={[400,402,398,404,408,406,410,412,409,411,412,414,415,412]}
          holdings={[
            { party:'app-provider', amount:'400.00', pct:'97.0', color:'#5BD7C5' },
            { party:'app-user',     amount:'12.20', pct:'2.9',  color:'#7CB5F7' },
            { party:'sv',           amount:'0.01',  pct:'0.1',  color:'#C4A8F5' },
          ]}
        />
        <TokenCard
          symbol="USDX" name="Stable Test" color="#7CB5F7"
          status="draft"
          supply="0"        holders="0" decimals="6" standard="CIP-0112 v2"
          sparkData={[0,0,0,0,0,0,0,0,0,0,0,0,0,0]}
          holdings={[
            { party:'(not minted yet)', amount:'—', pct:'—', color:W.dim },
          ]}
        />
      </div>

      {/* Faucet + activity */}
      <div style={{display:'grid', gridTemplateColumns:'1.4fr 1fr', gap:14, marginBottom:14}}>
        <Card pad={0} title="Test wallet faucet" subtitle="One-click drips into LocalNet wallets · CIP-0112"
          action={<>
            <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5}}>Configure drip amounts</button>
            <button className="w-btn primary" style={{padding:'4px 10px', fontSize:11.5}}>Drip all</button>
          </>}>
          <div style={{
            display:'grid', gridTemplateColumns:'1.5fr 1.5fr 1fr .8fr auto',
            gap:14, padding:'8px 14px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
            borderBottom:`1px solid ${W.border}`,
          }}>
            <span>Wallet</span><span>Party</span><span>Balance</span><span>Last drip</span><span></span>
          </div>
          <FaucetRow wallet="app-provider" party="party::1220a8d2…f7c1" color="#5BD7C5" balance="999,450" sym="RTK" lastDrip="2m ago" />
          <FaucetRow wallet="app-user"     party="party::1220c4f1…aa90" color="#7CB5F7" balance="450"     sym="RTK" lastDrip="2m ago" />
          <FaucetRow wallet="sv"           party="party::1220a01b…3309" color="#C4A8F5" balance="100"     sym="RTK" lastDrip="1h ago" />
          <FaucetRow wallet="DSO"          party="party::1220dso0…f8aa" color="#F5BF55" balance="bootstrap" sym="" lastDrip="—" />
          <div style={{padding:'10px 14px', color:W.dim, fontSize:11.5}}>
            Use <Mono c={W.brandText}>dpm localnet token mint &lt;SYM&gt; &lt;amount&gt; --to &lt;wallet&gt;</Mono> from the CLI for scripted drips.
          </div>
        </Card>

        <Card title="Recent token activity" subtitle="all tokens · last hour" pad={0}>
          {[
            { time:'15:42:14', kind:'MINT',     who:'app-provider', body:'+50 RTK → app-user',         color: W.amber },
            { time:'15:42:12', kind:'TRANSFER', who:'app-provider', body:'500 RTK → app-user',         color: W.info },
            { time:'15:42:11', kind:'PROPOSAL', who:'app-provider', body:'gift 50 RTK to app-user',    color: W.mag },
            { time:'15:42:04', kind:'CREATE',   who:'app-provider', body:'retail-token@1.1.0 deployed', color: W.brand },
            { time:'15:38:21', kind:'BURN',     who:'app-user',     body:'−12 RTK',                    color: W.err },
            { time:'15:31:02', kind:'DRIP',     who:'faucet',       body:'+100 RTK → app-user, +100 → sv', color: W.ok },
          ].map((r,i) => (
            <div key={i} className="w-row" style={{
              display:'grid', gridTemplateColumns:'72px 80px 1fr', gap:12,
              padding:'9px 14px', alignItems:'center',
              borderBottom: i < 5 ? `1px solid ${W.border}` : 'none',
            }}>
              <Mono c={W.dim}>{r.time}</Mono>
              <span style={{color:r.color, fontWeight:600, fontSize:11.5, letterSpacing:0.4}}>{r.kind}</span>
              <span style={{color:W.text, fontSize:12.5}}>{r.body} <span style={{color:W.dim}}>· {r.who}</span></span>
            </div>
          ))}
        </Card>
      </div>

      {/* CLI hint footer */}
      <Card pad={14} style={{ background: `${W.brand}08`, borderColor: `${W.brand}22` }}>
        <div style={{display:'flex', alignItems:'center', gap:12, flexWrap:'wrap'}}>
          <span style={{color:W.brand, fontWeight:700}}>›</span>
          <span style={{fontSize:13}}>Every action here is also a CLI command — try:</span>
          <Mono c={W.text}>dpm localnet token create</Mono>
          <span style={{color:W.dim}}>·</span>
          <Mono c={W.text}>token mint RTK 500 --to app-user</Mono>
          <span style={{color:W.dim}}>·</span>
          <Mono c={W.text}>token balance RTK --json</Mono>
        </div>
      </Card>
    </AppShell>
  );
}

// ─── ⌘K COMMAND PALETTE ──────────────────────────────────────────────────
function PaletteRow({ icon, color = W.dim, primary, secondary, kbd, active }) {
  return (
    <div style={{
      display:'flex', alignItems:'center', gap:12, padding:'10px 14px',
      background: active ? `${W.brand}14` : 'transparent',
      borderLeft: active ? `2px solid ${W.brand}` : '2px solid transparent',
      paddingLeft: active ? 12 : 14,
      cursor:'pointer',
    }}>
      <span style={{
        width:30, height:30, borderRadius:7, display:'flex', alignItems:'center', justifyContent:'center',
        background: `${color}1A`, color, fontSize: 14,
      }}>{icon}</span>
      <div style={{flex:1, minWidth:0}}>
        <div style={{fontSize:13, fontWeight: active ? 600 : 500, color: active ? W.text : W.text2}}>{primary}</div>
        {secondary && <div style={{color:W.dim, fontSize:11.5, fontFamily:secondary.includes('::') ? wMono : wSans, marginTop:1}}>{secondary}</div>}
      </div>
      {kbd && <span className="w-kbd">{kbd}</span>}
      {active && <span style={{color:W.brand, fontSize:13, marginLeft:6}}>↵</span>}
    </div>
  );
}

function PaletteSection({ title, count, children }) {
  return (
    <div>
      <div style={{
        padding:'10px 14px 4px',
        color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
        display:'flex', justifyContent:'space-between',
      }}>
        <span>{title}</span>
        <span style={{fontFamily:wMono}}>{count}</span>
      </div>
      {children}
    </div>
  );
}

function PaletteOverlay({ query = "tok" }) {
  return (
    <div style={{
      position:'absolute', inset:0,
      background:'rgba(7, 10, 14, 0.66)',
      backdropFilter:'blur(8px)',
      WebkitBackdropFilter:'blur(8px)',
      display:'flex', justifyContent:'center', paddingTop: 88,
      zIndex: 20,
    }}>
      <div style={{
        width: 640, maxHeight: 540,
        background: W.surface, border: `1px solid ${W.borderHi}`, borderRadius: 14,
        boxShadow: '0 32px 60px -20px rgba(0,0,0,.7), 0 0 0 1px rgba(255,255,255,0.02) inset',
        overflow: 'hidden', display:'flex', flexDirection:'column',
      }}>
        {/* search input */}
        <div style={{padding:'16px 18px', borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:12}}>
          <span style={{color: W.brand, fontSize:18}}>⌕</span>
          <span style={{flex:1, fontFamily:wSans, fontSize:15, color:W.text, fontWeight:500}}>
            {query}<span className="term-cursor" style={{background:W.brand}} />
          </span>
          <span style={{display:'flex', gap:6}}>
            <span className="w-kbd">↑↓</span><span className="w-kbd">↵</span><span className="w-kbd">esc</span>
          </span>
        </div>

        <div className="w-scroll" style={{flex:1, overflow:'auto'}}>
          <PaletteSection title="Actions" count="3">
            <PaletteRow icon="◆" color={W.amber} primary="Create token" secondary="dpm localnet token create — opens the CIP-0112 wizard" kbd="↵" active />
            <PaletteRow icon="↗" color={W.info}  primary="Mint RTK to app-user" secondary="dpm localnet token mint RTK 500 --to app-user" />
            <PaletteRow icon="◐" color={W.warn}  primary="Snapshot LocalNet hubble" secondary="dpm localnet snapshot --name hubble" />
          </PaletteSection>
          <PaletteSection title="Jump to" count="4">
            <PaletteRow icon="◉" color={W.brand} primary="Token RTK · contract 0x77bf…" secondary="party::1220c4f1…aa90 · owner app-user · amount 500" />
            <PaletteRow icon="❒" color={W.mag}   primary="retail-token@1.1.0" secondary="DAR · pkg 0x9f2c0a…d12c" />
            <PaletteRow icon="≣" color={W.rose}  primary="Transaction 0xa102b1ce4977" secondary="offset 0/1244 · 15:42:07" />
            <PaletteRow icon="◇" color={W.info}  primary="Party · app-user" secondary="party::1220c4f1…aa90" />
          </PaletteSection>
          <PaletteSection title="Recent" count="2">
            <PaletteRow icon="↻" color={W.dim} primary="Restart app-provider-participant" secondary="ran 3m ago" />
            <PaletteRow icon="↑" color={W.dim} primary="Upload retail-token-1.1.0.dar" secondary="ran 4m ago · vetted on 3" />
          </PaletteSection>
        </div>

        <div style={{
          padding:'8px 14px', borderTop:`1px solid ${W.border}`,
          background: W.bg, color: W.dim, fontSize: 11,
          display:'flex', alignItems:'center', gap:14,
        }}>
          <span style={{display:'flex', alignItems:'center', gap:6}}><LogoMark size={14}/> <span style={{color:W.text2}}>DevKit</span></span>
          <span style={{flex:1}}>Try <Mono c={W.text2}>&gt;</Mono> for commands · <Mono c={W.text2}>@</Mono> for parties · <Mono c={W.text2}>#</Mono> for contracts</span>
          <span>10 of 248 results</span>
        </div>
      </div>
    </div>
  );
}

function CommandPaletteScreen() {
  return (
    <div style={{position:'relative', width:'100%', height:'100%'}}>
      <DashboardScreen />
      <PaletteOverlay query="tok" />
    </div>
  );
}

// ─── MULTI-INSTANCE SWITCHER ─────────────────────────────────────────────
function InstanceRow({ name, splice, state, ports, started, volumes, active }) {
  const stateColor = state === 'running' ? W.ok : state === 'paused' ? W.warn : W.dim;
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'auto 1fr 1fr 1.4fr 1fr .8fr 80px',
      gap:14, padding:'12px 16px', alignItems:'center',
      background: active ? W.selRow : 'transparent',
      borderLeft: active ? `2px solid ${W.brand}` : '2px solid transparent',
      paddingLeft: active ? 14 : 16,
      borderBottom:`1px solid ${W.border}`, cursor:'pointer',
    }}>
      <Dot color={stateColor} pulse={state==='running'} size={9} />
      <div>
        <div style={{display:'flex', alignItems:'center', gap:8}}>
          <span style={{fontWeight:600, fontSize:13, letterSpacing:-0.1}}>{name}</span>
          {active && <Pill color={W.brand}>active</Pill>}
        </div>
        <Mono c={W.dim}>splice {splice}</Mono>
      </div>
      <span style={{color:stateColor, fontWeight:500, fontSize:12.5, textTransform:'capitalize'}}>{state}</span>
      <Mono c={W.text2}>{ports}</Mono>
      <span style={{color:W.text2, fontSize:11.5}}>{started}</span>
      <Mono c={W.text2}>{volumes}</Mono>
      <div style={{display:'flex', gap:4, justifyContent:'flex-end'}}>
        {state === 'running'
          ? <button className="w-iconbtn" title="Stop">■</button>
          : <button className="w-iconbtn" title="Start">▶</button>}
        <button className="w-iconbtn" title="More">···</button>
      </div>
    </div>
  );
}

function InstanceSwitcherOverlay() {
  return (
    <div style={{
      position:'absolute', inset:0,
      background:'rgba(7, 10, 14, 0.55)',
      backdropFilter:'blur(6px)', WebkitBackdropFilter:'blur(6px)',
      display:'flex', justifyContent:'center', paddingTop: 76,
      zIndex: 20,
    }}>
      <div style={{
        width: 880, background: W.surface, border:`1px solid ${W.borderHi}`,
        borderRadius: 14, overflow:'hidden',
        boxShadow:'0 32px 60px -20px rgba(0,0,0,.7), 0 0 0 1px rgba(255,255,255,0.02) inset',
        display:'flex', flexDirection:'column', maxHeight: 660,
      }}>
        <header style={{padding:'14px 18px', borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:12}}>
          <div style={{flex:1}}>
            <div style={{fontWeight:600, fontSize:15, letterSpacing:-0.2}}>Switch LocalNet</div>
            <div style={{color:W.dim, fontSize:11.5, marginTop:1}}>4 instances · 2 running · 1 paused · 1 stopped</div>
          </div>
          <input placeholder="filter by name…" style={{
            background:W.bg, border:`1px solid ${W.border}`, color:W.text, fontFamily:wSans,
            fontSize:12.5, padding:'6px 11px', borderRadius:6, width:220,
          }} />
          <button className="w-btn primary"><span>+</span> New LocalNet</button>
        </header>

        <div style={{
          display:'grid', gridTemplateColumns:'auto 1fr 1fr 1.4fr 1fr .8fr 80px',
          gap:14, padding:'9px 16px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
          borderBottom:`1px solid ${W.border}`,
        }}>
          <span></span><span>Name · Splice</span><span>State</span><span>Ports</span><span>Started</span><span>Volumes</span><span></span>
        </div>

        <div style={{overflow:'auto', flex:1}}>
          <InstanceRow name="hubble" splice="0.4.12" state="running" ports="4901, 3901, 2901 · multi-sync" started="1h 22m ago" volumes="3.4 GiB" active />
          <InstanceRow name="weiss"  splice="0.4.10" state="paused"  ports="5901, 5801, 5701"           started="2d 4h ago"   volumes="1.1 GiB" />
          <InstanceRow name="shadow" splice="0.4.12" state="stopped" ports="6901, 6801, 6701"           started="5d ago"      volumes="0.4 GiB" />
          <InstanceRow name="ci-pr-841" splice="0.4.12" state="running" ports="7901, 7801, 7701 · ephemeral" started="12m ago · gh-actions" volumes="220 MiB" />
        </div>

        <footer style={{
          padding:'10px 16px', borderTop:`1px solid ${W.border}`,
          background:W.bg, color:W.dim, fontSize:11,
          display:'flex', alignItems:'center', gap:14,
        }}>
          <span style={{display:'flex', alignItems:'center', gap:6}}><LogoMark size={14}/><span style={{color:W.text2}}>DevKit</span></span>
          <span style={{flex:1}}>CLI: <Mono c={W.text2}>dpm localnet list --json</Mono> · jump via <Mono c={W.text2}>dpm localnet env --name &lt;name&gt;</Mono></span>
          <span><span className="w-kbd">↑↓</span> nav · <span className="w-kbd">↵</span> switch · <span className="w-kbd">esc</span> close</span>
        </footer>
      </div>
    </div>
  );
}

function InstanceSwitcherScreen() {
  return (
    <div style={{position:'relative', width:'100%', height:'100%'}}>
      <DashboardScreen />
      <InstanceSwitcherOverlay />
    </div>
  );
}

Object.assign(window, { TokensScreen, CommandPaletteScreen, InstanceSwitcherScreen });
