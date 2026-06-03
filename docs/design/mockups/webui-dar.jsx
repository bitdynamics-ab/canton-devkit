// DAR Manager — drag-drop upload, per-participant vetting toggles,
// package explorer tree, SCU diff viewer, hot-deploy indicator.

function VetToggle({ on = true, label }) {
  return (
    <div style={{display:'flex', alignItems:'center', gap:8, padding:'5px 9px', background: on ? `${W.ok}14` : W.surface2, border:`1px solid ${on ? W.ok+'44' : W.border}`, borderRadius:6}}>
      <span style={{
        width:24, height:14, borderRadius:8, background: on ? W.ok : W.faint,
        position:'relative', flex:'0 0 auto',
      }}>
        <span style={{
          position:'absolute', top:1, left: on ? 11 : 1, width:12, height:12, borderRadius:'50%', background:'#0B0E13', transition:'left .15s'
        }} />
      </span>
      <span style={{fontSize:12, fontWeight:500, color: on ? W.text : W.dim}}>{label}</span>
    </div>
  );
}

function TreeNode({ icon, color = W.dim, label, meta, indent = 0, open = true, last, branch }) {
  return (
    <div style={{display:'flex', alignItems:'center', gap:6, padding:'3px 0', paddingLeft: indent * 18, fontSize: 12.5, fontFamily: wMono, color: W.text}}>
      {indent > 0 && <span style={{color: W.faint, marginLeft: -10}}>{last ? '└─' : '├─'}</span>}
      <span style={{color, width:14, textAlign:'center'}}>{icon}</span>
      <span style={{color: W.text}}>{label}</span>
      {meta && <span style={{color: W.dim, marginLeft: 6, fontSize:11.5}}>{meta}</span>}
    </div>
  );
}

function DarPkgRow({ name, version, packageId, status = 'vetted', hot = false, active = false }) {
  const c = status === 'vetted' ? W.ok : status === 'partial' ? W.warn : W.err;
  return (
    <div className="w-row" style={{
      display:'grid', gridTemplateColumns:'1.6fr .6fr 1fr .8fr .4fr',
      gap:14, padding:'8px 12px', alignItems:'center', cursor:'pointer',
      background: active ? W.selRow : 'transparent',
      borderLeft: active ? `2px solid ${W.brand}` : '2px solid transparent',
      borderBottom:`1px solid ${W.border}`,
    }}>
      <div style={{display:'flex', alignItems:'center', gap:8}}>
        <span style={{color: hot ? W.warn : W.dim, width:14, textAlign:'center'}}>{hot ? '◐' : '◇'}</span>
        <span style={{fontWeight: active ? 600 : 500, fontSize:12.5}}>{name}</span>
        {hot && <Pill color={W.warn}>hot</Pill>}
      </div>
      <span style={{color: W.text2, fontFamily: wMono, fontSize:12}}>{version}</span>
      <span style={{color: W.mag, fontFamily: wMono, fontSize:11.5}}>{packageId}</span>
      <span><Pill color={c}><Dot color={c} /> {status}</Pill></span>
      <span style={{color: W.dim, fontSize:11.5, textAlign:'right'}}>···</span>
    </div>
  );
}

