// Create LocalNet flow — modal wizard, SSE-driven live progress, error states.
// Grounded in canton-devkit/internal/localnet/up.go and internal/docker.
//
// • Name validation: DNS-label regex from registry.validInstanceName
// • Version picker: catalogue + upstream rows from splice.CrossReferenceUpstream
//   statuses: supported / drifted / available / catalogued-only
// • Allow uncurated → opts.AllowUncurated (PR #20 #2)
// • 8 step bring-up + JWT capture, all SSE events from the instances topic
// • Errors mapped to ExitCodeError taxonomy (PreflightFail=2, Timeout=3, RuntimeFailure=4)
//   with the actual remediation strings from docker/remediation.go

// ── Modal shell ──────────────────────────────────────────────────────────
function ModalShell({ title, subtitle, width = 760, height, children, footer, onClose, accent }) {
  return (
    <div style={{
      position:'absolute', inset:0,
      background:'rgba(7,10,14,0.62)',
      backdropFilter:'blur(8px)', WebkitBackdropFilter:'blur(8px)',
      display:'flex', alignItems:'flex-start', justifyContent:'center',
      paddingTop: 64, zIndex: 20,
    }}>
      <div style={{
        width, maxHeight: height || 760,
        background: W.surface, border: `1px solid ${W.borderHi}`,
        borderRadius: 14, overflow:'hidden',
        boxShadow:'0 32px 60px -20px rgba(0,0,0,.7), 0 0 0 1px rgba(255,255,255,0.02) inset',
        display:'flex', flexDirection:'column', position:'relative',
      }}>
        {accent && <div style={{ height:3, background: accent }} />}
        <header style={{padding:'16px 20px', borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:12}}>
          <LogoMark size={20} />
          <div style={{flex:1, minWidth:0}}>
            <div style={{fontWeight:600, fontSize:14.5, letterSpacing:-0.2}}>{title}</div>
            {subtitle && <div style={{color:W.dim, fontSize:11.5, marginTop:1}}>{subtitle}</div>}
          </div>
          <button className="w-iconbtn" title="Close" onClick={onClose}>✕</button>
        </header>
        <div className="w-scroll" style={{overflow:'auto', flex:1}}>{children}</div>
        {footer && (
          <footer style={{
            padding:'12px 20px', borderTop:`1px solid ${W.border}`,
            background: W.bg, display:'flex', alignItems:'center', gap:12,
          }}>{footer}</footer>
        )}
      </div>
    </div>
  );
}

// ── Form field primitive ─────────────────────────────────────────────────
function Field({ label, hint, error, success, children, right }) {
  const c = error ? W.err : success ? W.ok : W.dim;
  return (
    <div style={{display:'flex', flexDirection:'column', gap:6, marginBottom: 14}}>
      <div style={{display:'flex', alignItems:'baseline', justifyContent:'space-between'}}>
        <label style={{fontSize: 11, color: W.text2, letterSpacing:0.4, fontWeight:600, textTransform:'uppercase'}}>{label}</label>
        {right}
      </div>
      {children}
      {(hint || error || success) && (
        <div style={{display:'flex', alignItems:'center', gap:6, fontSize:11.5, color: c, lineHeight:1.4}}>
          {error && <span>✕</span>}
          {success && <span>✓</span>}
          <span>{error || success || hint}</span>
        </div>
      )}
    </div>
  );
}

function TextInput({ value, placeholder, mono, error, success, prefix, suffix }) {
  const borderColor = error ? W.err : success ? `${W.ok}88` : W.border;
  return (
    <div style={{
      display:'flex', alignItems:'center',
      background: W.bg, border: `1px solid ${borderColor}`, borderRadius: 8,
      padding: '8px 12px', gap: 8,
      boxShadow: success ? `0 0 0 3px ${W.ok}10` : error ? `0 0 0 3px ${W.err}10` : 'none',
    }}>
      {prefix && <span style={{color:W.dim, fontFamily: mono ? wMono : wSans, fontSize: 13}}>{prefix}</span>}
      <span style={{
        flex:1, fontFamily: mono ? wMono : wSans,
        fontSize: 13, color: value ? W.text : W.dim, fontWeight: 500,
      }}>{value || placeholder}<span className="term-cursor" style={{background:W.brand}}/></span>
      {suffix && <span style={{color:W.dim, fontFamily: mono ? wMono : wSans, fontSize: 12}}>{suffix}</span>}
    </div>
  );
}

