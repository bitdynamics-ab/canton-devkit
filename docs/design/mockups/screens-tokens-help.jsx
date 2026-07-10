// Token wizard, help, errors, env, list, snapshot, ci

function ScreenTokenCreate() {
  const Q = ({ q, a, cur }) => (
    <Line>
      <span style={{color:TERM.brand, fontWeight:600}}>?</span>
      <span style={{color:TERM.text}}> {q}</span>
      {a && <><span style={{color:TERM.dim}}> › </span><span style={{color:TERM.text, fontWeight:600}}>{a}</span></>}
      {cur && <span className="term-cursor" />}
    </Line>
  );
  return (
    <Terminal title="canton-devkit · token create (CIP-0112 wizard)" height={620}
      footer={<><span>step <span style={{color:TERM.text}}>5/6</span> · standard CIP-0112</span><span><span style={{color:TERM.text}}>↑↓</span> select   <span style={{color:TERM.text}}>↵</span> next   <span style={{color:TERM.text}}>esc</span> back</span></>}>
      <Prompt cmd={<>dpm localnet token create</>} />
      <Line />
      <Box accent={TERM.brand} bg="#0F1A1A" pad="9px 14px" style={{borderRadius:6, marginBottom:10}}>
        <Line><span style={{color:TERM.brand, fontWeight:600}}>✦ Canton Token Wizard</span><span style={{color:TERM.dim}}> · CIP-0112 (Token Standard V2)</span></Line>
        <Line dim style={{marginTop:2}}>Defines a new instrument, deploys to the active LocalNet, and mints to test wallets.</Line>
      </Box>

      <Q q="Token name"           a="Retail Token" />
      <Q q="Symbol (3–8 chars)"   a="RTK" />
      <Q q="Decimals"             a="2" />
      <Q q="Issuer"               a="alice@localnet" />
      <Line>
        <span style={{color:TERM.brand, fontWeight:600}}>?</span>
        <span style={{color:TERM.text}}> Initial supply</span>
        <span style={{color:TERM.dim}}> › </span>
        <span style={{color:TERM.text}}>1_000_000</span>
        <span className="term-cursor" />
      </Line>
      <Line dim indent={1}>format: integers or e-notation · split across wallets next step</Line>

      <Line />
      <Line dim>──── selections so far ────────────────────────────</Line>
      <KV k="standard"   v={<>CIP-0112 <span style={{color:TERM.brand}}>(V2)</span></>} />
      <KV k="name"       v="Retail Token" />
      <KV k="symbol"     v={C.bold('RTK')} />
      <KV k="decimals"   v="2" />
      <KV k="issuer"     v={C.mag('party::1220a8…')} />
      <Line />
      <Line dim>Next: choose recipients and amounts · type <span style={{color:TERM.text}}>?</span> for help · <span style={{color:TERM.text}}>--dry-run</span> to skip deployment.</Line>
    </Terminal>
  );
}

function ScreenTokenActions() {
  return (
    <Terminal title="canton-devkit · token mint / transfer / balance" height={500}>
      <Prompt cmd={<>dpm localnet token mint <span style={{color:TERM.text}}>RTK</span> <span style={{color:TERM.text}}>500</span> <span style={{color:TERM.amber}}>--to</span> bob</>} />
      <Step icon="check" label="Resolved RTK"           detail={<><span style={{color:TERM.dim}}>retail-token@1.1.0 · issuer </span><span style={{color:TERM.text}}>alice</span></>} />
      <Step icon="check" label="Submitted mint command" detail={<>cmd-id <span style={{color:TERM.magenta}}>cli-e4f12</span></>} time="0.2s" />
      <Step icon="check" label="Created Token contract" detail={<>cid <span style={{color:TERM.magenta}}>0x77c1…</span><span style={{color:TERM.dim}}> · owner </span>bob<span style={{color:TERM.dim}}> · amount </span>500</>} time="0.4s" />

      <Line />
      <Prompt cmd={<>dpm localnet token transfer <span style={{color:TERM.text}}>RTK</span> <span style={{color:TERM.text}}>50</span> <span style={{color:TERM.amber}}>--from</span> bob <span style={{color:TERM.amber}}>--to</span> carol</>} />
      <Step icon="check" label="Found 1 Token for bob" detail="amount 500 · sufficient" />
      <Step icon="check" label="Exercised Transfer"     detail={<>memo <span style={{color:TERM.dim}}>"cli"</span></>} time="0.5s" />

      <Line />
      <Prompt cmd={<>dpm localnet token balance <span style={{color:TERM.text}}>RTK</span></>} />
      <Table dense columns={[
        { label:'wallet',    w:'1fr' },
        { label:'party',     w:'1.5fr' },
        { label:'balance',   w:'.7fr', align:'right' },
        { label:'usd est.',  w:'.7fr', align:'right' },
      ]} rows={[
        ['alice', C.mag('party::1220a8…'), C.bold('999,450'), C.dim('test-only')],
        ['bob',   C.mag('party::1220c4…'), C.bold('450'),     C.dim('test-only')],
        ['carol', C.mag('party::1220d9…'), C.bold('50'),      C.dim('test-only')],
        ['—',     C.dim('· totals ·'),     C.brand('1,000,000'), C.dim('—')],
      ]} />
    </Terminal>
  );
}

