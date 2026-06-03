// Metrics — Canton-specific Grafana dashboard preset for DApp developers
// and AI Agent Skill documentation viewer.

function AreaChart({ data, color = W.brand, height = 120 }) {
  const max = Math.max(...data), min = Math.min(...data);
  const range = max - min || 1;
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * 100;
    const y = 100 - ((v - min) / range) * 85 - 5;
    return `${x},${y}`;
  });
  return (
    <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{width:'100%', height, display:'block'}}>
      <defs>
        <linearGradient id={`g-${color.slice(1)}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.32" />
          <stop offset="100%" stopColor={color} stopOpacity="0.02" />
        </linearGradient>
      </defs>
      {/* grid */}
      {[20,40,60,80].map(y => (
        <line key={y} x1="0" y1={y} x2="100" y2={y} stroke={W.border} strokeWidth="0.2" />
      ))}
      <polygon points={`0,100 ${pts.join(' ')} 100,100`} fill={`url(#g-${color.slice(1)})`} />
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth="1.4" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function MultiLine({ series, height = 140, labels }) {
  return (
    <div style={{position:'relative'}}>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{width:'100%', height, display:'block'}}>
        {[20,40,60,80].map(y => (
          <line key={y} x1="0" y1={y} x2="100" y2={y} stroke={W.border} strokeWidth="0.2" />
        ))}
        {series.map((s, si) => {
          const max = Math.max(...s.data), min = Math.min(...s.data);
          const range = max - min || 1;
          const pts = s.data.map((v, i) => `${(i/(s.data.length-1))*100},${100 - ((v - min)/range)*80 - 10}`);
          return (
            <polyline key={si} points={pts.join(' ')} fill="none" stroke={s.color} strokeWidth="1.4" vectorEffect="non-scaling-stroke" />
          );
        })}
      </svg>
      {labels && (
        <div style={{display:'flex', gap:14, marginTop:8, flexWrap:'wrap'}}>
          {series.map((s, i) => (
            <div key={i} style={{display:'flex', alignItems:'center', gap:6, fontSize:11.5, color:W.text2}}>
              <span style={{width:10, height:2, background:s.color, borderRadius:1}} />
              <span>{s.label}</span>
              <span style={{color:W.dim, fontFamily:wMono}}>{s.data[s.data.length-1]}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function BarChart({ data, color = W.brand, height = 120 }) {
  const max = Math.max(...data);
  return (
    <div style={{display:'flex', alignItems:'flex-end', gap:3, height}}>
      {data.map((v, i) => (
        <div key={i} style={{flex:1, height: `${(v/max)*100}%`, background: color, opacity:0.85, borderRadius: '2px 2px 0 0'}} />
      ))}
    </div>
  );
}

function Heatmap({ rows, cols = 24 }) {
  const cells = rows * cols;
  return (
    <div style={{display:'grid', gridTemplateColumns:`repeat(${cols}, 1fr)`, gap:2}}>
      {Array.from({length: cells}).map((_, i) => {
        const v = Math.random();
        const a = v < 0.3 ? 0.05 : v < 0.55 ? 0.2 : v < 0.8 ? 0.5 : 0.9;
        return <div key={i} style={{aspectRatio:'1/1', background: `${W.brand}`, opacity: a, borderRadius:2}} />
      })}
    </div>
  );
}

function MetricCard({ title, value, unit, delta, deltaColor = W.ok, children, span = 1 }) {
  return (
    <Card pad={14} style={{ gridColumn: `span ${span}` }}>
      <div style={{display:'flex', alignItems:'baseline', justifyContent:'space-between', marginBottom:4}}>
        <span style={{color:W.dim, fontSize:11, letterSpacing:1.2, textTransform:'uppercase', fontWeight:600}}>{title}</span>
        {delta && <span style={{color:deltaColor, fontSize:11.5, fontWeight:500}}>{delta}</span>}
      </div>
      <div style={{display:'flex', alignItems:'baseline', gap:6, marginBottom:8}}>
        <span style={{fontSize:24, fontWeight:600, letterSpacing:-0.5}}>{value}</span>
        {unit && <span style={{color:W.dim, fontSize:12}}>{unit}</span>}
      </div>
      {children}
    </Card>
  );
}

function MetricsScreen() {
  return (
    <AppShell active="metrics" instance="hubble"
      topRight={<>
        <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>auto-refresh 5s</span>
        <button className="w-btn">Last 1h ⌄</button>
        <button className="w-btn">Open in Grafana ↗</button>
      </>}>
      <section style={{marginBottom:14, display:'flex', alignItems:'flex-end', justifyContent:'space-between'}}>
        <div>
          <h1 style={{margin:'0 0 3px', fontSize:20, fontWeight:600, letterSpacing:-0.4}}>DApp Developer Metrics</h1>
          <div style={{color:W.dim, fontSize:12.5}}>Canton-specific Grafana preset · Prometheus scraping participants every 10s.</div>
        </div>
        <div style={{display:'flex', gap:8}}>
          <span className="w-pill" style={{background:`${W.brand}1A`, color:W.brandText, border:`1px solid ${W.brand}33`}}>preset · cn-devkit/dapp</span>
          <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>3 participants</span>
        </div>
      </section>

      {/* KPI strip */}
      <div style={{display:'grid', gridTemplateColumns:'repeat(4,1fr)', gap:14, marginBottom:14}}>
        <MetricCard title="Throughput" value="14.2" unit="tx/s" delta="▲ 1.4 vs 5m">
          <AreaChart data={[3,5,4,6,8,7,9,12,10,11,13,14,12,14,15,14,14,13,14,14]} color={W.brand} height={60} />
        </MetricCard>
        <MetricCard title="Command completion p99" value="184" unit="ms" delta="▲ 22ms" deltaColor={W.warn}>
          <AreaChart data={[120,130,128,140,150,160,170,180,184,176,180,184,190,184,180,176,182,184,188,184]} color={W.warn} height={60} />
        </MetricCard>
        <MetricCard title="Active contracts" value="1,402" delta="▲ 86 / 5m">
          <AreaChart data={[800,820,860,900,940,980,1020,1100,1180,1240,1280,1310,1340,1360,1380,1392,1396,1399,1401,1402]} color={W.info} height={60} />
        </MetricCard>
        <MetricCard title="Errors" value="0.4" unit="/min" delta="▼ 0.2" deltaColor={W.ok}>
          <AreaChart data={[2,1,2,3,1,0,1,2,1,1,0,1,0,1,1,0,0,1,0,0]} color={W.err} height={60} />
        </MetricCard>
      </div>

      {/* Big chart row */}
      <div style={{display:'grid', gridTemplateColumns:'2fr 1fr', gap:14, marginBottom:14}}>
        <Card title="Latency by phase" subtitle="median · p99 across last hour" pad={16}>
          <MultiLine height={180} labels series={[
            { label:'submit → completion p50', color: W.brand, data:[58,60,62,64,68,70,68,72,74,72,70,68,72,74,76,72,70,72,74,72] },
            { label:'submit → completion p99', color: W.warn, data:[120,130,128,140,150,160,170,180,184,176,180,184,190,184,180,176,182,184,188,184] },
            { label:'transaction tree publish p99', color: W.info, data:[90,95,98,100,108,112,118,122,128,124,126,128,130,126,128,124,126,128,130,128] },
          ]} />
        </Card>
        <Card title="Per-template throughput" subtitle="exercises / 5m" pad={16}>
          <div style={{display:'flex', flexDirection:'column', gap:10}}>
            {[
              { label:'Token.Transfer',         pct: 62, count: 412, color: W.brand },
              { label:'TokenProposal.Accept',   pct: 24, count: 160, color: W.info },
              { label:'Token.Mint',             pct: 9,  count: 60,  color: W.amber },
              { label:'Token.Burn',             pct: 3,  count: 22,  color: W.rose },
              { label:'CNS.Renew',              pct: 2,  count: 14,  color: W.mag },
            ].map((r,i) => (
              <div key={i}>
                <div style={{display:'flex', alignItems:'baseline', justifyContent:'space-between', marginBottom:4}}>
                  <span style={{fontSize:12, color:W.text}}>{r.label}</span>
                  <span style={{fontSize:11.5, color:W.dim, fontFamily:wMono}}>{r.count} · {r.pct}%</span>
                </div>
                <div style={{height:6, background:W.surface2, borderRadius:3, overflow:'hidden'}}>
                  <div style={{width:`${r.pct}%`, height:'100%', background:r.color, borderRadius:3}} />
                </div>
              </div>
            ))}
          </div>
        </Card>
      </div>

      {/* Mid row */}
      <div style={{display:'grid', gridTemplateColumns:'1fr 1fr 1fr', gap:14, marginBottom:14}}>
        <Card title="Active contracts" subtitle="trend · 1h" pad={16}>
          <AreaChart data={[800,820,860,900,940,980,1020,1100,1180,1240,1280,1310,1340,1360,1380,1392,1396,1399,1401,1402]} color={W.info} />
          <div style={{display:'flex', gap:10, marginTop:8, fontSize:11.5, color:W.dim, fontFamily:wMono}}>
            <span>min 800</span><span style={{color:W.text2}}>avg 1,148</span><span>max 1,402</span>
          </div>
        </Card>
        <Card title="Ledger errors" subtitle="commands rejected / min" pad={16}>
          <BarChart data={[2,1,3,2,4,1,0,1,2,3,1,0,0,1,2,1,0,1,0,0]} color={W.err} />
          <div style={{display:'flex', gap:10, marginTop:8, fontSize:11.5, color:W.dim, fontFamily:wMono}}>
            <span>AUTH_NOT_FOUND × 4</span><span style={{color:W.text2}}>CONTRACT_NOT_FOUND × 2</span>
          </div>
        </Card>
        <Card title="Resource usage" subtitle="containers · % of limit" pad={16}>
          <MultiLine height={120} labels series={[
            { label:'cpu',    color: W.mag,   data:[8,10,12,14,12,16,18,15,20,18,17,15,18,20,22,20,19,18,22,22] },
            { label:'memory', color: W.brand, data:[12,12,14,15,15,16,16,18,18,18,19,19,20,21,22,22,22,22,22,22] },
          ]} />
        </Card>
      </div>

      {/* Bottom row */}
      <div style={{display:'grid', gridTemplateColumns:'2fr 1fr', gap:14, marginBottom:14}}>
        <Card title="Submit-to-commit heatmap" subtitle="latency density · 1m buckets · 1h" pad={16}>
          <div style={{display:'grid', gridTemplateColumns:'40px 1fr', gap:8, alignItems:'center'}}>
            <div style={{display:'flex', flexDirection:'column', justifyContent:'space-between', height:140, color:W.dim, fontSize:10.5, fontFamily:wMono}}>
              <span>500ms</span><span>250</span><span>100</span><span>50</span><span>0</span>
            </div>
            <Heatmap rows={5} cols={36} />
          </div>
          <div style={{display:'flex', gap:14, marginTop:8, alignItems:'center', fontSize:11.5, color:W.dim, fontFamily:wMono}}>
            <span>sparse</span>
            <div style={{height:6, flex:1, background:`linear-gradient(90deg, ${W.brand}11, ${W.brand}ee)`, borderRadius:3}} />
            <span>dense</span>
          </div>
        </Card>
        <Card title="Top error sources" subtitle="last hour" pad={0}>
          {[
            { svc:'app-provider-participant', code:'AUTH_NOT_FOUND',     count: 4, color: W.err },
            { svc:'app-user-participant',     code:'CONTRACT_NOT_FOUND', count: 2, color: W.warn },
            { svc:'sv-participant',           code:'TIMEOUT',            count: 1, color: W.amber },
            { svc:'global-synchronizer',      code:'TRAFFIC_THROTTLED',  count: 1, color: W.warn },
          ].map((r,i) => (
            <div key={i} className="w-row" style={{
              padding:'10px 16px', display:'flex', alignItems:'center', gap:10,
              borderBottom: i < 3 ? `1px solid ${W.border}` : 'none',
            }}>
              <span style={{width:6, height:6, borderRadius:'50%', background:r.color}} />
              <div style={{flex:1, minWidth:0}}>
                <div style={{fontSize:12.5, fontWeight:500}}>{r.code}</div>
                <Mono c={W.dim}>{r.svc}</Mono>
              </div>
              <span style={{fontFamily:wMono, color:W.text, fontSize:12}}>{r.count}</span>
            </div>
          ))}
        </Card>
      </div>
    </AppShell>
  );
}

// ── AI Agent Skill doc viewer ────────────────────────────────────────────
function AgentScreen() {
  return (
    <AppShell active="agent" instance="hubble"
      topRight={<>
        <button className="w-btn">Copy to clipboard</button>
        <button className="w-btn primary">Open in Claude ↗</button>
      </>}>
      <section style={{marginBottom:14, display:'flex', alignItems:'flex-end', justifyContent:'space-between'}}>
        <div>
          <h1 style={{margin:'0 0 3px', fontSize:20, fontWeight:600, letterSpacing:-0.4}}>Agent Skill</h1>
          <div style={{color:W.dim, fontSize:12.5}}>Editor-agnostic skill doc describing safe <Mono c={W.brand}>dpm localnet</Mono> workflows. Drop into <Mono c={W.text2}>.claude/skills/</Mono> or <Mono c={W.text2}>~/.codex/skills/</Mono>.</div>
        </div>
        <div style={{display:'flex', gap:8}}>
          <Pill color={W.brand}>v0.7.2</Pill>
          <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>cli-only surface</span>
        </div>
      </section>

      {/* What is this — explainer */}
      <Card pad={0} style={{marginBottom:14, background:`linear-gradient(135deg, ${W.surface} 0%, ${W.card} 100%)`}}>
        <div style={{padding:'18px 22px', display:'grid', gridTemplateColumns:'auto 1fr', gap:18, alignItems:'flex-start'}}>
          <div style={{
            width:54, height:54, borderRadius:14,
            background:`linear-gradient(135deg, ${W.brand}33, ${W.brand}11)`,
            display:'flex', alignItems:'center', justifyContent:'center',
            fontSize:26, color:W.brand, flex:'0 0 auto',
          }}>✦</div>
          <div>
            <div style={{display:'flex', alignItems:'center', gap:10, marginBottom:8, flexWrap:'wrap'}}>
              <h2 style={{margin:0, fontSize:16, fontWeight:600, letterSpacing:-0.2}}>What this does</h2>
              <Pill color={W.brand}>optional</Pill>
              <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>auxiliary docs · not core runtime</span>
            </div>
            <p style={{margin:'0 0 12px', color:W.text2, fontSize:13.5, lineHeight:1.6, maxWidth:880}}>
              Skill docs are small <Mono c={W.text}>.md</Mono> files that teach an AI coding agent how to use DevKit <em>safely</em>. You drop them into your editor's skills folder once; from then on, the agent knows the exact, documented <Mono c={W.brand}>dpm localnet</Mono> commands to spin up a ledger, upload a DAR, inspect contracts, or run a CIP-0112 token flow — without you copy-pasting the docs into every conversation.
            </p>
            <div style={{display:'grid', gridTemplateColumns:'repeat(3, 1fr)', gap:14, marginTop:10}}>
              <div>
                <div style={{display:'flex', alignItems:'center', gap:8, marginBottom:6}}>
                  <span style={{width:22, height:22, borderRadius:6, background:`${W.brand}1A`, color:W.brand, display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:11.5}}>1</span>
                  <span style={{fontWeight:600, fontSize:12.5}}>Install once</span>
                </div>
                <div style={{color:W.dim, fontSize:11.5, lineHeight:1.55}}>Click <Mono c={W.text2}>Open in Claude</Mono> or copy the doc into <Mono c={W.text2}>.claude/skills/</Mono>, <Mono c={W.text2}>~/.codex/</Mono>, or any agent that reads markdown skills.</div>
              </div>
              <div>
                <div style={{display:'flex', alignItems:'center', gap:8, marginBottom:6}}>
                  <span style={{width:22, height:22, borderRadius:6, background:`${W.brand}1A`, color:W.brand, display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:11.5}}>2</span>
                  <span style={{fontWeight:600, fontSize:12.5}}>Ask in plain English</span>
                </div>
                <div style={{color:W.dim, fontSize:11.5, lineHeight:1.55}}>"Boot a fresh ledger called <Mono c={W.text2}>weiss</Mono> and upload my retail-token build." The agent picks the matching skill from its index.</div>
              </div>
              <div>
                <div style={{display:'flex', alignItems:'center', gap:8, marginBottom:6}}>
                  <span style={{width:22, height:22, borderRadius:6, background:`${W.brand}1A`, color:W.brand, display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:11.5}}>3</span>
                  <span style={{fontWeight:600, fontSize:12.5}}>Agent runs DevKit, safely</span>
                </div>
                <div style={{color:W.dim, fontSize:11.5, lineHeight:1.55}}>It only uses documented <Mono c={W.text2}>dpm localnet</Mono> commands — never raw <Mono c={W.text2}>docker</Mono> — and pauses on first failure to run <Mono c={W.text2}>doctor</Mono>.</div>
              </div>
            </div>
          </div>
        </div>
      </Card>

      <div style={{display:'grid', gridTemplateColumns:'260px 1fr 320px', gap:14, alignItems:'start'}}>
        <Card title="Skills" subtitle="6 workflows" pad={6}>
          {[
            ['localnet-lifecycle.md', 'start, status, stop', true],
            ['dar-upload.md',         'upload &amp; vet a DAR'],
            ['hot-deploy.md',         'watch &amp; redeploy'],
            ['inspect-contracts.md',  'live ACS, tx replay'],
            ['token-flow.md',         'create, mint, transfer'],
            ['ci-localnet.md',        'CI workflow harness'],
          ].map(([name, desc, active], i) => (
            <div key={i} className="w-row" style={{
              padding:'8px 10px', borderRadius:6, cursor:'pointer',
              background: active ? W.selRow : 'transparent',
              borderLeft: active ? `2px solid ${W.brand}` : '2px solid transparent',
              marginBottom: 2,
            }}>
              <div style={{fontSize:12.5, fontWeight: active ? 600 : 500, fontFamily:wMono}}>{name}</div>
              <div style={{color:W.dim, fontSize:11, marginTop:2}} dangerouslySetInnerHTML={{__html: desc}} />
            </div>
          ))}
        </Card>

        {/* Doc preview */}
        <Card pad={0}>
          <header style={{padding:'12px 18px', borderBottom:`1px solid ${W.border}`, display:'flex', alignItems:'center', gap:10}}>
            <Mono c={W.text}>localnet-lifecycle.md</Mono>
            <span className="w-pill" style={{background:`${W.brand}1A`, color:W.brandText, border:`1px solid ${W.brand}33`, fontFamily:wMono}}>safe</span>
            <span style={{marginLeft:'auto', color:W.dim, fontSize:11.5}}>read-only · agent-consumable</span>
          </header>
          <div className="w-scroll" style={{padding:'20px 26px', maxHeight: 620, overflow:'auto'}}>
            <div style={{fontSize:11.5, color:W.dim, fontFamily:wMono, marginBottom:14, padding:'6px 10px', background:W.surface2, borderRadius:6, border:`1px solid ${W.border}`}}>
              ---<br/>
              name: localnet-lifecycle<br/>
              description: Start, inspect, and stop a Canton LocalNet. Use ONLY documented <span style={{color:W.brand}}>dpm localnet</span> commands. Never edit Docker resources directly.<br/>
              when_to_use: User asks to "boot the ledger", "spin up a participant", "tear down localnet".<br/>
              ---
            </div>

            <h2 style={{fontSize:18, fontWeight:600, margin:'0 0 6px', letterSpacing:-0.3}}>Start a named LocalNet</h2>
            <p style={{color:W.text2, fontSize:13, lineHeight:1.6, margin:'0 0 10px'}}>
              Default instance is <code style={{background:W.surface2, padding:'1px 6px', borderRadius:4, fontFamily:wMono, fontSize:12}}>hubble</code>. Always name instances when more than one may run.
            </p>
            <pre style={{margin:'0 0 16px', padding:'12px 14px', background:W.surface2, border:`1px solid ${W.border}`, borderRadius:8, fontFamily:wMono, fontSize:12.5, color:W.text, lineHeight:1.6, overflow:'auto'}}>
{`# 1. preflight + start`}<br/>
{`dpm localnet up --name `}<span style={{color:W.brand}}>{`<name>`}</span>{` --version `}<span style={{color:W.brand}}>{`<splice-version>`}</span><br/>
<br/>
{`# 2. wait for readiness (--json for machine-readable status)`}<br/>
{`dpm localnet status --name `}<span style={{color:W.brand}}>{`<name>`}</span>{` --json`}<br/>
<br/>
{`# 3. export endpoints for the calling app`}<br/>
{`dpm localnet env --name `}<span style={{color:W.brand}}>{`<name>`}</span>
            </pre>

            <h3 style={{fontSize:14, fontWeight:600, margin:'14px 0 6px'}}>Verify the participant is ready</h3>
            <p style={{color:W.text2, fontSize:13, lineHeight:1.6, margin:'0 0 8px'}}>
              The agent MUST confirm <code style={{background:W.surface2, padding:'1px 6px', borderRadius:4, fontFamily:wMono, fontSize:12}}>{`"state": "running"`}</code> for the three participants (sv, app-provider, app-user) before issuing ledger commands.
            </p>

            <h3 style={{fontSize:14, fontWeight:600, margin:'14px 0 6px'}}>Safety constraints</h3>
            <ul style={{color:W.text2, fontSize:13, lineHeight:1.7, margin:'0 0 12px', paddingLeft: 20}}>
              <li>Never call <code style={{background:W.surface2, padding:'1px 6px', borderRadius:4, fontFamily:wMono, fontSize:12}}>docker</code> directly — use the DPM wrappers so labels and resource ownership stay correct.</li>
              <li>Do not modify <code style={{background:W.surface2, padding:'1px 6px', borderRadius:4, fontFamily:wMono, fontSize:12}}>~/.canton-devkit/&lt;name&gt;/auth.json</code>.</li>
              <li>Run <code style={{background:W.surface2, padding:'1px 6px', borderRadius:4, fontFamily:wMono, fontSize:12}}>dpm localnet doctor</code> when a command fails before retrying.</li>
            </ul>

            <h3 style={{fontSize:14, fontWeight:600, margin:'14px 0 6px'}}>Stop &amp; clean</h3>
            <pre style={{margin:0, padding:'12px 14px', background:W.surface2, border:`1px solid ${W.border}`, borderRadius:8, fontFamily:wMono, fontSize:12.5, color:W.text, lineHeight:1.6}}>
{`dpm localnet down  --name `}<span style={{color:W.brand}}>{`<name>`}</span>{`   # keep volumes`}<br/>
{`dpm localnet clean --name `}<span style={{color:W.brand}}>{`<name>`}</span>{`   # remove volumes (asks confirmation)`}
            </pre>
          </div>
        </Card>

        {/* Right preview — agent in action */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card title="Used by" pad={0}>
            <div style={{padding:'10px 14px', display:'flex', alignItems:'center', gap:10, borderBottom:`1px solid ${W.border}`}}>
              <div style={{width:24, height:24, borderRadius:6, background:'#D97757', color:'#fff', display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:12}}>C</div>
              <div style={{flex:1, fontSize:12.5, fontWeight:500}}>Claude (Code, Desktop)</div>
              <Pill color={W.ok}>linked</Pill>
            </div>
            <div style={{padding:'10px 14px', display:'flex', alignItems:'center', gap:10, borderBottom:`1px solid ${W.border}`}}>
              <div style={{width:24, height:24, borderRadius:6, background:'#10A37F', color:'#fff', display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:12}}>X</div>
              <div style={{flex:1, fontSize:12.5, fontWeight:500}}>Codex CLI</div>
              <Pill color={W.dim}>copy</Pill>
            </div>
            <div style={{padding:'10px 14px', display:'flex', alignItems:'center', gap:10}}>
              <div style={{width:24, height:24, borderRadius:6, background:'#5BD7C5', color:'#082018', display:'flex', alignItems:'center', justifyContent:'center', fontWeight:700, fontSize:12}}>+</div>
              <div style={{flex:1, fontSize:12.5, color:W.dim}}>Add another agent…</div>
            </div>
          </Card>

          <Card title="Demo conversation" subtitle="grounded in skill doc">
            <div style={{display:'flex', flexDirection:'column', gap:10, fontSize:12.5, lineHeight:1.5}}>
              <div style={{padding:'8px 10px', background:W.surface2, borderRadius:8, color:W.text2}}>
                <span style={{color:W.dim, fontWeight:600, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase'}}>You</span><br/>
                spin up a clean ledger called <Mono c={W.brand}>weiss</Mono> on splice 0.4.12 then upload my retail-token build
              </div>
              <div style={{padding:'8px 10px', background:`${W.brand}10`, borderRadius:8, color:W.text, border:`1px solid ${W.brand}22`}}>
                <span style={{color:W.brand, fontWeight:600, fontSize:10.5, letterSpacing:1.3, textTransform:'uppercase'}}>Claude</span><br/>
                Running the documented <Mono c={W.brand}>localnet up</Mono> sequence, then waiting on status JSON before uploading the DAR.
                <div style={{marginTop:6, fontFamily:wMono, fontSize:11.5, color:W.text2}}>
                  <Dot color={W.ok} /> dpm localnet up --name weiss --version 0.4.12<br/>
                  <Dot color={W.ok} /> dpm localnet status --name weiss --json<br/>
                  <Dot color={W.warn} pulse /> dpm localnet dar upload retail-token-1.1.0.dar --all
                </div>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </AppShell>
  );
}

Object.assign(window, { MetricsScreen, AgentScreen });
