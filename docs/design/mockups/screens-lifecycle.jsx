// Lifecycle screens — up / status / doctor / logs / down

function ScreenUp() {
  return (
    <Terminal title="canton-devkit · localnet up" subtitle="zsh" height={620}
      footer={<><span>elapsed <span style={{color:TERM.text}}>43s</span></span><span><span style={{color:TERM.brand}}>READY</span> · press <span style={{color:TERM.text}}>q</span> to detach</span></>}>
      <Prompt cmd={<><span>dpm localnet up </span><span style={{color:TERM.amber}}>--name</span> hubble <span style={{color:TERM.amber}}>--version</span> 0.4.12</>} />

      <Line dim>┌─ preflight ────────────────────────────────────────────────</Line>
      <Step icon="check" label="Docker daemon"      detail="v25.0.5"               time="0.2s" />
      <Step icon="check" label="Compose v2"         detail="v2.30.1"               time="0.1s" />
      <Step icon="check" label="Ports 4441–4490"    detail="available"             time="0.3s" />
      <Step icon="check" label="Disk · Memory"      detail="38GiB free · 24GiB"    time="0.1s" />
      <Step icon="warn"  label="Splice image"       detail="pulled (412 MiB)"      time="11.4s" />

      <Line />
      <Line dim>┌─ services ─────────────────────────────────────────────────</Line>
      <Step icon="check" label="canton-domain"      detail="2 sequencers · ledger api ready" time="6.1s" />
      <Step icon="check" label="participant-alice"  detail="ledger 4441 · admin 4442"        time="4.8s" />
      <Step icon="check" label="participant-bob"    detail="ledger 4451 · admin 4452"        time="4.9s" />
      <Step icon="check" label="json-api"           detail="4480"                            time="2.0s" />
      <Step icon="check" label="wallet-ui"          detail="4485"                            time="3.0s" />
      <Step icon="busy"  label="scan-ui"            detail={<><span style={{color:TERM.dim}}>indexing… </span><span style={{color:TERM.warn}}>62%</span></>} />

      <Line />
      <Box bg="#11181A" accent={TERM.brand} pad="10px 14px" style={{ borderRadius: 6, marginTop: 8 }}>
        <Line><span style={{color:TERM.brand, fontWeight:600}}>✦ LocalNet </span><span style={{color:TERM.text, fontWeight:600}}>"hubble"</span><span style={{color:TERM.dim}}> is ready.</span></Line>
        <Line dim style={{ marginTop: 4 }}>Run <span style={{color:TERM.text}}>dpm localnet env --name hubble</span> to export config, or <span style={{color:TERM.text}}>--ui</span> to open the dashboard.</Line>
      </Box>
    </Terminal>
  );
}

