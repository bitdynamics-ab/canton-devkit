// Contract & transaction exploration screens, plus observability metrics

function ScreenContractsWatch() {
  const Ev = ({ t, kind, party, tpl, cid, payload, archived }) => {
    const kindColor = kind === 'CREATE' ? TERM.success : kind === 'ARCHIVE' ? TERM.error : TERM.info;
    return (
      <Line>
        <span style={{color:TERM.faint}}>{t}</span>
        <span style={{color:TERM.dim}}>  </span>
        <span style={{color:kindColor, fontWeight:600, display:'inline-block', width:62}}>{kind}</span>
        <span style={{color:TERM.dim, display:'inline-block', width:96}}>{party}</span>
        <span style={{color:TERM.text, display:'inline-block', width:160}}>{tpl}</span>
        <span style={{color:TERM.magenta, display:'inline-block', width:96}}>{cid}</span>
        <span style={{color:TERM.dim}}>{payload}</span>
      </Line>
    );
  };
  return (
    <Terminal title="canton-devkit · contracts watch (live)" height={500}
      footer={<><span><span style={{color:TERM.success}}>● live</span> · 1,402 events · 8 templates</span><span><span style={{color:TERM.text}}>f</span> filter   <span style={{color:TERM.text}}>p</span> projection   <span style={{color:TERM.text}}>q</span> quit</span></>}>
      <Prompt cmd={<>dpm localnet contracts watch <span style={{color:TERM.amber}}>--template</span> retail-token:Token <span style={{color:TERM.amber}}>--party</span> alice</>} />
      <Line dim>Projection: <span style={{color:TERM.text}}>participant-alice</span> as <span style={{color:TERM.text}}>alice@localnet</span>  ·  template <span style={{color:TERM.text}}>Retail.Token.Token</span></Line>
      <Line />
      <div style={{display:'grid', gridTemplateColumns:'80px 62px 96px 160px 96px 1fr', color:TERM.brand, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', borderBottom:`1px solid ${TERM.faint}`, paddingBottom:4, marginBottom:4, fontWeight:600}}>
        <span>time</span><span>kind</span><span>by</span><span>template</span><span>cid</span><span>payload</span>
      </div>
      <Ev t="15:42:02" kind="CREATE"  party="alice@ln" tpl="Retail.Token.Token" cid="0x77bd…" payload={<>{ '{' } owner=<span style={{color:TERM.text}}>alice</span>, amount=<span style={{color:TERM.text}}>500</span>, sym=<span style={{color:TERM.text}}>RTK</span> { '}' }</>} />
      <Ev t="15:42:03" kind="CREATE"  party="alice@ln" tpl="Retail.Token.Token" cid="0x77be…" payload={<>{ '{' } owner=<span style={{color:TERM.text}}>alice</span>, amount=<span style={{color:TERM.text}}>120</span>, sym=<span style={{color:TERM.text}}>USD</span> { '}' }</>} />
      <Ev t="15:42:07" kind="EXERCISE" party="alice@ln" tpl="Token.Transfer"    cid="0x77bd…" payload={<>arg <span style={{color:TERM.text}}>newOwner=bob</span>, memo=<span style={{color:TERM.text}}>"pay-001"</span></>} />
      <Ev t="15:42:07" kind="ARCHIVE" party="alice@ln" tpl="Retail.Token.Token" cid="0x77bd…" payload={<>{ '{' } owner=<span style={{color:TERM.dim, textDecoration:'line-through'}}>alice</span><span style={{color:TERM.dim}}> → archived</span> { '}' }</>} />
      <Ev t="15:42:07" kind="CREATE"  party="alice@ln" tpl="Retail.Token.Token" cid="0x77bf…" payload={<>{ '{' } owner=<span style={{color:TERM.text}}>bob</span>, amount=<span style={{color:TERM.text}}>500</span>, sym=<span style={{color:TERM.text}}>RTK</span> { '}' }</>} />
      <Ev t="15:42:11" kind="CREATE"  party="alice@ln" tpl="Token.TokenProposal" cid="0x77c0…" payload={<>{ '{' } recipient=<span style={{color:TERM.text}}>carol</span>, amount=<span style={{color:TERM.text}}>50</span> { '}' }</>} />
      <Ev t="15:42:12" kind="EXERCISE" party="alice@ln" tpl="TokenProposal.Accept" cid="0x77c0…" payload={<>arg <span style={{color:TERM.text}}>{ '{}' }</span></>} />
      <Ev t="15:42:12" kind="ARCHIVE" party="alice@ln" tpl="Token.TokenProposal" cid="0x77c0…" payload={<></>} />
      <Ev t="15:42:14" kind="CREATE"  party="alice@ln" tpl="Retail.Token.Token" cid="0x77c1…" payload={<>{ '{' } owner=<span style={{color:TERM.text}}>carol</span>, amount=<span style={{color:TERM.text}}>50</span> { '}' }</>} />
      <Line><span style={{color:TERM.brand}}>⠹ </span><span style={{color:TERM.dim}}>tailing live events…</span><span className="term-cursor" /></Line>
    </Terminal>
  );
}

function ScreenTxList() {
  return (
    <Terminal title="canton-devkit · tx ls" height={500}>
      <Prompt cmd={<>dpm localnet tx ls <span style={{color:TERM.amber}}>--party</span> alice <span style={{color:TERM.amber}}>--template</span> retail-token:Token <span style={{color:TERM.amber}}>--from</span> 0/1240</>} />
      <Line dim>Projection alice@localnet · 4 results · offsets 0/1240 → 0/1258</Line>
      <Line />
      <Table dense columns={[
        { label:'offset',     w:'.7fr' },
        { label:'tx id',      w:'1fr' },
        { label:'time',       w:'.8fr' },
        { label:'effect',     w:'.8fr' },
        { label:'template',   w:'1.4fr' },
        { label:'parties',    w:'1.1fr' },
        { label:'workflow',   w:'1fr' },
      ]} rows={[
        ['0/1240', C.mag('0x9a3c…01'), '15:42:02.110', C.ok('+1 create'),  'Retail.Token.Token',    'alice', C.dim('wf-init')],
        ['0/1244', C.mag('0xa102…77'), '15:42:07.412', C.warn('xchange'),  'Token.Transfer→Token',  C.brand('alice → bob'),  C.dim('pay-001')],
        ['0/1251', C.mag('0xa388…1b'), '15:42:11.840', C.ok('+1 create'),  'Token.TokenProposal',   C.brand('alice → carol'),C.dim('proposal-9')],
        ['0/1258', C.mag('0xa50d…4c'), '15:42:12.920', C.warn('xchange'),  'Proposal.Accept→Token', C.brand('alice → carol'),C.dim('proposal-9')],
      ]} />
      <Line />
      <Line dim>Inspect a tx: <span style={{color:TERM.text}}>dpm localnet tx replay 0xa102…77 --as alice</span></Line>
      <Line dim>Watch this query live: <span style={{color:TERM.text}}>--follow</span> · CSV: <span style={{color:TERM.text}}>--format csv &gt; tx.csv</span></Line>
    </Terminal>
  );
}

function ScreenTxReplay() {
  return (
    <Terminal title="canton-devkit · tx replay (per-party projection)" height={580}>
      <Prompt cmd={<>dpm localnet tx replay <span style={{color:TERM.magenta}}>0xa102b1ce4977</span> <span style={{color:TERM.amber}}>--as</span> alice</>} />
      <Line />
      <KV k="Transaction" v={C.mag('0xa102b1ce4977…')} />
      <KV k="Offset"      v="0/1244" />
      <KV k="Recorded"    v="15:42:07.412Z" />
      <KV k="Workflow ID" v="pay-001" />
      <KV k="Command ID"  v={<>{C.dim('cli-')}<span style={{color:TERM.text}}>e4f12</span></>} />

      <Section title="Projection · alice@localnet">
        <Line><span style={{color:TERM.dim}}>┌─ </span>{C.brand('Exercise')}<span style={{color:TERM.text}}> Token.Transfer</span><span style={{color:TERM.dim}}>  cid </span><span style={{color:TERM.magenta}}>0x77bd…</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>actors    </span><span style={{color:TERM.text}}>alice</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>argument  </span><span style={{color:TERM.text}}>{'{ newOwner = bob, memo = "pay-001" }'}</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>├─ </span>{C.err('Archive')}<span style={{color:TERM.text}}> Retail.Token.Token </span><span style={{color:TERM.magenta}}>0x77bd…</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>└─ </span>{C.ok('Create')}<span style={{color:TERM.text}}> Retail.Token.Token </span><span style={{color:TERM.magenta}}>0x77bf…</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>payload  </span><span style={{color:TERM.text}}>{'{ owner = bob, amount = 500, sym = "RTK" }'}</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>witnesses </span><span style={{color:TERM.text}}>alice, bob</span></Line>
      </Section>

      <Section title="What other parties see" right={<>switch with <span style={{color:TERM.text}}>--as bob</span></>}>
        <Line><span style={{color:TERM.dim}}>bob   </span>{C.ok('● visible')}<span style={{color:TERM.dim}}>   sees Create 0x77bf… only (no transferor identity)</span></Line>
        <Line><span style={{color:TERM.dim}}>carol </span>{C.dim('○ excluded')}<span style={{color:TERM.dim}}>  not party to this exercise</span></Line>
      </Section>
    </Terminal>
  );
}

function ScreenMetrics() {
  const Spark = ({ data, color = TERM.brand }) => {
    const max = Math.max(...data);
    return (
      <div style={{display:'flex', alignItems:'flex-end', gap:2, height:28}}>
        {data.map((v, i) => (
          <div key={i} style={{width:5, height: `${(v/max)*100}%`, background: color, opacity: 0.85, borderRadius: 1}} />
        ))}
      </div>
    );
  };
  return (
    <Terminal title="canton-devkit · localnet metrics" height={500}>
      <Prompt cmd={<>dpm localnet metrics <span style={{color:TERM.amber}}>--name</span> hubble</>} />
      <Line dim>Last 5 minutes · sampled at 5s · Grafana running at http://localhost:3000</Line>
      <Line />
      <div style={{display:'grid', gridTemplateColumns:'1fr 1fr', gap: 14, marginTop: 4}}>
        <div style={{padding:'10px 12px', background:'#11161D', borderRadius:6}}>
          <div style={{color:TERM.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase'}}>Throughput</div>
          <div style={{display:'flex', alignItems:'baseline', gap:8, margin:'4px 0 6px'}}>
            <span style={{fontSize:22, color:TERM.text, fontWeight:600}}>14.2</span>
            <span style={{color:TERM.dim}}>tx/s</span>
            <span style={{color:TERM.success, marginLeft:'auto'}}>▲ 1.4</span>
          </div>
          <Spark data={[3,5,4,6,8,7,9,12,10,11,13,14,12,14,15,14,14,13,14,14]} />
        </div>
        <div style={{padding:'10px 12px', background:'#11161D', borderRadius:6}}>
          <div style={{color:TERM.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase'}}>Latency p99</div>
          <div style={{display:'flex', alignItems:'baseline', gap:8, margin:'4px 0 6px'}}>
            <span style={{fontSize:22, color:TERM.text, fontWeight:600}}>184</span>
            <span style={{color:TERM.dim}}>ms</span>
            <span style={{color:TERM.warn, marginLeft:'auto'}}>▲ 22ms</span>
          </div>
          <Spark color={TERM.warn} data={[120,130,128,140,150,160,170,180,184,176,180,184,190,184,180,176,182,184,188,184]} />
        </div>
        <div style={{padding:'10px 12px', background:'#11161D', borderRadius:6}}>
          <div style={{color:TERM.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase'}}>Active contracts</div>
          <div style={{display:'flex', alignItems:'baseline', gap:8, margin:'4px 0 6px'}}>
            <span style={{fontSize:22, color:TERM.text, fontWeight:600}}>1,402</span>
            <span style={{color:TERM.success, marginLeft:'auto'}}>▲ 86</span>
          </div>
          <Spark color={TERM.info} data={[800,820,860,900,940,980,1020,1100,1180,1240,1280,1310,1340,1360,1380,1392,1396,1399,1401,1402]} />
        </div>
        <div style={{padding:'10px 12px', background:'#11161D', borderRadius:6}}>
          <div style={{color:TERM.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase'}}>CPU · memory</div>
          <div style={{display:'flex', alignItems:'baseline', gap:8, margin:'4px 0 6px'}}>
            <span style={{fontSize:22, color:TERM.text, fontWeight:600}}>22%</span>
            <span style={{color:TERM.dim}}>· 3.4 GiB / 24</span>
          </div>
          <Spark color={TERM.magenta} data={[10,12,14,18,16,20,18,22,24,20,22,24,22,20,22,24,22,22,22,22]} />
        </div>
      </div>
      <Section title="Top templates by exercise">
        <Line><span style={{color:TERM.text, display:'inline-block', width:240}}>Token.Transfer</span><span style={{color:TERM.dim}}>  ████████████████  </span><span style={{color:TERM.brand}}>62%</span><span style={{color:TERM.dim}}>  · 412 exercises</span></Line>
        <Line><span style={{color:TERM.text, display:'inline-block', width:240}}>TokenProposal.Accept</span><span style={{color:TERM.dim}}>  ██████  </span><span style={{color:TERM.brand}}>24%</span><span style={{color:TERM.dim}}>  · 160 exercises</span></Line>
        <Line><span style={{color:TERM.text, display:'inline-block', width:240}}>Token.Burn</span><span style={{color:TERM.dim}}>  ██  </span><span style={{color:TERM.brand}}>9%</span><span style={{color:TERM.dim}}>  · 60 exercises</span></Line>
      </Section>
    </Terminal>
  );
}

Object.assign(window, { ScreenContractsWatch, ScreenTxList, ScreenTxReplay, ScreenMetrics });
