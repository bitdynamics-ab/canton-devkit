// Web UI shell — sidebar, topbar, command palette overlay, primitives.
// All screens reuse these via window.* exports.

const W = {
  bg:        '#0B0E13',
  surface:   '#11151D',
  surface2:  '#161C25',
  border:    '#1F2731',
  borderHi:  '#2A3441',
  text:      '#E6E9EE',
  text2:     '#B5BCC7',
  dim:       '#7C8593',
  faint:     '#4A5260',
  brand:     '#5BD7C5',
  brandSoft: '#1F4A47',
  brandText: '#7FE9D9',
  ok:        '#62E2A0',
  warn:      '#F5BF55',
  err:       '#F47C7C',
  info:      '#7CB5F7',
  mag:       '#C4A8F5',
  rose:      '#F08FB5',
  amber:     '#E8A14E',
  card:      '#10151D',
  rowHover:  '#161D26',
  selRow:    '#162226',
};

const wMono = "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace";
const wSans = "'IBM Plex Sans', 'Inter Tight', system-ui, sans-serif";

if (typeof document !== 'undefined' && !document.getElementById('w-styles')) {
  const s = document.createElement('style');
  s.id = 'w-styles';
  s.textContent = `
    @keyframes w-pulse { 0%,100% { opacity:1; transform:scale(1); } 50% { opacity:.55; transform:scale(1.15); } }
    @keyframes w-shimmer { 0%{background-position:-200% 0} 100%{background-position:200% 0} }
    @keyframes w-flash { 0%{background:rgba(94,226,160,.22)} 100%{background:transparent} }
    .w-pulse { animation: w-pulse 1.8s ease-in-out infinite; }
    .w-flash { animation: w-flash 1.6s ease-out 1; }
    .w-row { transition: background .12s; }
    .w-row:hover { background: ${W.rowHover}; }
    .w-scroll::-webkit-scrollbar { width: 6px; height: 6px; }
    .w-scroll::-webkit-scrollbar-thumb { background: ${W.borderHi}; border-radius: 4px; }
    .w-kbd { font-family: ${wMono}; font-size: 10px; padding: 1px 5px; border-radius: 4px; background:#1A2230; border:1px solid ${W.border}; color:${W.text2}; line-height:1.2; }
    .w-pill { display:inline-flex; align-items:center; gap:6px; padding: 3px 9px; border-radius:999px; font-size:11px; line-height:1; }
    .w-link { color:${W.brandText}; text-decoration:none; border-bottom: 1px dashed ${W.brandSoft}; }
    .w-btn { font-family:${wSans}; font-size: 12px; padding: 6px 11px; border-radius:6px; border:1px solid ${W.border}; background:${W.surface2}; color:${W.text}; cursor:pointer; display:inline-flex; align-items:center; gap:6px; }
    .w-btn:hover { border-color:${W.borderHi}; background:${W.card}; }
    .w-btn.primary { background:${W.brand}; color:#082018; border-color:${W.brand}; font-weight:600; }
    .w-btn.danger { color:${W.err}; }
    .w-iconbtn { width:28px; height:28px; display:inline-flex; align-items:center; justify-content:center; border-radius:6px; color:${W.dim}; cursor:pointer; }
    .w-iconbtn:hover { background:${W.surface2}; color:${W.text}; }
  `;
  document.head.appendChild(s);
}

// ── BitDynamics mark (chip/candle motif: outer frame + lime square + leads) ──
function LogoMark({ size = 22, frame = '#5C6573', fill = '#CCFB6A' }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-label="BitDynamics">
      <rect x="10" y="10" width="44" height="44" fill="none" stroke={frame} strokeWidth="3" />
      <rect x="22" y="22" width="20" height="20" fill={fill} />
      <line x1="32" y1="0"  x2="32" y2="8"  stroke={fill}  strokeWidth="4" />
      <line x1="32" y1="56" x2="32" y2="64" stroke={frame} strokeWidth="3" />
    </svg>
  );
}

// ── BitDynamics horizontal lockup (mark + wordmark) ──
function LogoLockup({ height = 18, color = '#E6E9EE', frame = '#5C6573', fill = '#CCFB6A' }) {
  return (
    <span style={{display:'inline-flex', alignItems:'center', gap: height * 0.45, lineHeight: 1}}>
      <LogoMark size={height} frame={frame} fill={fill} />
      <span style={{
        fontFamily: wMono, color, fontWeight: 600,
        letterSpacing: height * 0.06, fontSize: height * 0.78,
      }}>BITDYNAMICS</span>
    </span>
  );
}

