// DAR management screens: upload / list / info / diff / watch

function ScreenDarUpload() {
  return (
    <Terminal title="canton-devkit · dar upload" height={440}>
      <Prompt cmd={<>dpm localnet dar upload <span style={{color:TERM.text}}>./.daml/dist/retail-token-1.1.0.dar</span> <span style={{color:TERM.amber}}>--all-participants</span> <span style={{color:TERM.amber}}>--vet</span></>} />
      <Line />
      <Line><span style={{color:TERM.dim}}>Reading DAR  </span><span style={{color:TERM.text}}>retail-token-1.1.0.dar</span><span style={{color:TERM.dim}}> · 1.2 MiB · 7 modules</span></Line>
      <Line><span style={{color:TERM.dim}}>Package ID   </span><span style={{color:TERM.magenta}}>0x9f2c0aef41b3…d12c</span></Line>
      <Line><span style={{color:TERM.dim}}>LF version   </span><span style={{color:TERM.text}}>1.17 </span><span style={{color:TERM.dim}}>· depends on </span><span style={{color:TERM.magenta}}>daml-stdlib 2.7</span><span style={{color:TERM.dim}}>, </span><span style={{color:TERM.magenta}}>token-standard-v2 0.3.1</span></Line>
      <Line />
      <Section title="upload">
        <Step icon="check" label="participant-alice" detail={<><span style={{color:TERM.dim}}>uploaded · </span>{C.ok('vetted')}</>} time="0.6s" />
        <Step icon="check" label="participant-bob"   detail={<><span style={{color:TERM.dim}}>uploaded · </span>{C.ok('vetted')}</>} time="0.7s" />
      </Section>
      <Line />
      <Box accent={TERM.success} bg="#0F1A14" pad="9px 14px" style={{ borderRadius: 6 }}>
        <Line><span style={{color:TERM.success, fontWeight:600}}>✓ </span><span style={{color:TERM.text}}>2 participants · </span><span style={{color:TERM.text, fontWeight:600}}>retail-token-1.1.0</span><span style={{color:TERM.dim}}> is now resolvable.</span></Line>
        <Line dim style={{marginTop:4}}>SCU check: <span style={{color:TERM.success}}>compatible</span> with 1.0.3 — see <span style={{color:TERM.text}}>dpm localnet dar diff retail-token@1.0.3 retail-token@1.1.0</span></Line>
      </Box>
    </Terminal>
  );
}

function ScreenDarList() {
  return (
    <Terminal title="canton-devkit · dar list" height={460}>
      <Prompt cmd={<>dpm localnet dar list <span style={{color:TERM.amber}}>--participant</span> alice</>} />
      <Line dim>32 packages on participant-alice · including stdlib transitives</Line>
      <Line />
      <Table columns={[
        { label:'package',          w:'1.6fr' },
        { label:'version',          w:'.6fr' },
        { label:'lf',               w:'.4fr' },
        { label:'package-id',       w:'1.2fr' },
        { label:'modules',          w:'.6fr', align:'right' },
        { label:'vetted',           w:'.6fr' },
        { label:'uploaded',         w:'.8fr', align:'right' },
      ]} rows={[
        [C.bold('retail-token'),       '1.1.0', '1.17', C.mag('0x9f2c0a…d12c'), '7',  C.ok('● yes'), C.dim('2m ago')],
        [C.bold('retail-token'),       '1.0.3', '1.17', C.mag('0x4ab120…ff01'), '6',  C.ok('● yes'), C.dim('1d ago')],
        ['onboarding-flow',            '0.2.1', '1.17', C.mag('0x2c19be…8f3a'), '4',  C.ok('● yes'), C.dim('3d ago')],
        ['token-standard-v2',          '0.3.1', '1.17', C.mag('0x1f44de…77c8'), '12', C.ok('● yes'), C.dim('5d ago')],
        ['daml-stdlib',                '2.7.0', '1.17', C.mag('0x880aaa…2034'), '38', C.ok('● yes'), C.dim('upstream')],
        ['daml-prim',                  '2.7.0', '1.17', C.mag('0x77bb29…c019'), '14', C.ok('● yes'), C.dim('upstream')],
        ['compatibility-shim',         '0.0.4', '1.15', C.mag('0xa4ee10…009b'), '2',  C.warn('◐ partial'), C.dim('6d ago')],
      ]} />
      <Line />
      <Line dim>Showing 7 of 32 · <span style={{color:TERM.text}}>--all</span> to include transitives · <span style={{color:TERM.text}}>--json</span> for machine output</Line>
    </Terminal>
  );
}