function ScreenHelp() {
  const Cmd = ({ name, desc }) => (
    <div style={{display:'grid', gridTemplateColumns:'180px 1fr', gap:14, padding:'2px 0'}}>
      <span style={{color:TERM.brand}}>{name}</span>
      <span style={{color:TERM.text}}>{desc}</span>
    </div>
  );
  return (
    <Terminal title="canton-devkit · help" height={620}>
      <Prompt cmd={<>dpm localnet --help</>} />
      <Line />
      <pre style={{margin:0, color:TERM.brand, fontFamily:fontMono, fontSize:11.5, lineHeight:1.15}}>{`
   ┌──────────────────────────────────────────┐
   │   canton-devkit · localnet                │
   │   spin up and manage a full Canton        │
   │   LocalNet with one command               │
   └──────────────────────────────────────────┘`}</pre>
      <Line />
      <Line><span style={{color:TERM.dim}}>Usage  </span><span style={{color:TERM.text}}>dpm localnet </span><span style={{color:TERM.brand}}>&lt;command&gt;</span><span style={{color:TERM.dim}}> [flags]</span></Line>
      <Line />
      <Section title="lifecycle">
        <Cmd name="up"         desc="start a named LocalNet with preflight checks and readiness wait" />
        <Cmd name="down"       desc="stop services without removing state" />
        <Cmd name="restart"    desc="restart a service or the full instance" />
        <Cmd name="clean"      desc="remove containers, networks, volumes for a named instance" />
        <Cmd name="status"     desc="services, endpoints, identities, version" />
        <Cmd name="logs"       desc="tail or follow service logs with filters" />
        <Cmd name="snapshot"   desc="capture or restore LocalNet state for demos and CI" />
        <Cmd name="env"        desc="emit .env values for ledger/json/admin/wallet/scan" />
        <Cmd name="list"       desc="discover devkit-managed LocalNets on this host" />
        <Cmd name="doctor"     desc="check docker, ports, resources, host prereqs" />
      </Section>
      <Section title="developing">
        <Cmd name="dar"        desc="upload · list · info · diff · download · remove · build-upload · watch" />
        <Cmd name="contracts"  desc="watch live ACS · stream creates and archives" />
        <Cmd name="tx"         desc="ls (multi-filter) · replay (per-party projection)" />
        <Cmd name="metrics"    desc="print throughput, latency p50/p99, contracts" />
        <Cmd name="token"      desc="create wizard · mint · transfer · burn · balance (CIP-0112)" />
      </Section>
      <Line />
      <Line dim>Flags work everywhere: <span style={{color:TERM.text}}>--name</span> <span style={{color:TERM.dim}}>(instance)</span>, <span style={{color:TERM.text}}>--json</span> <span style={{color:TERM.dim}}>(machine output)</span>, <span style={{color:TERM.text}}>--quiet</span>, <span style={{color:TERM.text}}>--no-color</span>. Run <span style={{color:TERM.text}}>dpm localnet &lt;cmd&gt; --help</span> for command-specific help.</Line>
    </Terminal>
  );
}