// ── Status dot ───────────────────────────────────────────────────────────
function Dot({ color = W.ok, pulse = false, size = 8 }) {
  return (
    <span style={{ display:'inline-block', width:size, height:size, borderRadius:'50%', background:color, boxShadow: pulse ? `0 0 0 3px ${color}33` : 'none' }} className={pulse ? 'w-pulse' : ''} />
  );
}

// ── Pill ─────────────────────────────────────────────────────────────────
function Pill({ children, color = W.brand, soft = true }) {
  return (
    <span className="w-pill" style={{
      background: soft ? `${color}1A` : color,
      color: soft ? color : '#0B0E13',
      border: `1px solid ${color}33`,
      fontWeight: 500,
    }}>{children}</span>
  );
}

// ── App Shell ────────────────────────────────────────────────────────────
function AppShell({ active = 'overview', instance = 'hubble', children, topRight }) {
  const nav = [
    { id:'overview',   icon:'⌂', label:'Overview' },
    { id:'wallet',     icon:'◉', label:'Wallet' },
    { id:'explorer',   icon:'◉', label:'Explorer' },
    { id:'dar',        icon:'❒', label:'DAR Manager' },
    { id:'tokens',     icon:'◆', label:'Tokens' },
    { id:'logs',       icon:'≣', label:'Logs' },
    { id:'metrics',    icon:'⌁', label:'Metrics' },
    { id:'agent',      icon:'✦', label:'Agent Skill' },
    { id:'settings',   icon:'⚙', label:'Settings' },
  ];
  return (
    <div style={{
      display:'grid',
      gridTemplateColumns:'232px 1fr',
      height:'100%',
      background: W.bg,
      color: W.text,
      fontFamily: wSans,
      fontSize: 13,
    }}>
      {/* Sidebar */}
      <aside style={{ borderRight:`1px solid ${W.border}`, padding: '14px 12px', display:'flex', flexDirection:'column' }}>
        <div style={{display:'flex', alignItems:'center', gap:9, padding:'4px 8px 14px', borderBottom:`1px solid ${W.border}`, marginBottom:10}}>
          <LogoMark />
          <div style={{display:'flex', flexDirection:'column'}}>
            <span style={{fontWeight:600, fontSize:13.5, letterSpacing:-0.1}}>DevKit</span>
            <span style={{color:W.dim, fontSize:11, marginTop:-1}}>canton · v0.7.2</span>
          </div>
        </div>

        {/* Instance switcher */}
        <div style={{padding:'8px 10px', background:W.surface, border:`1px solid ${W.border}`, borderRadius:8, marginBottom:14}}>
          <div style={{display:'flex', alignItems:'center', gap:8}}>
            <Dot color={W.ok} pulse />
            <div style={{flex:1}}>
              <div style={{fontWeight:600, fontSize:12.5, letterSpacing:-0.1}}>{instance}</div>
              <div style={{color:W.dim, fontSize:10.5, marginTop:-1, fontFamily:wMono}}>splice 0.4.12 · 1h 22m</div>
            </div>
            <span style={{color:W.dim}}>⌄</span>
          </div>
        </div>

        <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.6, textTransform:'uppercase', padding:'4px 10px', fontWeight:600}}>Workspace</div>
        <nav style={{display:'flex', flexDirection:'column', gap:1, marginTop:4}}>
          {nav.map(n => (
            <div key={n.id} className="w-row" style={{
              display:'flex', alignItems:'center', gap:10, padding:'7px 10px', borderRadius:6,
              background: active === n.id ? W.surface2 : 'transparent',
              color: active === n.id ? W.text : W.text2,
              fontWeight: active === n.id ? 600 : 500, fontSize: 12.5,
              cursor:'pointer',
              borderLeft: active === n.id ? `2px solid ${W.brand}` : '2px solid transparent',
              paddingLeft: 10,
            }}>
              <span style={{width:14, textAlign:'center', color: active === n.id ? W.brand : W.dim}}>{n.icon}</span>
              <span style={{flex:1}}>{n.label}</span>
              {n.id==='explorer' && <span className="w-kbd">G E</span>}
              {n.id==='dar' && <span className="w-kbd">G D</span>}
            </div>
          ))}
        </nav>

        <div style={{marginTop:'auto', borderTop:`1px solid ${W.border}`, paddingTop:10}}>
          <div className="w-row" style={{display:'flex', alignItems:'center', gap:10, padding:'7px 10px', borderRadius:6, color:W.text2, cursor:'pointer', fontSize:12}}>
            <span style={{color:W.dim, fontSize:11}}>?</span>
            <span>Docs &amp; shortcuts</span>
            <span className="w-kbd" style={{marginLeft:'auto'}}>?</span>
          </div>
          <div className="w-row" style={{display:'flex', alignItems:'center', gap:10, padding:'8px 10px', borderRadius:6, color:W.text2, cursor:'pointer'}}>
            <div style={{width:22, height:22, borderRadius:'50%', background:'linear-gradient(135deg,#5BD7C5,#7CB5F7)', display:'flex', alignItems:'center', justifyContent:'center', color:'#082018', fontWeight:700, fontSize:10}}>ZL</div>
            <div style={{display:'flex', flexDirection:'column', fontSize:11.5}}>
              <span style={{color:W.text, fontWeight:500}}>Zhe Li</span>
              <span style={{color:W.dim, fontSize:10.5}}>BitDynamics</span>
            </div>
          </div>
        </div>
      </aside>

      {/* Main */}
      <div style={{display:'flex', flexDirection:'column', minWidth:0, position:'relative'}}>
        <TopBar topRight={topRight} />
        <main className="w-scroll" style={{flex:1, overflow:'auto', padding: '20px 28px 44px', minHeight:0}}>{children}</main>
        <footer style={{
          position:'absolute', bottom: 0, left: 0, right: 0,
          padding:'8px 28px', display:'flex', alignItems:'center', justifyContent:'space-between',
          background: `linear-gradient(180deg, transparent 0%, ${W.bg} 60%)`,
          color: W.faint, fontSize: 10.5, fontFamily: wMono, letterSpacing: 0.3,
          pointerEvents: 'none',
        }}>
          <span style={{display:'inline-flex', alignItems:'center', gap:8}}>
            <span style={{color: W.dim}}>built by</span>
            <LogoLockup height={13} color={W.text2} frame={W.dim} />
          </span>
          <span>canton-devkit v0.7.2 · splice 0.4.12</span>
        </footer>
      </div>
    </div>
  );
}