function ScreenDarInfo() {
  return (
    <Terminal title="canton-devkit · dar info" height={560}>
      <Prompt cmd={<>dpm localnet dar info <span style={{color:TERM.text}}>retail-token@1.1.0</span></>} />
      <Line />
      <KV k="Package"     v={<><span style={{color:TERM.text, fontWeight:600}}>retail-token</span> <span style={{color:TERM.dim}}>v</span>1.1.0</>} />
      <KV k="Package ID"  v={C.mag('0x9f2c0aef41b3…d12c')} />
      <KV k="LF version"  v="1.17" />
      <KV k="Hash"        v={C.mag('sha256:8e2c…0a14')} />
      <KV k="Vetted on"   v={<><span style={{color:TERM.text}}>participant-alice</span><span style={{color:TERM.dim}}>, </span><span style={{color:TERM.text}}>participant-bob</span></>} />
      <KV k="Depends on"  v={<><span style={{color:TERM.magenta}}>daml-stdlib 2.7.0</span>, <span style={{color:TERM.magenta}}>token-standard-v2 0.3.1</span></>} />
      <Section title="Modules · Templates">
        <Line><span style={{color:TERM.dim}}>└─ </span><span style={{color:TERM.text}}>Retail.Token</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>├─ template </span>{C.bold('Token')}<span style={{color:TERM.dim}}>   { '{' } issuer owner amount metadata { '}' }</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>├─ choice </span>{C.brand('Transfer')}<span style={{color:TERM.dim}}>     newOwner : Party</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>├─ choice </span>{C.brand('Burn')}<span style={{color:TERM.dim}}>         amount : Decimal</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>└─ implements </span>{C.mag('ITokenV2')}</Line>
        <Line indent={1}><span style={{color:TERM.dim}}>└─ template </span>{C.bold('TokenProposal')}<span style={{color:TERM.dim}}> { '{' } issuer recipient amount { '}' }</span></Line>
        <Line indent={2}><span style={{color:TERM.dim}}>└─ choice </span>{C.brand('Accept')}</Line>
        <Line><span style={{color:TERM.dim}}>└─ </span><span style={{color:TERM.text}}>Retail.Registry</span></Line>
        <Line indent={1}><span style={{color:TERM.dim}}>└─ interface </span>{C.mag('IRegistryView')}</Line>
      </Section>
      <Line />
      <Line dim>Use <span style={{color:TERM.text}}>dpm localnet dar diff retail-token@1.0.3 retail-token@1.1.0</span> to see what changed.</Line>
    </Terminal>
  );
}