function DarManagerScreen() {
  return (
    <AppShell active="dar" instance="hubble"
      topRight={<>
        <Pill color={W.warn}><Dot color={W.warn} pulse /> hot-deploy 0:04 ago</Pill>
        <button className="w-btn"><span style={{color:W.brand}}>↻</span> Rebuild</button>
        <button className="w-btn primary"><span>+</span> Upload DAR</button>
      </>}>
      {/* Header strip with title + watch-mode indicator */}
      <section style={{display:'flex', alignItems:'flex-end', justifyContent:'space-between', marginBottom:14, gap:16}}>
        <div>
          <h1 style={{margin:0, fontSize:20, fontWeight:600, letterSpacing:-0.4}}>DAR Manager</h1>
          <div style={{color:W.dim, fontSize:12.5, marginTop:3}}>32 packages across 3 participants · watch mode active on <span style={{color:W.text}}>./daml</span></div>
        </div>
        <div style={{display:'flex', gap:8}}>
          <button className="w-btn">Diff…</button>
          <button className="w-btn">Download</button>
          <button className="w-btn">Remove</button>
        </div>
      </section>

      <div style={{display:'grid', gridTemplateColumns:'320px 1fr 360px', gap:14, alignItems:'start'}}>
        {/* LEFT: Drop zone + per-participant vetting */}
        <div style={{display:'flex', flexDirection:'column', gap:14}}>
          <Card pad={0}>
            <div style={{
              margin:14,
              border:`1.5px dashed ${W.brand}55`,
              borderRadius:10,
              padding:'22px 16px', textAlign:'center',
              background: `linear-gradient(180deg, ${W.brand}0A 0%, transparent 100%)`,
            }}>
              <div style={{fontSize:26, color:W.brand, marginBottom:8}}>⬆</div>
              <div style={{fontWeight:600, marginBottom:3, fontSize:13}}>Drop DAR here</div>
              <div style={{color:W.dim, fontSize:11.5}}>or click to browse · multi-file ok</div>
            </div>
            <div style={{padding:'4px 14px 14px', borderTop:`1px solid ${W.border}`}}>
              <div style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, marginTop:8, marginBottom:8}}>Vet on upload</div>
              <div style={{display:'flex', flexDirection:'column', gap:6}}>
                <VetToggle on label="sv-participant" />
                <VetToggle on label="app-provider-participant" />
                <VetToggle on label="app-user-participant" />
              </div>
              <div style={{marginTop:12, padding:'8px 10px', background:W.surface2, borderRadius:6, fontSize:11.5, color:W.text2, lineHeight:1.5}}>
                <span style={{color:W.brand}}>ⓘ</span> SCU compatibility is pre-checked against the prior version before upload.
              </div>
            </div>
          </Card>

          <Card title="Watch mode" subtitle="dpm build → upload on change">
            <div style={{display:'flex', flexDirection:'column', gap:8, fontSize:12, fontFamily: wMono}}>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.dim}}>watching</span><span style={{color:W.text}}>./daml/**/*.daml</span></div>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.dim}}>debounce</span><span style={{color:W.text}}>250ms</span></div>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.dim}}>builds</span><span style={{color:W.text}}>3 ok · 0 failed</span></div>
              <div style={{display:'flex', justifyContent:'space-between'}}><span style={{color:W.dim}}>last build</span><span style={{color:W.ok}}>4.1s · 0:04 ago</span></div>
              <div style={{height:1, background:W.border, margin:'4px 0'}} />
              <div style={{color:W.dim, fontSize:11.5, fontFamily: wSans}}>
                <Dot color={W.ok} pulse /> <span style={{marginLeft:6, color:W.text2}}>uploading to participants as you save</span>
              </div>
            </div>
          </Card>
        </div>

        {/* MIDDLE: Package list */}
        <Card pad={0} title="Packages on app-provider-participant" subtitle="filter: app DARs only · 8 results"
          action={
            <div style={{display:'flex', gap:6}}>
              <button className="w-btn" style={{padding:'5px 10px'}}>app-provider ⌄</button>
              <button className="w-btn" style={{padding:'5px 10px'}}>app DARs ⌄</button>
            </div>
          }>
          <div style={{
            display:'grid', gridTemplateColumns:'1.6fr .6fr 1fr .8fr .4fr',
            gap:14, padding:'9px 12px', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600,
            borderBottom:`1px solid ${W.border}`,
          }}>
            <span>Package</span><span>Version</span><span>Package-id</span><span>Vetting</span><span></span>
          </div>
          <DarPkgRow name="retail-token"        version="1.1.0" packageId="0x9f2c0a…d12c" status="vetted" hot active />
          <DarPkgRow name="retail-token"        version="1.0.3" packageId="0x4ab120…ff01" status="vetted" />
          <DarPkgRow name="onboarding-flow"     version="0.2.1" packageId="0x2c19be…8f3a" status="vetted" />
          <DarPkgRow name="token-standard-v2"   version="0.3.1" packageId="0x1f44de…77c8" status="vetted" />
          <DarPkgRow name="splice-amulet"       version="0.1.17" packageId="0x880aaa…2034" status="vetted" />
          <DarPkgRow name="compatibility-shim"  version="0.0.4" packageId="0xa4ee10…009b" status="partial" />
          <DarPkgRow name="experimental-bridge" version="0.0.1" packageId="0xeebb91…0aa1" status="unvetted" />
          <div style={{padding:'10px 12px', color:W.dim, fontSize:11.5, display:'flex', justifyContent:'space-between'}}>
            <span>7 packages · 24 transitives hidden</span>
            <span>↑↓ navigate · ↵ inspect</span>
          </div>
        </Card>

        {/* RIGHT: Inspect drawer with diff */}
        <Card title={<><span style={{color:W.text}}>retail-token</span> <span style={{color:W.dim, fontWeight:400}}>1.1.0</span></>}
          subtitle="Inspect · diff vs 1.0.3" pad={0}>
          <div style={{padding:'12px 16px', borderBottom:`1px solid ${W.border}`}}>
            <div style={{display:'grid', gridTemplateColumns:'auto 1fr', gap:6, fontSize:12, fontFamily: wMono}}>
              <span style={{color:W.dim}}>pkg-id</span><span style={{color:W.mag}}>0x9f2c0a…d12c</span>
              <span style={{color:W.dim}}>hash</span><span style={{color:W.text2}}>sha256:8e2c…0a14</span>
              <span style={{color:W.dim}}>lf</span><span style={{color:W.text}}>1.17</span>
              <span style={{color:W.dim}}>uploaded</span><span style={{color:W.text2}}>2 minutes ago</span>
              <span style={{color:W.dim}}>vetted on</span><span style={{color:W.text2}}>3 participants</span>
            </div>
          </div>

          <div style={{padding:'12px 12px 6px', display:'flex', alignItems:'center', gap:8}}>
            <span style={{color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600}}>SCU diff</span>
            <Pill color={W.warn}>⚠ 1 breaking</Pill>
            <span style={{color:W.dim, fontSize:11.5}}>vs 1.0.3</span>
          </div>
          <div style={{padding:'4px 8px 14px', fontSize:12, fontFamily:wMono}}>
            <div style={{padding:'4px 8px', color:W.text}}>Retail.Token.Token</div>
            <div style={{padding:'3px 8px', background:`${W.ok}10`, borderLeft:`2px solid ${W.ok}`, color:W.text2}}>
              <span style={{color:W.ok, fontWeight:700, marginRight:6}}>+</span> field metadata : Optional Text
            </div>
            <div style={{padding:'3px 8px', background:`${W.warn}10`, borderLeft:`2px solid ${W.warn}`, color:W.text2}}>
              <span style={{color:W.warn, fontWeight:700, marginRight:6}}>~</span> choice Transfer +arg memo
            </div>
            <div style={{padding:'3px 8px', background:`${W.ok}10`, borderLeft:`2px solid ${W.ok}`, color:W.text2}}>
              <span style={{color:W.ok, fontWeight:700, marginRight:6}}>+</span> implements ITokenV2
            </div>
            <div style={{padding:'6px 8px 2px', color:W.text}}>Retail.Token.Mint</div>
            <div style={{padding:'3px 8px', background:`${W.err}10`, borderLeft:`2px solid ${W.err}`, color:W.text2}}>
              <span style={{color:W.err, fontWeight:700, marginRight:6}}>−</span> field legacyId : Text  <span style={{color:W.err}}>breaking</span>
            </div>

            <div style={{padding:'8px 8px 0', color:W.dim, fontSize:10.5, letterSpacing:1.4, textTransform:'uppercase', fontWeight:600, fontFamily: wSans}}>Package tree</div>
            <div style={{padding:'6px 8px'}}>
              <TreeNode icon="❒" color={W.brand} label="Retail.Token" />
              <TreeNode icon="◫" color={W.info} label="Token"        meta="template" indent={1} />
              <TreeNode icon="∴" color={W.amber} label="Transfer"     meta="choice  memo : Optional Text" indent={2} />
              <TreeNode icon="∴" color={W.amber} label="Burn"         meta="choice  amount : Decimal" indent={2} last />
              <TreeNode icon="◫" color={W.info} label="TokenProposal" meta="template (new)" indent={1} last />
              <TreeNode icon="❒" color={W.brand} label="Retail.Registry" />
              <TreeNode icon="◐" color={W.mag} label="IRegistryView" meta="interface" indent={1} last />
            </div>
          </div>
        </Card>
      </div>
    </AppShell>
  );
}

Object.assign(window, { DarManagerScreen });
