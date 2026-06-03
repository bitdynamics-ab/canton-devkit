// Wallet · embeds the Splice-shipped Wallet UI inside the DevKit shell.
// We do NOT reimplement the wallet — we frame it. Multi-wallet switcher,
// health check, "open in new tab" escape hatch, and dev-side helpers.

function WalletSwitcher({ active = 'app-user' }) {
  const wallets = [
    { id:'sv',           name:'sv',           party:'1220a01b…3309', color:'#C4A8F5', health:'ok'   },
    { id:'app-provider', name:'app-provider', party:'1220a8d2…f7c1', color:'#5BD7C5', health:'ok'   },
    { id:'app-user',     name:'app-user',     party:'1220c4f1…aa90', color:'#7CB5F7', health:'ok'   },
    { id:'DSO',          name:'DSO',          party:'1220dso0…f8aa', color:'#F5BF55', health:'sys'  },
  ];
  return (
    <div style={{
      display:'flex', gap:1, padding:3, background:W.surface2,
      border:`1px solid ${W.border}`, borderRadius:9,
    }}>
      {wallets.map(w => {
        const isActive = w.id === active;
        return (
          <button key={w.id} style={{
            display:'flex', alignItems:'center', gap:8,
            padding:'6px 11px', borderRadius:6, border:'none',
            background: isActive ? W.bg : 'transparent',
            cursor:'pointer', fontFamily: wSans,
            boxShadow: isActive ? `0 0 0 1px ${W.borderHi}` : 'none',
          }}>
            <span style={{
              width:18, height:18, borderRadius:'50%',
              background:`linear-gradient(135deg, ${w.color}, ${W.brand})`,
              color:'#0B0E13', display:'flex', alignItems:'center', justifyContent:'center',
              fontWeight:700, fontSize:9,
            }}>{w.name[0].toUpperCase()}</span>
            <span style={{
              fontWeight: isActive ? 600 : 500, fontSize:12.5,
              color: isActive ? W.text : W.text2,
            }}>{w.name}</span>
            {w.health === 'ok' && <span style={{width:6, height:6, borderRadius:'50%', background:W.ok}} />}
            {w.health === 'sys' && <span style={{
              fontSize:9, color:W.amber, fontFamily:wMono, padding:'1px 5px',
              background:`${W.amber}1A`, borderRadius:3, fontWeight:600,
            }}>SYS</span>}
          </button>
        );
      })}
    </div>
  );
}