function ScreenDarDiff() {
  const D = ({ sign, color, children, indent = 0 }) => (
    <Line indent={indent} style={{ background: sign === '-' ? 'rgba(244,124,124,.07)' : sign === '+' ? 'rgba(94,226,160,.07)' : 'transparent', borderLeft: `2px solid ${sign === '-' ? TERM.error : sign === '+' ? TERM.success : 'transparent'}`, paddingLeft: 8 }}>
      <span style={{color, fontWeight:700, marginRight: 6}}>{sign || ' '}</span>{children}
    </Line>
  );
  return (
    <Terminal title="canton-devkit · dar diff (SCU-aware)" height={540}>
      <Prompt cmd={<>dpm localnet dar diff <span style={{color:TERM.text}}>retail-token@1.0.3</span> <span style={{color:TERM.text}}>retail-token@1.1.0</span></>} />
      <Line dim>Comparing 1.0.3 → 1.1.0  ·  LF 1.17 → 1.17  ·  name unchanged</Line>
      <Line />
      <Section title="Templates" right={<>3 changed · 1 added · 0 removed</>}>
        <Line><span style={{color:TERM.text}}>Retail.Token.Token</span></Line>
        <D sign="+" color={TERM.success} indent={1}>field <span style={{color:TERM.brand}}>metadata</span>  <span style={{color:TERM.dim}}>: Optional Text  </span>{C.ok('SCU-safe (optional)')}</D>
        <D sign="~" color={TERM.warn} indent={1}>choice <span style={{color:TERM.brand}}>Transfer</span> <span style={{color:TERM.dim}}>added arg </span><span style={{color:TERM.text}}>memo : Optional Text</span>  {C.ok('SCU-safe')}</D>
        <D sign="+" color={TERM.success} indent={1}>implements <span style={{color:TERM.magenta}}>ITokenV2</span>  {C.ok('compatible')}</D>
        <Line><span style={{color:TERM.text}}>Retail.Token.TokenProposal</span> {C.dim('· new template')}</Line>
        <D sign="+" color={TERM.success} indent={1}>template <span style={{color:TERM.brand}}>TokenProposal</span>  {C.ok('additive')}</D>
        <Line />
        <Line><span style={{color:TERM.text}}>Retail.Token.Mint</span></Line>
        <D sign="-" color={TERM.error} indent={1}>field <span style={{color:TERM.rose}}>legacyId</span>  <span style={{color:TERM.dim}}>: Text  </span>{C.err('breaking — removed required field')}</D>
      </Section>
      <Line />
      <Box accent={TERM.warn} bg="#1A1612" pad="9px 14px" style={{ borderRadius: 6 }}>
        <Line><span style={{color:TERM.warn, fontWeight:600}}>! SCU verdict: </span><span style={{color:TERM.warn}}>BREAKING</span><span style={{color:TERM.dim}}> for </span><span style={{color:TERM.text}}>Retail.Token.Mint</span><span style={{color:TERM.dim}}>. Other changes are upgrade-safe.</span></Line>
        <Line dim style={{marginTop:4}}>Authoritative validation runs at upload — this is best-effort signal.</Line>
      </Box>
    </Terminal>
  );
}

function ScreenDarWatch() {
  return (
    <Terminal title="canton-devkit · dar watch (hot reload)" height={400}
      footer={<><span>watching ./daml  ·  3 builds · 0 failed</span><span><span style={{color:TERM.text}}>r</span> rebuild   <span style={{color:TERM.text}}>q</span> quit</span></>}>
      <Prompt cmd={<>dpm localnet dar watch <span style={{color:TERM.text}}>./</span> <span style={{color:TERM.amber}}>--participant</span> alice <span style={{color:TERM.amber}}>--participant</span> bob</>} />
      <Line dim>Watching <span style={{color:TERM.text}}>./daml/**/*.daml</span> · debounced 250ms</Line>
      <Line />
      <Line><span style={{color:TERM.faint}}>[15:42:04] </span><span style={{color:TERM.info}}>change </span><span style={{color:TERM.text}}>daml/Retail/Token.daml</span></Line>
      <Line><span style={{color:TERM.faint}}>[15:42:04] </span><span style={{color:TERM.brand}}>build  </span><span style={{color:TERM.dim}}>delegating to </span><span style={{color:TERM.text}}>dpm build</span></Line>
      <Line><span style={{color:TERM.faint}}>[15:42:08] </span>{Sym.check}<span style={{color:TERM.text}}> built retail-token-1.1.0.dar </span><span style={{color:TERM.dim}}>· 4.1s</span></Line>
      <Line><span style={{color:TERM.faint}}>[15:42:08] </span>{Sym.check}<span style={{color:TERM.text}}> uploaded → alice </span><span style={{color:TERM.dim}}>· 0.6s · </span>{C.ok('vetted')}</Line>
      <Line><span style={{color:TERM.faint}}>[15:42:09] </span>{Sym.check}<span style={{color:TERM.text}}> uploaded → bob   </span><span style={{color:TERM.dim}}>· 0.7s · </span>{C.ok('vetted')}</Line>
      <Line />
      <Line><span style={{color:TERM.brand}}>⠹ </span><span style={{color:TERM.dim}}>watching for changes…</span><span className="term-cursor" /></Line>
    </Terminal>
  );
}

Object.assign(window, { ScreenDarUpload, ScreenDarList, ScreenDarInfo, ScreenDarDiff, ScreenDarWatch });