function TopBar({ topRight }) {
  return (
    <header style={{
      height:52, flex:'0 0 auto',
      borderBottom:`1px solid ${W.border}`,
      display:'flex', alignItems:'center',
      padding:'0 24px', gap: 14,
    }}>
      {/* breadcrumb */}
      <div style={{display:'flex', alignItems:'center', gap:8, color:W.dim, fontSize:12.5}}>
        <span>hubble</span>
        <span style={{color:W.faint}}>/</span>
        <span style={{color:W.text, fontWeight:600}}>Overview</span>
      </div>

      {/* command palette trigger */}
      <button style={{
        background: W.surface,
        border:`1px solid ${W.border}`, borderRadius:8,
        padding:'7px 12px',
        color: W.dim, fontFamily: wSans, fontSize: 12.5,
        display:'flex', alignItems:'center', gap:10,
        flex:1, maxWidth: 440, margin:'0 auto', cursor:'pointer',
      }}>
        <span style={{color:W.dim}}>⌕</span>
        <span style={{flex:1, textAlign:'left'}}>Run a command, jump to contract, find a party…</span>
        <span className="w-kbd">⌘K</span>
      </button>

      <div style={{display:'flex', alignItems:'center', gap:10, marginLeft:'auto'}}>
        {topRight || (
          <>
            <Pill color={W.ok}><Dot color={W.ok} pulse /> ledger healthy</Pill>
            <button className="w-iconbtn" title="Notifications">⌖</button>
            <button className="w-btn"><span style={{color:W.brand}}>+</span> New LocalNet</button>
          </>
        )}
      </div>
    </header>
  );
}

// ── Card / Section primitives ─────────────────────────────────────────────
function Card({ title, subtitle, action, children, pad = 16, style }) {
  return (
    <section style={{
      background: W.surface,
      border:`1px solid ${W.border}`,
      borderRadius:10,
      overflow:'hidden',
      ...style,
    }}>
      {(title || action) && (
        <header style={{
          padding:'12px 16px',
          borderBottom:`1px solid ${W.border}`,
          display:'flex', alignItems:'center', gap:12,
        }}>
          <div style={{flex:1, minWidth:0}}>
            {title && <div style={{fontWeight:600, fontSize:13, letterSpacing:-0.1}}>{title}</div>}
            {subtitle && <div style={{color:W.dim, fontSize:11.5, marginTop:1}}>{subtitle}</div>}
          </div>
          {action}
        </header>
      )}
      <div style={{padding: pad}}>{children}</div>
    </section>
  );
}

function Mono({ children, c = W.text }) {
  return <span style={{ fontFamily: wMono, color: c, fontSize: 12 }}>{children}</span>;
}

Object.assign(window, { W, wMono, wSans, AppShell, TopBar, Card, Mono, Pill, Dot, LogoMark, LogoLockup });