// ── Mocked Splice Wallet UI (what's inside the iframe) ──────────────────
function EmbeddedSpliceWallet() {
  return (
    <div style={{
      width:'100%', height:'100%',
      background:'#FAFAF8', // Splice wallet is light-themed
      fontFamily:"'Inter', system-ui, sans-serif",
      color:'#1A1A1A', display:'flex', flexDirection:'column', position:'relative',
    }}>
      {/* Splice wallet header (their chrome) */}
      <div style={{
        padding:'12px 22px', borderBottom:'1px solid #E5E5E0',
        display:'flex', alignItems:'center', gap:12, background:'#fff',
      }}>
        <svg width="24" height="24" viewBox="0 0 24 24">
          <circle cx="12" cy="12" r="10" fill="none" stroke="#FF6B35" strokeWidth="2" />
          <path d="M7 12 L11 16 L17 8" fill="none" stroke="#FF6B35" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        <div style={{fontWeight:600, fontSize:14}}>Wallet</div>
        <span style={{color:'#666', fontSize:12}}>·</span>
        <span style={{fontSize:12, color:'#666'}}>Splice 0.4.12</span>
        <div style={{flex:1}} />
        <div style={{
          padding:'5px 11px', background:'#F0F0EC', borderRadius:6,
          fontSize:12, color:'#333', fontFamily:'ui-monospace,monospace',
        }}>app-user</div>
        <span style={{
          width:28, height:28, borderRadius:'50%',
          background:'linear-gradient(135deg, #FF6B35, #FFB070)',
          color:'#fff', display:'flex', alignItems:'center', justifyContent:'center',
          fontWeight:700, fontSize:12,
        }}>A</span>
      </div>

      {/* Body */}
      <div style={{flex:1, padding:'24px 28px', overflow:'hidden'}}>
        {/* Balance hero */}
        <div style={{display:'flex', alignItems:'baseline', gap:8, marginBottom:4}}>
          <span style={{color:'#666', fontSize:12.5, textTransform:'uppercase', letterSpacing:1.2, fontWeight:600}}>Available balance</span>
        </div>
        <div style={{display:'flex', alignItems:'baseline', gap:12, marginBottom:24}}>
          <span style={{fontSize:48, fontWeight:600, letterSpacing:-1.4, color:'#1A1A1A'}}>12.50</span>
          <span style={{fontSize:18, color:'#666', fontWeight:500}}>CC</span>
          <span style={{fontSize:13, color:'#0A8043', fontWeight:500, marginLeft:8}}>▲ 2.50 (24h)</span>
        </div>

        {/* Action buttons */}
        <div style={{display:'flex', gap:10, marginBottom:28}}>
          <button style={{
            padding:'10px 20px', background:'#1A1A1A', color:'#fff', border:'none',
            borderRadius:8, fontWeight:600, fontSize:13.5, cursor:'pointer',
            display:'flex', alignItems:'center', gap:8,
          }}><span>↗</span> Send</button>
          <button style={{
            padding:'10px 20px', background:'#fff', color:'#1A1A1A', border:'1px solid #D9D9D5',
            borderRadius:8, fontWeight:600, fontSize:13.5, cursor:'pointer',
            display:'flex', alignItems:'center', gap:8,
          }}><span>↘</span> Receive</button>
          <button style={{
            padding:'10px 20px', background:'#fff', color:'#1A1A1A', border:'1px solid #D9D9D5',
            borderRadius:8, fontWeight:600, fontSize:13.5, cursor:'pointer',
          }}>Pending offers (2)</button>
        </div>

        {/* Tabs */}
        <div style={{display:'flex', gap:24, borderBottom:'1px solid #E5E5E0', marginBottom:14}}>
          {['Holdings', 'Activity', 'Allocations', 'Featured Apps'].map((t, i) => (
            <div key={t} style={{
              padding:'10px 2px', fontSize:13, fontWeight: i === 0 ? 600 : 500,
              color: i === 0 ? '#1A1A1A' : '#666', cursor:'pointer',
              borderBottom: i === 0 ? '2px solid #1A1A1A' : '2px solid transparent',
              marginBottom:-1,
            }}>{t}</div>
          ))}
        </div>

        {/* Holdings table */}
        <div style={{background:'#fff', border:'1px solid #E5E5E0', borderRadius:8, overflow:'hidden'}}>
          <div style={{
            display:'grid', gridTemplateColumns:'1.4fr 1fr 1fr 1fr',
            gap:14, padding:'9px 16px', color:'#666', fontSize:10.5, letterSpacing:1.2, textTransform:'uppercase', fontWeight:600,
            borderBottom:'1px solid #E5E5E0',
          }}>
            <span>Asset</span><span>Balance</span><span>Locked</span><span>Last activity</span>
          </div>
          {[
            { sym:'CC',  name:'Canton Coin', balance:'12.50', locked:'0.00',  ago:'2 minutes ago', icon:'#FF6B35' },
            { sym:'RTK', name:'Retail Token', balance:'450', locked:'0',    ago:'5 minutes ago', icon:'#5BD7C5' },
            { sym:'USDX', name:'Stable Test', balance:'0', locked:'0',     ago:'—',             icon:'#7CB5F7' },
          ].map((r,i) => (
            <div key={r.sym} style={{
              display:'grid', gridTemplateColumns:'1.4fr 1fr 1fr 1fr',
              gap:14, padding:'12px 16px', alignItems:'center',
              borderBottom: i < 2 ? '1px solid #F2F2EE' : 'none',
            }}>
              <div style={{display:'flex', alignItems:'center', gap:10}}>
                <span style={{
                  width:30, height:30, borderRadius:8,
                  background:r.icon, color:'#fff',
                  display:'flex', alignItems:'center', justifyContent:'center',
                  fontWeight:700, fontSize:11,
                }}>{r.sym}</span>
                <div>
                  <div style={{fontWeight:600, fontSize:13}}>{r.name}</div>
                  <div style={{color:'#888', fontSize:11.5, fontFamily:'ui-monospace,monospace'}}>{r.sym}</div>
                </div>
              </div>
              <span style={{fontFamily:'ui-monospace,monospace', fontSize:14, fontWeight:600}}>{r.balance}</span>
              <span style={{fontFamily:'ui-monospace,monospace', fontSize:13, color:'#888'}}>{r.locked}</span>
              <span style={{fontSize:12, color:'#666'}}>{r.ago}</span>
            </div>
          ))}
        </div>
      </div>

      {/* "Embedded" hint badge */}
      <div style={{
        position:'absolute', bottom:10, right:14,
        padding:'3px 8px', background:'rgba(0,0,0,0.04)',
        color:'#888', fontSize:10.5, fontFamily:'ui-monospace,monospace',
        borderRadius:4, letterSpacing:0.3,
      }}>splice-wallet-ui · localhost:4485</div>
    </div>
  );
}