// ── Version picker row ──────────────────────────────────────────────────
function VersionRow({ tag, status, major, commit, note, selected, latest }) {
  const colors = {
    supported:       W.ok,
    drifted:         W.warn,
    available:       W.info,
    'catalogued-only': W.rose,
  };
  const c = colors[status] || W.dim;
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'auto 1fr 1.1fr .8fr 1.4fr',
      gap: 14, padding:'10px 14px', alignItems:'center',
      background: selected ? `${W.brand}14` : 'transparent',
      borderLeft: selected ? `2px solid ${W.brand}` : '2px solid transparent',
      paddingLeft: 12, borderBottom: `1px solid ${W.border}`, cursor:'pointer',
    }}>
      <span style={{
        width:14, height:14, borderRadius:'50%',
        border:`2px solid ${selected ? W.brand : W.faint}`,
        background: selected ? W.brand : 'transparent',
        boxShadow: selected ? `inset 0 0 0 3px ${W.surface}` : 'none',
      }} />
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <span style={{fontFamily:wMono, fontWeight:600, fontSize:13}}>{tag}</span>
        {latest && <Pill color={W.brand}>latest</Pill>}
        <Mono c={W.dim}>major {major}</Mono>
      </div>
      <Pill color={c}><Dot color={c} /> {status}</Pill>
      <Mono c={W.mag}>{commit}</Mono>
      <span style={{color:W.dim, fontSize:11.5}}>{note || (status === 'supported' ? 'curated, signed, reviewed' : '')}</span>
    </div>
  );
}

