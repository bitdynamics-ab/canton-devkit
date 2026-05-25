// Dashboard / Overview screen — the LocalNet at-a-glance hub.

function Sparkline({ data, color = W.brand, width = '100%', height = 32, fill = true }) {
  const max = Math.max(...data), min = Math.min(...data);
  const range = max - min || 1;
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * 100;
    const y = 100 - ((v - min) / range) * 90 - 5;
    return `${x},${y}`;
  });
  return (
    <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{width, height, display:'block'}}>
      {fill && (
        <polygon points={`0,100 ${pts.join(' ')} 100,100`} fill={color} fillOpacity="0.10" />
      )}
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
      <circle cx={pts[pts.length-1].split(',')[0]} cy={pts[pts.length-1].split(',')[1]} r="1.6" fill={color} />
    </svg>
  );
}

function Kpi({ label, value, unit, trend, trendColor = W.ok, spark, sparkColor = W.brand }) {
  return (
    <Card pad={14}>
      <div style={{display:'flex', flexDirection:'column', gap:6}}>
        <div style={{color:W.dim, fontSize:11, letterSpacing:1.2, textTransform:'uppercase', fontWeight:600}}>{label}</div>
        <div style={{display:'flex', alignItems:'baseline', gap:6}}>
          <span style={{fontSize:26, fontWeight:600, color:W.text, letterSpacing:-0.5, fontFamily:wSans}}>{value}</span>
          {unit && <span style={{color:W.dim, fontSize:12.5}}>{unit}</span>}
          {trend && <span style={{marginLeft:'auto', color:trendColor, fontSize:12, fontWeight:500}}>{trend}</span>}
        </div>
        {spark && <div style={{marginTop:2}}><Sparkline data={spark} color={sparkColor} height={28} /></div>}
      </div>
    </Card>
  );
}

function ServiceRow({ name, image, state, ports, cpu, mem, sparks }) {
  const stateColor = state === 'healthy' ? W.ok : state === 'syncing' ? W.warn : W.dim;
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'1.6fr 1fr 1.1fr 1.4fr .6fr', gap: 14,
      padding:'10px 16px', borderBottom:`1px solid ${W.border}`, alignItems:'center',
    }}>
      <div style={{display:'flex', alignItems:'center', gap:10, minWidth:0}}>
        <Dot color={stateColor} pulse={state==='syncing'} />
        <div style={{display:'flex', flexDirection:'column', minWidth:0}}>
          <span style={{fontWeight:600, fontSize:12.5}}>{name}</span>
          <Mono c={W.dim}>{image}</Mono>
        </div>
      </div>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <span style={{color:stateColor, fontWeight:500, fontSize:12, textTransform:'capitalize'}}>{state}</span>
        {state === 'syncing' && <span style={{color:W.dim, fontSize:11}}>62%</span>}
      </div>
      <Mono c={W.text2}>{ports}</Mono>
      <div style={{display:'flex', alignItems:'center', gap:10}}>
        <div style={{width:60}}>
          <Sparkline data={sparks} height={20} color={W.brand} fill={false} />
        </div>
        <Mono c={W.dim}>{cpu}% · {mem}</Mono>
      </div>
      <div style={{display:'flex', justifyContent:'flex-end', gap:4}}>
        <button className="w-iconbtn" title="restart">↻</button>
        <button className="w-iconbtn" title="logs">≣</button>
      </div>
    </div>
  );
}

function EndpointRow({ label, url, port, info }) {
  return (
    <div className="w-row" style={{display:'flex', alignItems:'center', gap:10, padding:'8px 10px', borderRadius:6}}>
      <div style={{width:6, height:6, borderRadius:'50%', background:W.brand}} />
      <div style={{flex:1, minWidth:0}}>
        <div style={{fontWeight:500, fontSize:12.5}}>{label}</div>
        <Mono c={W.dim}>{url}</Mono>
      </div>
      <span className="w-kbd">⌘C</span>
    </div>
  );
}

