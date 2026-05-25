// Terminal primitives — a sleek window chrome + tokenized line renderer.
// Color tokens are exposed on window so screen files can use them.

const TERM = {
  bg: '#0E1116',
  panel: '#13171E',
  panelBorder: '#1F2630',
  chrome: '#161B22',
  text: '#D6DAE0',
  dim: '#6B7380',
  mid: '#8B95A1',
  faint: '#3B434F',
  brand: '#5BD7C5',          // mint/teal — CantonDevKit accent
  brandSoft: '#2A6F66',
  success: '#5EE2A0',
  warn: '#F5BF55',
  error: '#F47C7C',
  info: '#7CB5F7',
  magenta: '#C4A8F5',
  amber: '#E8A14E',
  rose: '#F08FB5',
  selBg: '#1B232E',
};

const fontMono = "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace";
const fontSans = "'Inter Tight', 'IBM Plex Sans', system-ui, sans-serif";

// One-time css for blinking cursor + spinner
if (typeof document !== 'undefined' && !document.getElementById('term-styles')) {
  const s = document.createElement('style');
  s.id = 'term-styles';
  s.textContent = `
    @keyframes term-blink { 0%,49%{opacity:1} 50%,100%{opacity:0} }
    @keyframes term-spin { to { transform: rotate(360deg) } }
    @keyframes term-bar {
      0% { transform: translateX(-100%); }
      100% { transform: translateX(400%); }
    }
    .term-cursor { display:inline-block; width:.55em; height:1.05em; background:${TERM.brand}; vertical-align:-0.18em; margin-left:1px; animation: term-blink 1s steps(1) infinite; }
    .term-bar-track { position:relative; height:6px; background:${TERM.faint}; border-radius:3px; overflow:hidden; }
    .term-bar-fill { position:absolute; inset:0; background: linear-gradient(90deg, transparent, ${TERM.brand}, transparent); width:25%; animation: term-bar 1.2s linear infinite; }
  `;
  document.head.appendChild(s);
}