// ── Create modal — empty + validating ───────────────────────────────────
function CreateLocalNetModal({ stage = 'form' }) {
  // stage: 'form' (empty), 'valid' (filled + version picked + advanced)
  const filled = stage === 'valid';
  return (
    <ModalShell
      title="Create LocalNet"
      subtitle="Spin up a Canton + Splice stack. Takes ~90–180 seconds depending on cache."
      width={780}
      footer={
        <>
          <span style={{color: W.dim, fontSize: 11.5, display:'flex', alignItems:'center', gap:8}}>
            <Dot color={W.brand} pulse /> verifying preflight thresholds…
          </span>
          <span style={{flex:1}} />
          <button className="w-btn">Cancel <span className="w-kbd" style={{marginLeft:6}}>esc</span></button>
          <button className="w-btn primary" style={{ opacity: filled ? 1 : 0.55 }}>
            <span>⏵</span> Create LocalNet <span className="w-kbd" style={{marginLeft:6, color:'#082018', background:'rgba(0,0,0,0.18)', borderColor:'transparent'}}>⏎</span>
          </button>
        </>
      }
    >
      <div style={{padding:'18px 20px 6px'}}>
        <Field
          label="Name"
          hint={filled ? null : 'lowercase letters, digits, hyphens · 1–63 chars · must start and end with [a-z0-9]'}
          success={filled ? 'available · will be created at ~/.canton-devkit/localnet/demo' : null}
        >
          <TextInput
            mono prefix="~/.canton-devkit/localnet/"
            value={filled ? 'demo' : ''}
            placeholder="e.g. demo, pr-841, hubble"
            success={filled}
            suffix={filled ? '✓ valid DNS label' : null}
          />
        </Field>

        <Field
          label="Splice version"
          right={<span style={{display:'flex', gap:6}}>
            <button className="w-btn" style={{padding:'3px 8px', fontSize:11}}>↻ refresh</button>
            <Pill color={W.dim}>4 catalogued · 6 upstream</Pill>
          </span>}
          hint={filled ? null : 'cross-referenced against canton-network/splice releases'}
        >
          <div style={{background:W.bg, border:`1px solid ${W.border}`, borderRadius:8, overflow:'hidden'}}>
            <div style={{
              display:'grid', gridTemplateColumns:'auto 1fr 1.1fr .8fr 1.4fr',
              gap:14, padding:'8px 14px', color:W.dim, fontSize:10, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
              borderBottom:`1px solid ${W.border}`,
            }}>
              <span></span><span>Tag</span><span>Status</span><span>Commit</span><span>Note</span>
            </div>
            <VersionRow tag="0.6.4"  status="supported"     major="0.6" commit="578b7822d629" selected={filled} latest />
            <VersionRow tag="0.6.3"  status="supported"     major="0.6" commit="6d50c0396417" />
            <VersionRow tag="0.5.18" status="supported"     major="0.5" commit="b162650cd18d" />
            <VersionRow tag="0.6.5"  status="available"     major="0.6" commit="upstream a01c…" note="run scripts/add-splice-version.sh 0.6.5" />
            <VersionRow tag="0.6.2"  status="drifted"       major="0.6" commit="cataloged 3f1a…" note="upstream now 9b2c… · re-review entry" />
            <VersionRow tag="0.4.9"  status="catalogued-only" major="0.4" commit="b8aa12cd9f01" note="upstream tag deleted · investigate before removal" />
          </div>
        </Field>

        {/* Advanced options */}
        <details open={filled} style={{marginTop: 6, marginBottom: 14}}>
          <summary style={{
            cursor:'pointer', listStyle:'none', userSelect:'none',
            display:'flex', alignItems:'center', gap:8, color:W.text2, fontSize:12, fontWeight:600,
            padding:'8px 0',
          }}>
            <span style={{color:W.brand, transform: filled ? 'rotate(90deg)' : 'none', display:'inline-block', transition:'.15s'}}>▸</span>
            Advanced
            <span style={{color:W.dim, fontWeight:400, fontSize:11.5}}>·  cache, uncurated versions, profiles</span>
          </summary>
          <div style={{paddingLeft: 18, paddingTop: 8, paddingBottom: 4, borderLeft: `1px solid ${W.border}`, marginLeft: 6}}>
            <div style={{display:'grid', gridTemplateColumns:'1fr 1fr', gap: 14, marginBottom: 12}}>
              <Field label="Cache" hint="reuse the verified Splice tarball if present">
                <TextInput mono value="~/.canton-devkit/cache/splice/" />
              </Field>
              <Field label="Compose profile" hint="default ships sv + app-provider + app-user + multi-sync">
                <TextInput value="default" />
              </Field>
            </div>
            <div style={{display:'flex', gap:8, padding:'10px 12px', background: W.surface2, borderRadius:8, alignItems:'flex-start'}}>
              <span style={{
                width:24, height:14, borderRadius:8, background: W.faint,
                position:'relative', flex:'0 0 auto', marginTop:3,
              }}>
                <span style={{ position:'absolute', top:1, left:1, width:12, height:12, borderRadius:'50%', background:'#0B0E13' }} />
              </span>
              <div style={{flex:1}}>
                <div style={{fontSize:12.5, fontWeight:600}}>Allow uncurated Splice tags</div>
                <div style={{color:W.dim, fontSize:11.5, marginTop:2, lineHeight:1.45}}>Resolve <Mono c={W.text2}>--version</Mono> upstream when not in the catalogue. DevKit hasn't reviewed those bits — only enable for experiments.</div>
              </div>
              <span className="w-pill" style={{background:`${W.warn}1A`, color:W.warn, border:`1px solid ${W.warn}44`, alignSelf:'center'}}>off · recommended</span>
            </div>
          </div>
        </details>
      </div>

      {/* Footer info bar */}
      <div style={{padding:'10px 20px', borderTop:`1px solid ${W.border}`, background: W.bg, display:'flex', gap:20, fontSize:11.5}}>
        <span style={{color: W.dim}}>CLI equivalent:</span>
        <Mono c={W.brandText}>
          dpm localnet up --name {filled ? 'demo' : '<name>'} --version {filled ? '0.6.4' : '<tag>'}
        </Mono>
      </div>
    </ModalShell>
  );
}