function ScreenStatus() {
  return (
    <Terminal title="canton-devkit · localnet status" height={620}>
      <Prompt cmd={<><span>dpm localnet status </span><span style={{color:TERM.amber}}>--name</span> hubble</>} />
      <Line />
      <div style={{display:'flex', gap:18}}>
        <div>
          <span style={{color:TERM.dim}}>Name      </span><span style={{color:TERM.text, fontWeight:600}}>hubble</span>
        </div>
        <div><span style={{color:TERM.dim}}>Splice </span><span style={{color:TERM.brand}}>0.4.12</span></div>
        <div><span style={{color:TERM.dim}}>Uptime </span><span style={{color:TERM.text}}>1h 22m</span></div>
        <div><span style={{color:TERM.dim}}>State </span>{C.ok('● running')}</div>
      </div>

      <Section title="Services">
        <Table dense columns={[
          { label:'service',     w:'1.4fr' },
          { label:'state',       w:'.7fr' },
          { label:'image',       w:'1.6fr' },
          { label:'ports',       w:'1.1fr' },
          { label:'cpu / mem',   w:'1fr', align:'right' },
        ]} rows={[
          ['canton-domain',     C.ok('● healthy'), 'splice/canton:0.4.12',     '4400, 4401',  '8%  / 1.2GiB'],
          ['participant-alice', C.ok('● healthy'), 'splice/participant:0.4.12','4441, 4442',  '4%  / 740MiB'],
          ['participant-bob',   C.ok('● healthy'), 'splice/participant:0.4.12','4451, 4452',  '3%  / 720MiB'],
          ['json-api',          C.ok('● healthy'), 'splice/jsonapi:0.4.12',    '4480',        '1%  / 180MiB'],
          ['wallet-ui',         C.ok('● healthy'), 'splice/wallet-ui:0.4.12',  '4485',        '0%  / 64MiB'],
          ['scan-ui',           C.warn('◐ syncing'), 'splice/scan-ui:0.4.12',  '4487',        '6%  / 220MiB'],
          ['observability',     C.dim('○ disabled'),'—',                       '—',           '—'],
        ]} />
      </Section>

      <Section title="Endpoints">
        <KV k="Ledger API"  v={<>localhost:<span style={{color:TERM.brand}}>4441</span>  ·  localhost:<span style={{color:TERM.brand}}>4451</span></>} />
        <KV k="JSON API"    v={<>http://localhost:<span style={{color:TERM.brand}}>4480</span></>} />
        <KV k="Wallet UI"   v={<>http://localhost:<span style={{color:TERM.brand}}>4485</span></>} />
        <KV k="Scan UI"     v={<>http://localhost:<span style={{color:TERM.brand}}>4487</span></>} />
        <KV k="Credentials" v={<>~/.canton-devkit/hubble/auth.json</>} />
      </Section>

      <Section title="Identities">
        <Line><span style={{color:TERM.dim}}>alice@localnet  </span>{C.mag('party::1220a8…')}<span style={{color:TERM.dim}}>   bob@localnet  </span>{C.mag('party::1220c4…')}</Line>
      </Section>
    </Terminal>
  );
}

function ScreenDoctor() {
  return (
    <Terminal title="canton-devkit · localnet doctor" height={540}>
      <Prompt cmd="dpm localnet doctor" />
      <Line dim>Checking host readiness for Canton LocalNet…</Line>
      <Line />
      <Section title="System">
        <Step icon="check" label="macOS 14.4 · arm64"            detail="supported" />
        <Step icon="check" label="Docker CLI"                    detail="v25.0.5" />
        <Step icon="check" label="Docker daemon reachable"       detail="unix:///var/run/docker.sock" />
        <Step icon="check" label="Docker Compose v2"             detail="v2.30.1" />
      </Section>
      <Section title="Resources">
        <Step icon="check" label="Memory available"   detail="24 GiB · need ≥ 8 GiB" />
        <Step icon="check" label="Disk available"     detail="38 GiB · need ≥ 12 GiB" />
        <Step icon="warn"  label="CPU"                detail={<><span style={{color:TERM.warn}}>4 cores</span><span style={{color:TERM.dim}}> · recommend ≥ 6</span></>} />
      </Section>
      <Section title="Network">
        <Step icon="check" label="Ports 4441–4490 free" />
        <Step icon="cross" label="Port 4485 in use"   detail={<><span style={{color:TERM.error}}>pid 88341 (orbstack-vmgr)</span></>} />
      </Section>
      <Line />
      <Box accent={TERM.warn} bg="#1A1612" pad="10px 14px" style={{ borderRadius: 6 }}>
        <Line><span style={{color:TERM.warn, fontWeight:600}}>! </span><span style={{color:TERM.text}}>1 issue · 1 warning</span></Line>
        <Line dim style={{ marginTop: 4 }}>Free port 4485 or run with <span style={{color:TERM.text}}>--ports wallet=4585</span> before <span style={{color:TERM.text}}>localnet up</span>.</Line>
      </Box>
    </Terminal>
  );
}

function ScreenLogs() {
  const L = (svc, level, msg, color) => (
    <Line>
      <span style={{color:TERM.faint}}>15:42:</span>
      <span style={{color:TERM.dim}}>{(Math.floor(Math.random()*60)).toString().padStart(2,'0')}.</span>
      <span style={{color:TERM.faint}}>{(Math.floor(Math.random()*900)+100)}</span>
      <span style={{color:TERM.dim}}>  </span>
      <span style={{color:TERM.info, display:'inline-block', width:170}}>{svc}</span>
      <span style={{color, fontWeight:600, display:'inline-block', width:50}}>{level}</span>
      <span style={{color:TERM.text}}>{msg}</span>
    </Line>
  );
  return (
    <Terminal title="canton-devkit · localnet logs (streaming)" height={460}
      footer={<><span>following 7 services · 612 lines</span><span><span style={{color:TERM.text}}>/</span> filter   <span style={{color:TERM.text}}>g</span> grep   <span style={{color:TERM.text}}>q</span> quit</span></>}>
      <Prompt cmd={<>dpm localnet logs participant-alice --since 5m --follow</>} />
      <Line />
      {L('participant-alice','INFO',  'Ledger API listening on 0.0.0.0:4441', TERM.success)}
      {L('participant-alice','INFO',  'Domain connection established · sequencer/0', TERM.success)}
      {L('participant-alice','DEBUG', 'PackageService.UploadDar received → 0x9f2c0a…', TERM.dim)}
      {L('participant-alice','INFO',  'Vetted package retail-token-1.0.3 (lf v1.17)', TERM.success)}
      {L('participant-alice','WARN',  'Slow transaction processing 412ms · tx 0x1a3c…', TERM.warn)}
      {L('participant-alice','INFO',  'Created contract retail-token:Token#0x77bd', TERM.success)}
      {L('participant-alice','INFO',  'Exercised choice Transfer on 0x77bd', TERM.success)}
      {L('participant-alice','ERROR', 'AuthenticationError: missing token for party bob', TERM.error)}
      {L('participant-alice','INFO',  'Retry succeeded · 1 attempt', TERM.success)}
      {L('participant-alice','DEBUG', 'StateService.GetActiveContracts → 142 items', TERM.dim)}
      {L('participant-alice','INFO',  'Archived contract retail-token:Token#0x77bd', TERM.success)}
      <Line><span style={{color:TERM.brand}}>{'⠹ '}</span><span style={{color:TERM.dim}}>streaming…</span><span className="term-cursor" /></Line>
    </Terminal>
  );
}

function ScreenDown() {
  return (
    <Terminal title="canton-devkit · localnet down" height={300}>
      <Prompt cmd={<>dpm localnet down <span style={{color:TERM.amber}}>--name</span> hubble</>} />
      <Step icon="check" label="Draining ledger" detail="3 in-flight commands" time="0.4s" />
      <Step icon="check" label="Stopping services" detail="7 containers" time="1.2s" />
      <Step icon="check" label="Detaching networks · keeping volumes" time="0.3s" />
      <Line />
      <Box accent={TERM.brand} bg="#11181A" pad="9px 14px" style={{ borderRadius: 6 }}>
        <Line><span style={{color:TERM.brand, fontWeight:600}}>✦ </span><span style={{color:TERM.text}}>Stopped LocalNet </span><span style={{color:TERM.text, fontWeight:600}}>"hubble"</span><span style={{color:TERM.dim}}> · state preserved.</span></Line>
        <Line dim style={{marginTop:4}}>Run <span style={{color:TERM.text}}>localnet up --name hubble</span> to resume, or <span style={{color:TERM.text}}>localnet clean</span> to remove volumes.</Line>
      </Box>
    </Terminal>
  );
}

// ScreenContainerList — `dpm localnet container list <inst>` (BIT-176).
// Mirrors the Container Health panel in the Web UI (frontend/src/screens/ContainerHealth.tsx).
// Each row shows a coloured glyph + service + state · health + raw docker status line.
function ScreenContainerList() {
  const C = (glyph, glyphColor, svc, state, status) => (
    <Line>
      <span style={{color:glyphColor, fontWeight:600, display:'inline-block', width:20}}>{glyph}</span>
      <span style={{color:TERM.text, display:'inline-block', width:260}}>{svc}</span>
      <span style={{color:TERM.text, display:'inline-block', width:200}}>{state}</span>
      <span style={{color:TERM.dim}}>{status}</span>
    </Line>
  );
  return (
    <Terminal title="canton-devkit · localnet container list" height={360}>
      <Prompt cmd={<>dpm localnet container list <span style={{color:TERM.amber}}>pr432</span></>} />
      <Section title="containers · pr432" right="project canton-pr432 · 12 containers">
        {C('✓', TERM.success, 'canton',                    'running · healthy',  'Up 36 minutes (healthy)')}
        {C('✓', TERM.success, 'postgres',                  'running · healthy',  'Up 36 minutes (healthy)')}
        {C('✓', TERM.success, 'splice',                    'running · healthy',  'Up 29 minutes (healthy)')}
        {C('●', TERM.success, 'nginx',                     'running',            'Up 36 minutes')}
        {C('●', TERM.success, 'swagger-ui',                'running',            'Up 36 minutes')}
        {C('✓', TERM.success, 'sv-web-ui',                 'running · healthy',  'Up 36 minutes (healthy)')}
        {C('✓', TERM.success, 'wallet-web-ui-sv',          'running · healthy',  'Up 36 minutes (healthy)')}
        {C('↻', TERM.warn,    'canton',                    'restarting',         'Restarting (1) 4 seconds ago')}
        {C('⊗', TERM.error,   'participant-bob',           'running · unhealthy','Up 12 minutes (unhealthy)')}
      </Section>
    </Terminal>
  );
}

// ScreenContainerRestart — `dpm localnet container restart <inst> <svc>` (BIT-176).
// Mirrors POST /api/instances/{n}/containers/{c}/restart. Defence-in-depth: the
// container is verified to belong to the instance's compose project before
// docker restart fires (rejects arbitrary host containers).
function ScreenContainerRestart() {
  return (
    <Terminal title="canton-devkit · localnet container restart" height={200}>
      <Prompt cmd={<>dpm localnet container restart <span style={{color:TERM.amber}}>pr432</span> <span style={{color:TERM.amber}}>splice</span></>} />
      <Step icon="check" label="Verifying container belongs to project canton-pr432" time="0.3s" />
      <Step icon="check" label="Restarting pr432-splice (docker restart)" time="11.4s" />
      <Line />
      <Line><span style={{color:TERM.success, fontWeight:600}}>✓ </span><span style={{color:TERM.text}}>restarted </span><span style={{color:TERM.text, fontWeight:600}}>pr432-splice</span></Line>
      <Line dim>Container health may take a few seconds to flip from <span style={{color:TERM.brand}}>starting</span> to <span style={{color:TERM.success}}>healthy</span>.</Line>
    </Terminal>
  );
}

// ScreenRefresh — `dpm localnet refresh` (BIT-177).
// On-demand mirror of the background reconciler that `dpm localnet ui` runs.
// Probes docker for every registered instance and flips registry status when
// it diverges. Silent for instances already in sync.
function ScreenRefresh() {
  return (
    <Terminal title="canton-devkit · localnet refresh" height={240}>
      <Prompt cmd={<>dpm localnet refresh</>} />
      <Line><span style={{color:TERM.dim}}>probing docker compose ps for 3 instance(s)…</span></Line>
      <Step icon="check" label="hubble: stopped → running" detail="docker truth: 7 containers" time="2.1s" />
      <Step icon="check" label="pr432: failed → running" detail="docker truth: 12 containers (OOM recovered)" time="2.4s" />
      <Line dim>demo: already running · no change</Line>
      <Line />
      <Box accent={TERM.success} bg="#11181A" pad="9px 14px" style={{ borderRadius: 6 }}>
        <Line><span style={{color:TERM.success, fontWeight:600}}>✓ </span><span style={{color:TERM.text}}>2 instance(s) re-synced from docker truth.</span></Line>
        <Line dim style={{marginTop:4}}>Run <span style={{color:TERM.text}}>localnet list</span> to see the updated status.</Line>
      </Box>
    </Terminal>
  );
}

Object.assign(window, {
  ScreenUp, ScreenStatus, ScreenDoctor, ScreenLogs, ScreenDown,
  ScreenContainerList, ScreenContainerRestart, ScreenRefresh,
});