// ── Terminal window shell ────────────────────────────────────────────────
function Terminal({ title = "~/canton-app", subtitle, width = '100%', height = 'auto', children, footer, density = 'normal' }) {
  const pad = density === 'tight' ? '10px 14px' : '14px 18px';
  return (
    <div style={{
      width, height,
      background: TERM.bg,
      border: `1px solid ${TERM.panelBorder}`,
      borderRadius: 10,
      overflow: 'hidden',
      fontFamily: fontMono,
      color: TERM.text,
      fontSize: 12.5,
      lineHeight: 1.55,
      display: 'flex', flexDirection: 'column',
      boxShadow: '0 18px 40px -22px rgba(0,0,0,.55), 0 2px 0 rgba(255,255,255,.02) inset',
    }}>
      {/* chrome */}
      <div style={{
        background: TERM.chrome,
        borderBottom: `1px solid ${TERM.panelBorder}`,
        padding: '8px 12px',
        display: 'flex', alignItems: 'center', gap: 10,
        flex: '0 0 auto',
      }}>
        <div style={{ display: 'flex', gap: 6 }}>
          <span style={{ width: 11, height: 11, borderRadius: '50%', background: '#3a3f48' }} />
          <span style={{ width: 11, height: 11, borderRadius: '50%', background: '#3a3f48' }} />
          <span style={{ width: 11, height: 11, borderRadius: '50%', background: '#3a3f48' }} />
        </div>
        <div style={{ flex: 1, textAlign: 'center', fontSize: 11.5, color: TERM.dim, letterSpacing: 0.2 }}>
          {title}{subtitle && <span style={{ color: TERM.faint }}> — {subtitle}</span>}
        </div>
        <span style={{ display:'inline-flex', alignItems:'center', gap: 5, paddingRight: 4 }}>
          <svg width="11" height="11" viewBox="0 0 64 64" aria-hidden="true">
            <rect x="10" y="10" width="44" height="44" fill="none" stroke={TERM.faint} strokeWidth="4" />
            <rect x="22" y="22" width="20" height="20" fill="#CCFB6A" />
            <line x1="32" y1="0"  x2="32" y2="8"  stroke="#CCFB6A" strokeWidth="5" />
            <line x1="32" y1="56" x2="32" y2="64" stroke={TERM.faint} strokeWidth="4" />
          </svg>
          <span style={{ fontSize: 10, color: TERM.faint, letterSpacing: 0.6, fontFamily: fontMono, fontWeight: 600 }}>BITDYNAMICS</span>
        </span>
      </div>
      {/* body */}
      <div style={{ padding: pad, flex: 1, overflow: 'hidden' }}>{children}</div>
      {footer && (
        <div style={{
          borderTop: `1px solid ${TERM.panelBorder}`,
          background: '#10141A',
          padding: '6px 14px',
          color: TERM.dim, fontSize: 11,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>{footer}</div>
      )}
    </div>
  );
}

// ── Single visible line in the terminal ─────────────────────────────────
function Line({ children, indent = 0, dim = false, style }) {
  return (
    <div style={{
      paddingLeft: indent * 12,
      color: dim ? TERM.dim : 'inherit',
      whiteSpace: 'pre-wrap',
      ...style,
    }}>{children || '\u00A0'}</div>
  );
}

// ── Inline tokens (colored spans) ────────────────────────────────────────
const C = {
  brand: (c) => <span style={{ color: TERM.brand }}>{c}</span>,
  ok: (c) => <span style={{ color: TERM.success }}>{c}</span>,
  warn: (c) => <span style={{ color: TERM.warn }}>{c}</span>,
  err: (c) => <span style={{ color: TERM.error }}>{c}</span>,
  info: (c) => <span style={{ color: TERM.info }}>{c}</span>,
  mag: (c) => <span style={{ color: TERM.magenta }}>{c}</span>,
  amber: (c) => <span style={{ color: TERM.amber }}>{c}</span>,
  rose: (c) => <span style={{ color: TERM.rose }}>{c}</span>,
  dim: (c) => <span style={{ color: TERM.dim }}>{c}</span>,
  mid: (c) => <span style={{ color: TERM.mid }}>{c}</span>,
  bold: (c) => <span style={{ fontWeight: 600, color: '#EEF1F4' }}>{c}</span>,
  underline: (c) => <span style={{ textDecoration: 'underline', textDecorationColor: TERM.brandSoft, textUnderlineOffset: 3 }}>{c}</span>,
  hl: (c) => <span style={{ background: TERM.selBg, padding: '0 4px', borderRadius: 3 }}>{c}</span>,
  key: (c) => <span style={{ color: TERM.brand, fontWeight: 600 }}>{c}</span>,
};

// ── Prompt line (with cursor option) ─────────────────────────────────────
function Prompt({ user = 'dev', host = 'devnet', dir = '~/canton-app', cmd, cursor = false, branch }) {
  return (
    <div style={{ marginBottom: 4 }}>
      <span style={{ color: TERM.success }}>{user}</span>
      <span style={{ color: TERM.dim }}>@</span>
      <span style={{ color: TERM.info }}>{host}</span>
      <span style={{ color: TERM.dim }}>:</span>
      <span style={{ color: TERM.magenta }}>{dir}</span>
      {branch && <span style={{ color: TERM.amber }}> {' '}({branch})</span>}
      <span style={{ color: TERM.dim }}> ❯ </span>
      <span style={{ color: TERM.text }}>{cmd}</span>
      {cursor && <span className="term-cursor" />}
    </div>
  );
}

// ── Symbols ──────────────────────────────────────────────────────────────
const Sym = {
  check: <span style={{ color: TERM.success }}>✓</span>,
  cross: <span style={{ color: TERM.error }}>✗</span>,
  warn:  <span style={{ color: TERM.warn }}>!</span>,
  arrow: <span style={{ color: TERM.brand }}>❯</span>,
  dot:   <span style={{ color: TERM.brand }}>•</span>,
  pending: <span style={{ color: TERM.dim }}>○</span>,
  busy: <span style={{ color: TERM.brand, display: 'inline-block', width: 12 }}>
          <span style={{ display:'inline-block', animation: 'term-spin 0.9s linear infinite', transformOrigin: '50% 50%' }}>⠹</span>
        </span>,
};

// ── Step row (used in up/doctor/upload) ──────────────────────────────────
function Step({ icon = 'check', label, detail, time }) {
  const iconEl = Sym[icon] || Sym.check;
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, padding: '1px 0' }}>
      <span style={{ width: 12, display: 'inline-block', textAlign: 'center' }}>{iconEl}</span>
      <span style={{ flex: 1, color: TERM.text }}>{label}</span>
      {detail && <span style={{ color: TERM.dim }}>{detail}</span>}
      {time && <span style={{ color: TERM.faint, fontSize: 11 }}>{time}</span>}
    </div>
  );
}