// ── Step row for live progress ───────────────────────────────────────────
function ProgressStep({ status, label, detail, pct, time, children }) {
  const icons = {
    done:    <span style={{color:W.ok, fontSize:13}}>✓</span>,
    active:  <span style={{color:W.brand, display:'inline-block', animation:'term-spin 0.9s linear infinite', fontSize:13}}>⠹</span>,
    pending: <span style={{color:W.faint, fontSize:13}}>○</span>,
    warn:    <span style={{color:W.warn, fontSize:13}}>!</span>,
    fail:    <span style={{color:W.err, fontSize:13}}>✕</span>,
  };
  const color = status === 'done' ? W.text : status === 'active' ? W.text : status === 'fail' ? W.err : W.text2;
  return (
    <div style={{display:'flex', gap:12, padding: '8px 0', borderBottom: `1px dashed ${W.border}`}}>
      <span style={{ width:18, textAlign:'center', flex:'0 0 auto', paddingTop:1 }}>{icons[status]}</span>
      <div style={{flex:1, minWidth:0}}>
        <div style={{display:'flex', alignItems:'baseline', gap:8}}>
          <span style={{fontSize:13, fontWeight: status === 'active' ? 600 : 500, color, flex:1}}>{label}</span>
          {time && <span style={{color: W.dim, fontSize: 11, fontFamily: wMono}}>{time}</span>}
        </div>
        {detail && <div style={{color: status === 'active' ? W.text2 : W.dim, fontSize: 11.5, marginTop: 2, fontFamily: wMono}}>{detail}</div>}
        {pct !== undefined && (
          <div style={{marginTop:6, height:5, background: W.surface2, borderRadius:3, overflow:'hidden'}}>
            <div style={{
              width: `${pct}%`, height:'100%',
              background: `linear-gradient(90deg, ${W.brand}, ${W.brandText})`,
              borderRadius: 3, transition: 'width .25s',
            }} />
          </div>
        )}
        {children}
      </div>
    </div>
  );
}