function HealthDot({ status = 'ok', label }) {
  const cfg = {
    ok:   { color: W.ok, text: 'healthy', pulse: true },
    warn: { color: W.warn, text: 'syncing', pulse: true },
    err:  { color: W.err, text: 'unreachable', pulse: false },
  }[status];
  return (
    <span style={{display:'inline-flex', alignItems:'center', gap:6}}>
      <Dot color={cfg.color} pulse={cfg.pulse} size={7} />
      <span style={{fontSize:11.5, color: cfg.color, fontWeight:500}}>{label || cfg.text}</span>
    </span>
  );
}

function WalletScreen() {
  return (
    <AppShell active="wallet" instance="hubble"
      topRight={<>
        <span className="w-pill" style={{background:`${W.brand}1A`, color:W.brandText, border:`1px solid ${W.brand}33`, fontFamily:wMono}}>Splice 0.4.12</span>
        <button className="w-btn"><span style={{color:W.amber}}>↓</span> Drip from faucet</button>
        <button className="w-btn primary"><span>↗</span> Open in new tab</button>
      </>}>
      {/* Header */}
      <section style={{marginBottom:14, display:'flex', alignItems:'flex-end', justifyContent:'space-between', gap:16, flexWrap:'wrap'}}>
        <div>
          <div style={{display:'flex', alignItems:'center', gap:10, marginBottom:4}}>
            <h1 style={{margin:0, fontSize:20, fontWeight:600, letterSpacing:-0.4}}>Wallet</h1>
            <Pill color={W.dim}>provided by Splice</Pill>
          </div>
          <div style={{color:W.dim, fontSize:12.5}}>Embedded Splice Wallet UI · DevKit handles auth + party selection so you don't juggle browser tabs.</div>
        </div>
        <WalletSwitcher active="app-user" />
      </section>

      {/* Active wallet info strip */}
      <Card pad={0} style={{marginBottom:14}}>
        <div style={{
          padding:'12px 18px', display:'grid', alignItems:'center', gap:18,
          gridTemplateColumns:'auto 1fr auto auto auto',
        }}>
          <div style={{
            width:36, height:36, borderRadius:'50%',
            background:'linear-gradient(135deg, #7CB5F7, #5BD7C5)',
            color:'#0B0E13', display:'flex', alignItems:'center', justifyContent:'center',
            fontWeight:700, fontSize:13,
          }}>A</div>
          <div>
            <div style={{display:'flex', alignItems:'center', gap:10}}>
              <span style={{fontWeight:600, fontSize:14}}>app-user</span>
              <span style={{color:W.dim, fontSize:12}}>@hubble</span>
              <HealthDot status="ok" />
            </div>
            <Mono c={W.dim}>party::1220c4f1c89a4e2b…aa90</Mono>
          </div>
          <div style={{textAlign:'right'}}>
            <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600}}>Balance</div>
            <div style={{display:'flex', alignItems:'baseline', gap:6}}>
              <span style={{fontFamily:wMono, fontSize:18, fontWeight:600}}>12.50</span>
              <span style={{color:W.dim, fontSize:11}}>CC</span>
            </div>
          </div>
          <div style={{height:32, width:1, background:W.border}} />
          <div style={{display:'flex', gap:6}}>
            <button className="w-btn" style={{padding:'5px 10px', fontSize:11.5}}>Copy party ID</button>
            <button className="w-btn" style={{padding:'5px 10px', fontSize:11.5}}>JWT</button>
          </div>
        </div>
      </Card>

      {/* Main: embedded wallet + side panel */}
      <div style={{display:'grid', gridTemplateColumns:'1fr 320px', gap:14, alignItems:'stretch'}}>
        {/* Embedded wallet iframe */}
        <Card pad={0} style={{overflow:'hidden', display:'flex', flexDirection:'column'}}>
          {/* Fake browser bar above the iframe so devs know this is the real Splice UI */}
          <div style={{
            background:W.surface2, borderBottom:`1px solid ${W.border}`,
            padding:'7px 12px', display:'flex', alignItems:'center', gap:8,
          }}>
            <span style={{display:'flex', gap:5}}>
              <button className="w-iconbtn" title="back" style={{width:22, height:22, fontSize:12}}>‹</button>
              <button className="w-iconbtn" title="fwd"  style={{width:22, height:22, fontSize:12}}>›</button>
              <button className="w-iconbtn" title="reload" style={{width:22, height:22, fontSize:12}}>↻</button>
            </span>
            <div style={{
              flex:1, background:W.bg, border:`1px solid ${W.border}`, borderRadius:6,
              padding:'4px 10px', display:'flex', alignItems:'center', gap:8,
              fontSize:11.5, fontFamily:wMono, color:W.text2,
            }}>
              <span style={{color:W.ok}}>●</span>
              <span style={{color:W.dim}}>https://</span>
              <span>wallet.localhost</span>
              <span style={{color:W.dim, marginLeft:'auto', fontSize:10.5}}>·  signed in as app-user via DevKit JWT</span>
            </div>
            <button className="w-iconbtn" title="open externally" style={{width:22, height:22, fontSize:11}}>↗</button>
          </div>
          {/* Actual embed area */}
          <div style={{flex:1, minHeight:0, background:'#FAFAF8'}}>
            <EmbeddedSpliceWallet />
          </div>
        </Card>

        {/* Right side panel — dev tools that aren't in the wallet itself */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card title="Other wallets" subtitle="quick switch" pad={8}>
            {[
              { name:'app-provider', balance:'400.00 CC · 999,450 RTK', color:'#5BD7C5', health:'ok' },
              { name:'sv',           balance:'0.01 CC · 100 RTK',       color:'#C4A8F5', health:'ok' },
              { name:'DSO',          balance:'bootstrap',               color:'#F5BF55', health:'sys' },
            ].map(r => (
              <div key={r.name} className="w-row" style={{
                display:'flex', alignItems:'center', gap:10, padding:'7px 9px', borderRadius:6, cursor:'pointer',
              }}>
                <span style={{
                  width:26, height:26, borderRadius:'50%',
                  background:`linear-gradient(135deg, ${r.color}, ${W.brand})`,
                  color:'#0B0E13', display:'flex', alignItems:'center', justifyContent:'center',
                  fontWeight:700, fontSize:11,
                }}>{r.name[0].toUpperCase()}</span>
                <div style={{flex:1, minWidth:0}}>
                  <div style={{fontWeight:600, fontSize:12.5}}>{r.name}</div>
                  <Mono c={W.dim}>{r.balance}</Mono>
                </div>
                <Dot color={r.health === 'ok' ? W.ok : W.amber} size={6} />
              </div>
            ))}
          </Card>

          <Card title="Faucet · drip to app-user" subtitle="dev-only · won't run on devnet">
            <div style={{display:'flex', flexDirection:'column', gap:6}}>
              {[
                { sym:'CC',  amt:'+10',  detail:'next drip in 0:00',  primary:true  },
                { sym:'RTK', amt:'+100', detail:'CIP-0112 retail-token@1.1.0', primary:false },
                { sym:'USDX', amt:'+50', detail:'CIP-0112 stable-test',         primary:false },
              ].map((r,i) => (
                <div key={i} className="w-row" style={{
                  display:'flex', alignItems:'center', gap:10, padding:'8px 10px', borderRadius:6,
                  background: r.primary ? `${W.brand}08` : 'transparent',
                  border: r.primary ? `1px solid ${W.brand}33` : `1px solid ${W.border}`,
                }}>
                  <span style={{
                    width:26, height:26, borderRadius:7,
                    background:r.primary ? W.brand : W.surface2, color:r.primary ? '#082018' : W.brand,
                    display:'flex', alignItems:'center', justifyContent:'center',
                    fontWeight:700, fontSize:10.5,
                  }}>{r.sym}</span>
                  <div style={{flex:1, minWidth:0}}>
                    <div style={{fontWeight:600, fontSize:12.5}}>{r.amt} {r.sym}</div>
                    <Mono c={W.dim}>{r.detail}</Mono>
                  </div>
                  <button className="w-btn" style={{padding:'4px 10px', fontSize:11.5, ...(r.primary ? {background:W.brand, color:'#082018', borderColor:W.brand, fontWeight:600} : {})}}>drip</button>
                </div>
              ))}
              <div style={{marginTop:4, padding:'8px 10px', background:W.surface2, borderRadius:6, fontSize:11, color:W.dim, lineHeight:1.45}}>
                <span style={{color:W.brand}}>ⓘ</span> Equivalent CLI: <Mono c={W.text2}>dpm localnet token mint CC 10 --to app-user</Mono>
              </div>
            </div>
          </Card>

          <Card title="Recent transfers" subtitle="this wallet · 1h" pad={0}>
            {[
              { dir:'in',  amt:'+10.00 CC', from:'app-provider', time:'2m', },
              { dir:'in',  amt:'+50 RTK',   from:'app-provider', time:'5m', },
              { dir:'out', amt:'−12 RTK',   to:  'burn',         time:'18m',},
              { dir:'in',  amt:'+100 RTK',  from:'faucet',       time:'1h', },
            ].map((r,i) => (
              <div key={i} style={{
                display:'flex', alignItems:'center', gap:10, padding:'9px 14px',
                borderBottom: i < 3 ? `1px solid ${W.border}` : 'none',
              }}>
                <span style={{
                  width:24, height:24, borderRadius:6,
                  background: r.dir === 'in' ? `${W.ok}14` : `${W.err}14`,
                  color: r.dir === 'in' ? W.ok : W.err,
                  display:'flex', alignItems:'center', justifyContent:'center',
                  fontSize:13, fontWeight:700,
                }}>{r.dir === 'in' ? '↘' : '↗'}</span>
                <div style={{flex:1, minWidth:0}}>
                  <div style={{fontFamily:wMono, fontSize:12, color: r.dir === 'in' ? W.ok : W.err, fontWeight:600}}>{r.amt}</div>
                  <Mono c={W.dim}>{r.dir === 'in' ? `from ${r.from}` : `to ${r.to}`}</Mono>
                </div>
                <Mono c={W.dim}>{r.time}</Mono>
              </div>
            ))}
          </Card>

          <Card pad={12} style={{background:`${W.brand}06`, borderColor:`${W.brand}22`}}>
            <div style={{display:'flex', alignItems:'center', gap:8, marginBottom:6}}>
              <span style={{color:W.brand, fontSize:14}}>↗</span>
              <span style={{fontWeight:600, fontSize:12.5}}>Need the full wallet?</span>
            </div>
            <div style={{color:W.dim, fontSize:11.5, lineHeight:1.5}}>
              For multi-step flows or copy-paste of allocation IDs, open the wallet in its own tab — auth carries over via the DevKit JWT cookie.
            </div>
            <button className="w-btn" style={{marginTop:8, width:'100%', justifyContent:'center'}}>
              Open wallet.localhost ↗
            </button>
          </Card>
        </div>
      </div>
    </AppShell>
  );
}

Object.assign(window, { WalletScreen, WalletSwitcher, EmbeddedSpliceWallet });