function PartyRow({ wallet, party, tokens, color }) {
  return (
    <div className="w-row" style={{display:'flex', alignItems:'center', gap:10, padding:'8px 10px', borderRadius:6}}>
      <div style={{
        width:28, height:28, borderRadius:'50%',
        background:`linear-gradient(135deg, ${color}, ${W.brand})`,
        color:'#082018', display:'flex', alignItems:'center', justifyContent:'center',
        fontSize:11, fontWeight:700,
      }}>{wallet[0].toUpperCase()}</div>
      <div style={{flex:1, minWidth:0}}>
        <div style={{fontWeight:600, fontSize:12.5}}>{wallet}<span style={{color:W.dim, fontWeight:400}}>@localnet</span></div>
        <Mono c={W.dim}>{party}</Mono>
      </div>
      <div style={{display:'flex', flexDirection:'column', alignItems:'flex-end', fontSize:11}}>
        <span style={{color:W.text, fontWeight:600, fontFamily:wMono}}>{tokens}</span>
        <span style={{color:W.dim}}>holdings</span>
      </div>
    </div>
  );
}

function ActivityRow({ time, kind, party, body, cid }) {
  const kindColors = { CREATE: W.ok, ARCHIVE: W.err, EXERCISE: W.info, UPLOAD: W.mag, MINT: W.amber };
  const c = kindColors[kind] || W.dim;
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'72px 84px 110px 1fr 110px',
      gap:14, padding:'9px 16px', alignItems:'center',
      borderBottom:`1px solid ${W.border}`,
    }}>
      <Mono c={W.dim}>{time}</Mono>
      <span style={{color:c, fontWeight:600, fontSize:11.5, letterSpacing:0.4}}>{kind}</span>
      <Mono c={W.text2}>{party}</Mono>
      <span style={{color:W.text, fontSize:12.5, overflow:'hidden', textOverflow:'ellipsis', whiteSpace:'nowrap'}}>{body}</span>
      <Mono c={W.mag}>{cid}</Mono>
    </div>
  );
}