// ── Live progress modal — SSE stream visualization ──────────────────────
function CreateProgressModal() {
  return (
    <ModalShell
      title="Bringing up · demo"
      subtitle="Splice 0.6.4 · adapter 0.6 · streaming via /api/instances/demo/events"
      accent={W.brand}
      width={780}
      footer={
        <>
          <span style={{display:'flex', alignItems:'center', gap:8, fontSize:11.5, color: W.dim}}>
            <span style={{
              display:'inline-block', width:8, height:8, borderRadius:'50%', background: W.brand,
            }} className="w-pulse" />
            <span>SSE · live · 0:48 elapsed · est. 1m remaining</span>
          </span>
          <span style={{flex:1}} />
          <button className="w-btn" style={{color:W.warn}}>Detach</button>
          <button className="w-btn danger">Cancel bring-up <span className="w-kbd" style={{marginLeft:6}}>⌃C</span></button>
        </>
      }
    >
      {/* Top progress bar with stage badges */}
      <div style={{padding:'14px 20px', borderBottom:`1px solid ${W.border}`}}>
        <div style={{display:'flex', alignItems:'baseline', justifyContent:'space-between', marginBottom:10}}>
          <div style={{display:'flex', alignItems:'baseline', gap:10}}>
            <span style={{fontSize:24, fontWeight:600, letterSpacing:-0.5}}>54<span style={{color:W.dim, fontSize:14, fontWeight:500}}>%</span></span>
            <span style={{color:W.brandText, fontSize:13, fontWeight:600}}>Starting services</span>
            <span style={{color:W.dim, fontSize:12}}>step 6 of 8</span>
          </div>
          <Mono c={W.dim}>POST /api/instances → 202 accepted</Mono>
        </div>
        <div style={{height:8, background: W.surface2, borderRadius:4, overflow:'hidden', position:'relative'}}>
          <div style={{
            position:'absolute', inset:0, width:'54%',
            background:`linear-gradient(90deg, ${W.brand}, ${W.brandText})`,
            borderRadius:4, boxShadow: `0 0 12px ${W.brand}88`,
          }} />
        </div>
      </div>

      {/* Steps */}
      <div style={{padding:'4px 20px 8px'}}>
        <ProgressStep status="done"   label="Resolve version + adapter"
          detail="splice@0.6.4 · commit 578b7822 · adapter 0.6 (curated)" time="0.1s" />
        <ProgressStep status="done"   label="Acquire instance lock"
          detail="locked ~/.canton-devkit/localnet/demo/.lock" time="0.0s" />
        <ProgressStep status="done"   label="Run preflight checks"
          detail="docker v25.0.5 · compose v2.30.1 · 24 GiB ram · 38 GiB disk" time="1.4s" />
        <ProgressStep status="done"   label="Fetch Splice LocalNet"
          detail="cache miss · 137.5 MiB downloaded · content-sha verified · db1e1336dc4e" time="11.2s" />
        <ProgressStep status="done"   label="Persist state + write overlay"
          detail="state.json (creating) · container-rename overlay → demo-*" time="0.2s" />
        <ProgressStep status="active" label="Starting services" pct={71}
          detail="docker compose -p canton-demo up -d --wait  ·  11/15 containers up">
          {/* per-container chips */}
          <div style={{display:'flex', flexWrap:'wrap', gap:6, marginTop:8}}>
            {[
              ['postgres', 'done'], ['sv-canton', 'done'], ['app-provider-canton', 'done'],
              ['app-user-canton', 'done'], ['global-synchronizer', 'done'],
              ['app-synchronizer', 'done'], ['sv-validator', 'done'], ['sv-wallet-ui', 'done'],
              ['app-provider-validator', 'done'], ['app-provider-wallet-ui', 'done'],
              ['scan-cns', 'done'],
              ['app-user-validator', 'active'], ['app-user-wallet-ui', 'pending'],
              ['swagger-ui', 'pending'], ['nginx', 'pending'],
            ].map(([name, st]) => {
              const c = st === 'done' ? W.ok : st === 'active' ? W.brand : W.faint;
              return (
                <span key={name} className="w-pill" style={{
                  background: `${c}10`, border: `1px solid ${c}44`, color: st === 'pending' ? W.dim : W.text2,
                  fontFamily: wMono, fontSize: 10.5, padding: '3px 8px',
                }}>
                  <span style={{
                    width: 6, height: 6, borderRadius: '50%', background: c,
                    display:'inline-block', marginRight:6,
                    ...(st === 'active' ? { animation: 'w-pulse 1.4s infinite' } : {}),
                  }} />{name}
                </span>
              );
            })}
          </div>
        </ProgressStep>
        <ProgressStep status="pending" label="Wait for services to become healthy"
          detail="watches docker compose --wait + readiness probes · timeout 5m" />
        <ProgressStep status="pending" label="Capture JWTs · register endpoints"
          detail="signs dev-secret tokens for sv, app-provider, app-user; mark running" />
      </div>

      {/* Live event console */}
      <div style={{
        margin:'0 20px 16px', background: W.bg, border: `1px solid ${W.border}`, borderRadius: 8,
        padding: '10px 12px', fontFamily: wMono, fontSize: 11, lineHeight: 1.65, color: W.text2,
        maxHeight: 150, overflow: 'hidden',
      }}>
        <div style={{color: W.dim, fontSize: 10, letterSpacing: 1.3, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4, fontFamily: wSans}}>SSE event log · /api/instances/demo/events</div>
        <div><span style={{color:W.faint}}>0:47.812 </span><span style={{color:W.info}}>step.progress  </span>services <span style={{color:W.brand}}>11/15</span> healthy · app-user-validator starting</div>
        <div><span style={{color:W.faint}}>0:46.221 </span><span style={{color:W.ok}}>container.up   </span>scan-cns · healthy in 4.2s</div>
        <div><span style={{color:W.faint}}>0:42.108 </span><span style={{color:W.ok}}>container.up   </span>app-provider-wallet-ui · healthy in 6.1s</div>
        <div><span style={{color:W.faint}}>0:38.044 </span><span style={{color:W.ok}}>container.up   </span>sv-wallet-ui · healthy in 5.8s</div>
        <div><span style={{color:W.faint}}>0:33.901 </span><span style={{color:W.brand}}>step.start     </span>starting services · docker compose up -d --wait</div>
        <div><span style={{color:W.faint}}>0:33.612 </span><span style={{color:W.ok}}>step.done      </span>persist state · overlay written<span className="term-cursor" style={{background:W.brand}}/></div>
      </div>
    </ModalShell>
  );
}