function ScreenError() {
  return (
    <Terminal title="canton-devkit · friendly error" height={400}>
      <Prompt cmd={<>dpm localnet up <span style={{color:TERM.amber}}>--name</span> hubble</>} />
      <Step icon="check" label="Docker daemon"      detail="v25.0.5" />
      <Step icon="check" label="Compose v2"         detail="v2.30.1" />
      <Step icon="cross" label="Ports 4485, 4487 in use" detail={<><span style={{color:TERM.error}}>pid 88341 (orbstack-vmgr), pid 88450 (grafana)</span></>} />
      <Line />
      <Box accent={TERM.error} bg="#1A1014" pad="10px 14px" style={{borderRadius:6}}>
        <Line><span style={{color:TERM.error, fontWeight:700}}>error</span><span style={{color:TERM.dim}}> · code </span><span style={{color:TERM.text}}>PORTS_IN_USE</span><span style={{color:TERM.dim}}> · </span><span style={{color:TERM.underline ? '': ''}}>devkit.dev/e/PORTS_IN_USE</span></Line>
        <Line style={{marginTop:6}}><span style={{color:TERM.text}}>Two ports DevKit needs are already bound by other processes.</span></Line>
        <Line dim style={{marginTop:6}}>Try one of:</Line>
        <Line indent={1}><span style={{color:TERM.brand}}>1. </span><span style={{color:TERM.text}}>Stop the conflicting processes</span><span style={{color:TERM.dim}}>  ·  </span><span style={{color:TERM.text}}>kill 88341 88450</span></Line>
        <Line indent={1}><span style={{color:TERM.brand}}>2. </span><span style={{color:TERM.text}}>Pick different ports</span><span style={{color:TERM.dim}}>           ·  </span><span style={{color:TERM.text}}>dpm localnet up --ports wallet=4585,scan=4587</span></Line>
        <Line indent={1}><span style={{color:TERM.brand}}>3. </span><span style={{color:TERM.text}}>Inspect host readiness</span><span style={{color:TERM.dim}}>           ·  </span><span style={{color:TERM.text}}>dpm localnet doctor</span></Line>
      </Box>
    </Terminal>
  );
}

function ScreenEnv() {
  return (
    <Terminal title="canton-devkit · localnet env" height={380}>
      <Prompt cmd={<>eval "$(dpm localnet env <span style={{color:TERM.amber}}>--name</span> hubble)"</>} />
      <Prompt cmd={<>dpm localnet env <span style={{color:TERM.amber}}>--name</span> hubble</>} />
      <Line><span style={{color:TERM.dim}}># Source these in your app or CI step</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_LEDGER_HOST</span>=<span style={{color:TERM.text}}>localhost</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_ALICE_LEDGER_PORT</span>=<span style={{color:TERM.text}}>4441</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_BOB_LEDGER_PORT</span>=<span style={{color:TERM.text}}>4451</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_JSON_API_URL</span>=<span style={{color:TERM.text}}>http://localhost:4480</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_WALLET_URL</span>=<span style={{color:TERM.text}}>http://localhost:4485</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_SCAN_URL</span>=<span style={{color:TERM.text}}>http://localhost:4487</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_ALICE_PARTY</span>=<span style={{color:TERM.magenta}}>party::1220a8d2…</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_BOB_PARTY</span>=<span style={{color:TERM.magenta}}>party::1220c4f1…</span></Line>
      <Line><span style={{color:TERM.info}}>CANTON_AUTH_FILE</span>=<span style={{color:TERM.text}}>~/.canton-devkit/hubble/auth.json</span></Line>
    </Terminal>
  );
}

function ScreenList() {
  return (
    <Terminal title="canton-devkit · localnet list" height={300}>
      <Prompt cmd={<>dpm localnet list</>} />
      <Line />
      <Table dense columns={[
        { label:'name',     w:'1fr' },
        { label:'state',    w:'.8fr' },
        { label:'splice',   w:'.7fr' },
        { label:'ports',    w:'1.2fr' },
        { label:'started',  w:'.8fr' },
        { label:'volumes',  w:'.7fr', align:'right' },
      ]} rows={[
        [C.bold('hubble'),  C.ok('● running'),  '0.4.12', '4441–4487', C.dim('1h 22m ago'), '3.4 GiB'],
        [C.bold('weiss'),   C.warn('◐ paused'), '0.4.10', '5441–5487', C.dim('2d ago'),     '1.1 GiB'],
        [C.bold('shadow'),  C.dim('○ stopped'), '0.4.12', '6441–6487', C.dim('5d ago'),     '0.4 GiB'],
      ]} />
      <Line />
      <Line dim>3 DevKit-managed LocalNets · run <span style={{color:TERM.text}}>localnet up --name &lt;name&gt;</span> to resume.</Line>
    </Terminal>
  );
}

Object.assign(window, { ScreenTokenCreate, ScreenTokenActions, ScreenHelp, ScreenError, ScreenEnv, ScreenList });