function DashboardScreen() {
  const txData = [3,4,6,5,8,7,10,9,12,11,14,12,16,15,14,16,18,15,14,16];
  const latData = [120,130,128,140,150,160,170,180,184,176,180,184,190,184,180,176,182,184,188,184];
  const acsData = [800,820,860,900,940,980,1020,1100,1180,1240,1280,1310,1340,1360,1380,1392,1396,1399,1401,1402];
  const cpuData = [8,10,12,14,12,16,18,15,20,18,17,15,18,20,22,20,19,18,22,22];
  return (
    <AppShell active="overview" instance="hubble">
      {/* Hero strip */}
      <section style={{
        background: `linear-gradient(135deg, ${W.surface} 0%, ${W.card} 100%)`,
        border:`1px solid ${W.border}`,
        borderRadius:12,
        padding:'18px 22px',
        display:'grid', gridTemplateColumns:'1fr auto', alignItems:'center', gap:16,
        marginBottom:16, position:'relative', overflow:'hidden',
      }}>
        <div style={{
          position:'absolute', top:-40, right:-40, width:200, height:200,
          background:`radial-gradient(circle, ${W.brand}22 0%, transparent 70%)`,
          pointerEvents:'none',
        }} />
        <div style={{display:'flex', flexDirection:'column', gap:6}}>
          <div style={{display:'flex', alignItems:'center', gap:10, flexWrap:'wrap'}}>
            <h1 style={{margin:0, fontSize:22, fontWeight:600, letterSpacing:-0.5}}>hubble</h1>
            <Pill color={W.ok}><Dot color={W.ok} pulse /> running</Pill>
            <Pill color={W.brand}>splice 0.4.12</Pill>
            <Pill color={W.info}>sv · app-provider · app-user</Pill>
            <Pill color={W.mag}>multi-sync</Pill>
            <span style={{color:W.dim, fontSize:12.5}}>· 11 services · started 1h 22m ago</span>
          </div>
          <div style={{color:W.dim, fontSize:12.5, fontFamily:wMono}}>
            wallet.localhost · scan.localhost · canton.localhost · grpc 4901, 3901, 2901 · DSO party::1220dso0…
          </div>
        </div>
        <div style={{display:'flex', gap:8}}>
          <button className="w-btn"><span style={{color:W.brand}}>⏻</span> Restart</button>
          <button className="w-btn"><span style={{color:W.amber}}>◐</span> Snapshot</button>
          <button className="w-btn primary">Open in CLI</button>
        </div>
      </section>

      {/* KPI strip */}
      <div style={{display:'grid', gridTemplateColumns:'repeat(4, 1fr)', gap:14, marginBottom:16}}>
        <Kpi label="Throughput"      value="14.2" unit="tx/s"   trend="▲ 1.4 vs 5m" spark={txData} sparkColor={W.brand} />
        <Kpi label="Latency p99"     value="184"  unit="ms"     trend="▲ 22 ms" trendColor={W.warn} spark={latData} sparkColor={W.warn} />
        <Kpi label="Active contracts" value="1,402" unit=""    trend="▲ 86 / 5m" spark={acsData} sparkColor={W.info} />
        <Kpi label="CPU · Memory"    value="22%"  unit="· 3.4 GiB" trend="of 24 GiB" trendColor={W.dim} spark={cpuData} sparkColor={W.mag} />
      </div>

      {/* Body grid */}
      <div style={{display:'grid', gridTemplateColumns:'1.8fr 1fr', gap:14, marginBottom:14}}>
        {/* Services */}
        <Card title="Services" subtitle="11 running · 1 syncing · profiles sv, app-provider, app-user, multi-sync, observability" pad={0}
          action={<>
            <span className="w-pill" style={{background:W.surface2, color:W.dim, border:`1px solid ${W.border}`}}>auto-refresh 2s</span>
            <button className="w-iconbtn" title="filter">⌕</button>
          </>}>
          <div style={{
            display:'grid', gridTemplateColumns:'1.6fr 1fr 1.1fr 1.4fr .6fr', gap:14,
            padding:'9px 16px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
            borderBottom:`1px solid ${W.border}`,
          }}>
            <span>Service · Profile</span><span>State</span><span>Ports</span><span>Load · 1m</span><span style={{textAlign:'right'}}>Actions</span>
          </div>
          <ServiceRow name="global-synchronizer"  image="splice/canton:0.4.12 · sv"            state="healthy" ports="4901, 4902"  cpu="8" mem="1.3GiB"  sparks={[2,3,2,4,3,5,4,6,5,5,4,5,6,5,4]} />
          <ServiceRow name="app-synchronizer"     image="splice/canton:0.4.12 · multi-sync"    state="healthy" ports="4801, 4802"  cpu="4" mem="780MiB"  sparks={[1,2,1,2,3,2,3,3,3,3,4,3,4,3,3]} />
          <ServiceRow name="sv-participant"       image="splice/participant:0.4.12 · sv"       state="healthy" ports="4901, 4902"  cpu="5" mem="820MiB"  sparks={[2,2,3,2,4,3,4,4,5,4,4,3,4,3,4]} />
          <ServiceRow name="app-provider-participant" image="splice/participant:0.4.12 · app-provider" state="healthy" ports="3901, 3902"  cpu="4" mem="740MiB"  sparks={[1,2,1,2,3,2,4,3,4,3,3,4,4,4,4]} />
          <ServiceRow name="app-user-participant" image="splice/participant:0.4.12 · app-user" state="healthy" ports="2901, 2902"  cpu="3" mem="720MiB"  sparks={[1,1,2,1,2,2,3,3,4,3,2,3,3,3,4]} />
          <ServiceRow name="sv-validator · wallet · scan" image="splice/validator:0.4.12 · sv" state="healthy" ports="sv.localhost"  cpu="2" mem="380MiB"  sparks={[1,1,1,2,1,2,2,1,1,2,1,1,2,1,1]} />
          <ServiceRow name="app-provider-validator" image="splice/validator:0.4.12 · app-provider" state="healthy" ports="wallet.localhost"  cpu="2" mem="360MiB"  sparks={[1,2,2,1,2,2,3,2,2,1,2,2,2,1,2]} />
          <ServiceRow name="app-user-validator"   image="splice/validator:0.4.12 · app-user"   state="healthy" ports="wallet.app-user.localhost"  cpu="1" mem="320MiB"  sparks={[1,1,1,2,2,1,2,2,1,1,2,1,1,1,1]} />
          <ServiceRow name="scan · CNS"           image="splice/scan:0.4.12 · sv"              state="syncing" ports="scan.localhost"  cpu="6" mem="220MiB"  sparks={[3,4,5,4,5,6,5,6,7,6,5,6,7,6,6]} />
          <ServiceRow name="postgres"             image="postgres:14 · shared"                 state="healthy" ports="5432"         cpu="3" mem="540MiB"  sparks={[2,2,3,2,2,3,3,2,2,3,2,2,2,3,2]} />
          <ServiceRow name="grafana · prometheus" image="grafana/grafana:10.4 · observability" state="healthy" ports="3000, 9090"   cpu="2" mem="160MiB"  sparks={[1,1,2,1,1,2,2,1,1,2,1,1,2,1,1]} />
        </Card>

        {/* Endpoints + Parties */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card title="Endpoints" subtitle="hover to copy · ⌘C" pad={8}>
            <EndpointRow label="Wallet · app-provider"    url="http://wallet.localhost" />
            <EndpointRow label="Wallet · app-user"        url="http://wallet.app-user.localhost" />
            <EndpointRow label="Scan UI · CNS"            url="http://scan.localhost" />
            <EndpointRow label="Ledger API · app-provider" url="grpc://localhost:3901" />
            <EndpointRow label="Ledger API · app-user"    url="grpc://localhost:2901" />
            <EndpointRow label="Ledger API · sv"          url="grpc://localhost:4901" />
            <EndpointRow label="Swagger · JSON API"       url="http://localhost:9090" />
            <EndpointRow label="Grafana · Prometheus"     url="http://localhost:3000" />
          </Card>
          <Card title="Parties" subtitle="DSO + 3 wallets · CIP-0112 ready" pad={8}>
            <PartyRow wallet="DSO"          party="party::1220dso0…f8aa" tokens="DSO Party" color="#F5BF55" />
            <PartyRow wallet="app-provider" party="party::1220a8d2…f7c1" tokens="999,450 RTK" color="#5BD7C5" />
            <PartyRow wallet="app-user"     party="party::1220c4f1…aa90" tokens="450 RTK"     color="#7CB5F7" />
            <PartyRow wallet="sv"           party="party::1220a01b…3309" tokens="bootstrap"   color="#C4A8F5" />
          </Card>
        </div>
      </div>

      {/* Activity */}
      <Card title="Recent activity" subtitle="live · projected through app-provider participant" pad={0}
        action={<>
          <span className="w-pill" style={{background:`${W.ok}22`, color:W.ok, border:`1px solid ${W.ok}44`}}><Dot color={W.ok} pulse /> live</span>
          <button className="w-btn">Open Explorer →</button>
        </>}>
        <div style={{
          display:'grid', gridTemplateColumns:'72px 84px 110px 1fr 110px',
          gap:14, padding:'8px 16px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
          borderBottom:`1px solid ${W.border}`,
        }}>
          <span>Time</span><span>Kind</span><span>Party</span><span>Event</span><span>Cid</span>
        </div>
        <div className="w-flash"><ActivityRow time="15:42:14" kind="CREATE"   party="app-provider" body="Retail.Token.Token  { owner=app-user, amount=50, sym='RTK' }"   cid="0x77c1…" /></div>
        <ActivityRow time="15:42:12" kind="ARCHIVE"  party="app-provider" body="Token.TokenProposal  archived after Accept"                  cid="0x77c0…" />
        <ActivityRow time="15:42:12" kind="EXERCISE" party="app-user"     body="TokenProposal.Accept  {}"                                    cid="0x77c0…" />
        <ActivityRow time="15:42:11" kind="CREATE"   party="app-provider" body="Token.TokenProposal  { recipient=app-user, amount=50 }"      cid="0x77c0…" />
        <ActivityRow time="15:42:07" kind="ARCHIVE"  party="app-provider" body="Retail.Token.Token  owner=app-provider → transferred"        cid="0x77bd…" />
        <ActivityRow time="15:42:07" kind="EXERCISE" party="app-provider" body="Token.Transfer  { newOwner=app-user, memo='pay-001' }"       cid="0x77bd…" />
        <ActivityRow time="15:42:04" kind="UPLOAD"   party="app-provider" body="retail-token-1.1.0.dar  vetted on 3 participants (sv, app-provider, app-user)" cid="pkg 0x9f2c…" />
        <ActivityRow time="15:42:02" kind="MINT"     party="app-provider" body="token create RTK · supply 1,000,000 · CIP-0112"              cid="cmd e4f12" />
      </Card>
    </AppShell>
  );
}

Object.assign(window, { DashboardScreen, Sparkline, Kpi });