// ── Error modal: PORTS_IN_USE (preflight fail · exit 2) ─────────────────
function CreateErrorPortsModal() {
  return (
    <ModalShell
      title="Bring-up failed · ports in use"
      subtitle="exit code 2 · ExitPreflightFail · no state was written"
      accent={W.err}
      width={780}
      footer={
        <>
          <span style={{color:W.dim, fontSize:11.5}}>retry preserves your name + version selections</span>
          <span style={{flex:1}} />
          <button className="w-btn">Edit options</button>
          <button className="w-btn">Run doctor</button>
          <button className="w-btn primary"><span>↻</span> Retry</button>
        </>
      }
    >
      <div style={{padding:'18px 20px 8px'}}>
        {/* Error banner */}
        <div style={{
          background: `${W.err}10`, border: `1px solid ${W.err}44`,
          borderRadius: 10, padding: '14px 16px', marginBottom: 14,
          display: 'flex', gap: 12,
        }}>
          <span style={{
            width:32, height:32, borderRadius: 8,
            background: `${W.err}22`, color: W.err,
            display:'flex', alignItems:'center', justifyContent:'center',
            fontSize: 18, fontWeight: 700, flex:'0 0 auto',
          }}>!</span>
          <div style={{flex:1}}>
            <div style={{display:'flex', alignItems:'center', gap:10, flexWrap:'wrap', marginBottom: 4}}>
              <span style={{fontWeight:600, fontSize:14}}>2 ports busy on this host</span>
              <Pill color={W.err}>code PORTS_IN_USE</Pill>
              <Pill color={W.dim}>ExitPreflightFail (2)</Pill>
            </div>
            <div style={{color: W.text2, fontSize: 12.5, lineHeight: 1.55}}>
              DevKit allocates host ports ephemerally, but two UI host ports the previous instance held are still bound by other processes. Free them, pick different ports, or tear down the conflicting instance and retry.
            </div>
          </div>
        </div>

        {/* Conflicting ports */}
        <div style={{
          background: W.bg, border: `1px solid ${W.border}`, borderRadius: 8, overflow: 'hidden',
          marginBottom: 14,
        }}>
          <div style={{
            display: 'grid', gridTemplateColumns: '90px 1fr 1fr 1fr',
            gap: 14, padding: '8px 14px', color: W.dim, fontSize: 10, letterSpacing: 1.4, textTransform: 'uppercase', fontWeight: 600,
            borderBottom: `1px solid ${W.border}`,
          }}>
            <span>Port</span><span>Service</span><span>Held by</span><span>Suggested fix</span>
          </div>
          {[
            ['4885', 'app-provider UI', 'pid 88341 · orbstack-vmgr', 'kill or auto-rebind to 5885'],
            ['4887', 'sv UI',            'pid 88450 · grafana',       'stop grafana or rebind to 5887'],
          ].map((r,i) => (
            <div key={i} style={{
              display: 'grid', gridTemplateColumns: '90px 1fr 1fr 1fr',
              gap: 14, padding: '10px 14px', alignItems:'center',
              borderBottom: i < 1 ? `1px solid ${W.border}` : 'none',
            }}>
              <Mono c={W.err}>{r[0]}</Mono>
              <span style={{fontSize:12.5}}>{r[1]}</span>
              <Mono c={W.text2}>{r[2]}</Mono>
              <span style={{fontSize:12, color:W.text2}}>{r[3]}</span>
            </div>
          ))}
        </div>

        {/* Three remediation paths */}
        <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom: 8}}>How to fix</div>
        {[
          { n:1, title:'Auto-rebind to fresh ports',  detail:'DevKit picks a new ephemeral port pair · your URLs change once',                 cli:'dpm localnet up --name demo --version 0.6.4 --rebind-ports', primary: true },
          { n:2, title:'Free the conflicting PIDs',   detail:'Stop the bound processes; the next bring-up reuses your previous URLs',         cli:'lsof -ti :4885,4887 | xargs kill' },
          { n:3, title:'Tear down the other instance', detail:'If you have a stale DevKit instance still owning those ports, remove it first', cli:'dpm localnet down --name <other> && dpm localnet up --name demo' },
        ].map(o => (
          <div key={o.n} className="w-row" style={{
            display:'flex', gap: 12, padding: '12px 14px', marginBottom: 8,
            background: o.primary ? `${W.brand}10` : W.bg,
            border: `1px solid ${o.primary ? W.brand+'44' : W.border}`, borderRadius: 8, alignItems:'flex-start',
          }}>
            <span style={{
              width:22, height:22, borderRadius: 7,
              background: o.primary ? W.brand : W.surface2,
              color: o.primary ? '#082018' : W.brand,
              display:'flex', alignItems:'center', justifyContent:'center', fontSize:12, fontWeight:700, flex:'0 0 auto',
            }}>{o.n}</span>
            <div style={{flex:1}}>
              <div style={{fontSize:13, fontWeight:600, marginBottom: 2}}>{o.title}</div>
              <div style={{color: W.dim, fontSize: 11.5, marginBottom: 6}}>{o.detail}</div>
              <Mono c={W.brandText}>{o.cli}</Mono>
            </div>
          </div>
        ))}
      </div>

      <div style={{padding: '10px 20px', borderTop: `1px solid ${W.border}`, background: W.bg, display:'flex', gap: 16, fontSize:11.5}}>
        <span style={{color: W.dim, display:'flex', alignItems:'center', gap:6}}>
          <span style={{color: W.brand}}>↗</span> error reference
        </span>
        <Mono c={W.text2}>devkit.dev/e/PORTS_IN_USE</Mono>
        <span style={{flex:1}} />
        <Mono c={W.dim}>trace-id 0x9c41…e210</Mono>
      </div>
    </ModalShell>
  );
}