// ── Section header (e.g. "ENDPOINTS") ───────────────────────────────────
function Section({ title, right, children, gap = 4 }) {
  return (
    <div style={{ marginTop: 10 }}>
      <div style={{
        display:'flex', justifyContent:'space-between', alignItems:'center',
        color: TERM.brand, fontSize: 10.5, letterSpacing: 1.6, textTransform: 'uppercase',
        borderBottom: `1px solid ${TERM.faint}`, paddingBottom: 4, marginBottom: 6,
        fontWeight: 600,
      }}>
        <span>{title}</span>{right && <span style={{ color: TERM.dim, letterSpacing: 0.4, textTransform:'none', fontSize: 11 }}>{right}</span>}
      </div>
      <div style={{ display:'flex', flexDirection:'column', gap }}>{children}</div>
    </div>
  );
}

// ── Key-value row ────────────────────────────────────────────────────────
function KV({ k, v, kw = 140, mono = true }) {
  return (
    <div style={{ display:'flex', gap: 12 }}>
      <span style={{ color: TERM.dim, width: kw, flex: '0 0 auto' }}>{k}</span>
      <span style={{ color: TERM.text, fontFamily: mono ? fontMono : 'inherit' }}>{v}</span>
    </div>
  );
}

// ── Table renderer ───────────────────────────────────────────────────────
function Table({ columns, rows, dense = false }) {
  const rowPad = dense ? '3px 0' : '5px 0';
  return (
    <div style={{ width: '100%' }}>
      <div style={{
        display:'grid',
        gridTemplateColumns: columns.map(c => c.w || '1fr').join(' '),
        gap: 14,
        color: TERM.brand, fontSize: 10.5, letterSpacing: 1.4, textTransform:'uppercase',
        borderBottom: `1px solid ${TERM.faint}`, paddingBottom: 5, marginBottom: 4,
        fontWeight: 600,
      }}>
        {columns.map((c, i) => <span key={i} style={{ textAlign: c.align || 'left' }}>{c.label}</span>)}
      </div>
      {rows.map((r, i) => (
        <div key={i} style={{
          display:'grid',
          gridTemplateColumns: columns.map(c => c.w || '1fr').join(' '),
          gap: 14, padding: rowPad,
          borderBottom: i < rows.length - 1 ? `1px dashed ${TERM.panelBorder}` : 'none',
        }}>
          {r.map((cell, j) => (
            <span key={j} style={{ textAlign: columns[j].align || 'left', overflow:'hidden', textOverflow:'ellipsis', whiteSpace:'nowrap' }}>{cell}</span>
          ))}
        </div>
      ))}
    </div>
  );
}

// ── Box (used for ready banners, callouts) ──────────────────────────────
function Box({ children, accent = TERM.brand, bg = 'transparent', pad = '10px 14px', style }) {
  return (
    <div style={{
      borderLeft: `2px solid ${accent}`,
      background: bg,
      padding: pad,
      ...style,
    }}>{children}</div>
  );
}

// Export to window for cross-file usage
Object.assign(window, {
  TERM, C, Sym, fontMono, fontSans,
  Terminal, Line, Prompt, Step, Section, KV, Table, Box,
});