// ── Error modal: DOCKER_DOWN (preflight fail · exit 2) ─────────────────
function CreateErrorDockerModal() {
  return (
    <ModalShell
      title="Bring-up failed · Docker daemon unreachable"
      subtitle="exit code 2 · ExitPreflightFail · preflight halted before any state was written"
      accent={W.err}
      width={780}
      footer={
        <>
          <span style={{color:W.dim, fontSize:11.5}}>nothing was changed on this host</span>
          <span style={{flex:1}} />
          <button className="w-btn">View doctor output</button>
          <button className="w-btn primary"><span>↻</span> Retry once Docker is up</button>
        </>
      }
    >
      <div style={{padding:'18px 20px 8px'}}>
        {/* Banner */}
        <div style={{
          background: `${W.err}10`, border: `1px solid ${W.err}44`,
          borderRadius: 10, padding: '14px 16px', marginBottom: 14,
          display: 'flex', gap: 12,
        }}>
          <span style={{
            width:32, height:32, borderRadius: 8,
            background: `${W.err}22`, color: W.err,
            display:'flex', alignItems:'center', justifyContent:'center',
            fontSize: 18, fontWeight: 700, flex:'0 0 auto',
          }}>!</span>
          <div style={{flex:1}}>
            <div style={{display:'flex', alignItems:'center', gap:10, flexWrap:'wrap', marginBottom: 4}}>
              <span style={{fontWeight:600, fontSize:14}}>Cannot reach the Docker daemon</span>
              <Pill color={W.err}>code DOCKER_DOWN</Pill>
              <Pill color={W.dim}>ExitPreflightFail (2)</Pill>
            </div>
            <div style={{color: W.text2, fontSize: 12.5, lineHeight: 1.55}}>
              <Mono c={W.text}>docker info</Mono> returned a connection error within the 10s preflight timeout. Splice + Canton run as containers, so DevKit needs a working daemon before it can do anything.
            </div>
          </div>
        </div>

        {/* Preflight summary */}
        <div style={{background: W.bg, border: `1px solid ${W.border}`, borderRadius: 8, padding: '12px 16px', marginBottom: 14}}>
          <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom: 8}}>Preflight summary</div>
          {[
            ['done', 'Docker CLI present',     'v25.0.5'],
            ['fail', 'Docker daemon reachable', 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock'],
            ['warn', 'Compose v2',              'skipped — depends on daemon'],
            ['warn', 'Memory ≥ 4 GiB',          'skipped — depends on daemon'],
            ['warn', 'Disk ≥ 10 GiB',           'skipped — depends on daemon'],
          ].map((r,i) => (
            <ProgressStep key={i} status={r[0]} label={r[1]} detail={r[2]} />
          ))}
        </div>

        {/* Remediation — pulled verbatim from docker/remediation.go */}
        <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginBottom: 8}}>Remediation</div>
        <div style={{
          background: `${W.brand}08`, border: `1px solid ${W.brand}44`, borderRadius: 8,
          padding: '12px 16px', display:'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14,
        }}>
          {[
            { os: 'macOS',   label:'Docker Desktop',  cli:'open -a Docker', detail:'Start Docker Desktop from Applications, then retry.' },
            { os: 'Linux',   label:'systemd',         cli:'sudo systemctl start docker', detail:'Start the daemon; enable on boot with `systemctl enable docker`.' },
            { os: 'Windows', label:'Docker Desktop',  cli:'Start-Process "Docker Desktop"', detail:'Start Docker Desktop from the Start menu, then retry.' },
          ].map(r => (
            <div key={r.os}>
              <div style={{fontSize:11, color:W.brand, fontWeight:700, letterSpacing:0.4, textTransform:'uppercase', marginBottom:4}}>{r.os}</div>
              <div style={{fontSize:12.5, color:W.text, fontWeight:500, marginBottom:6}}>{r.label}</div>
              <Mono c={W.text2}>{r.cli}</Mono>
              <div style={{color:W.dim, fontSize:11, marginTop:6, lineHeight:1.45}}>{r.detail}</div>
            </div>
          ))}
        </div>
      </div>

      <div style={{padding: '10px 20px', borderTop: `1px solid ${W.border}`, background: W.bg, display:'flex', gap: 16, fontSize:11.5}}>
        <span style={{color: W.dim}}>error reference</span>
        <Mono c={W.text2}>devkit.dev/e/DOCKER_DOWN</Mono>
        <span style={{flex:1}} />
        <Mono c={W.dim}>trace-id 0x77a3…1c80</Mono>
      </div>
    </ModalShell>
  );
}

// ── Wrapper screens — modal-over-dashboard ──────────────────────────────
function CreateLocalNetFormScreen()    { return <div style={{position:'relative', width:'100%', height:'100%'}}><DashboardScreen/><CreateLocalNetModal stage="form" /></div>; }
function CreateLocalNetFilledScreen()  { return <div style={{position:'relative', width:'100%', height:'100%'}}><DashboardScreen/><CreateLocalNetModal stage="valid" /></div>; }
function CreateProgressScreen()        { return <div style={{position:'relative', width:'100%', height:'100%'}}><DashboardScreen/><CreateProgressModal/></div>; }
function CreateErrorPortsScreen()      { return <div style={{position:'relative', width:'100%', height:'100%'}}><DashboardScreen/><CreateErrorPortsModal/></div>; }
function CreateErrorDockerScreen()     { return <div style={{position:'relative', width:'100%', height:'100%'}}><DashboardScreen/><CreateErrorDockerModal/></div>; }

Object.assign(window, {
  CreateLocalNetFormScreen,
  CreateLocalNetFilledScreen,
  CreateProgressScreen,
  CreateErrorPortsScreen,
  CreateErrorDockerScreen,
});
