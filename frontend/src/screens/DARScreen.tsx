import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  fetchDARList,
  fetchDARVetting,
  setDARVetting,
  subscribeDARWatch,
  uploadDARs,
  type DARListResponse,
  type DARRow,
  type DARUploadRoleResult,
  type DARVettingRow,
  type DARWatchEvent,
  type Role,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";
import { DARPackageTree } from "./DARPackageTree";
import { DARDiff } from "./DARDiff";

// DARScreen — three-column DAR manager:
//   LEFT (320px)   drag-drop upload + per-participant vetting toggles
//                  + Watch-mode card
//   MIDDLE         package list (Package · Version · Package-id ·
//                  Vetting)
//   RIGHT (360px)  inspect drawer with package tree / structural diff
//
// Vetting is live end-to-end: the package-list column (VettingCell)
// and the inspect-drawer toggles (VettingPanel) both read
// per-participant state from GET …/dar/{id}/vetting and POST to the
// vet/unvet endpoint. The Watch-mode card reflects SSE events from a
// `dpm localnet dar watch` process when one is running, and stays
// "Idle" otherwise.

const ROLES: Role[] = ["app-user", "app-provider", "sv"];

type UploadState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filenames: string[] }
  | { kind: "success"; total: number; results: DARUploadRoleResult[] }
  | { kind: "partial"; total: number; results: DARUploadRoleResult[] }
  | { kind: "error"; message: string };

export function DARScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  // Which participants an upload fans out to (the backend dials each in
  // parallel). Default ON for all three so "vet everywhere" is one
  // drag-and-drop. Orthogonal to `role`, which drives the package LIST:
  // the user can read one participant's packages while uploading to a
  // different subset.
  const [vetTargets, setVetTargets] = useState<Record<Role, boolean>>({
    "app-user": true,
    "app-provider": true,
    sv: true,
  });
  const selectedRoles = (Object.entries(vetTargets) as Array<[Role, boolean]>)
    .filter(([, on]) => on)
    .map(([r]) => r);
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: DARListResponse }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [selectedHash, setSelectedHash] = useState<string | null>(null);
  // Diff mode: a picked "compare with" target flips the right drawer
  // from the inspect tree to DARDiff. Kept separate from selectedHash
  // so the user can toggle the comparison off without losing their
  // primary selection.
  const [compareHash, setCompareHash] = useState<string | null>(null);
  const [upload, setUpload] = useState<UploadState>({ kind: "idle" });
  const [dragOver, setDragOver] = useState(false);
  const [filter, setFilter] = useState<"all" | "app">("all");
  const [tick, setTick] = useState(0); // bump to refetch after upload
  // Per-participant vetting per listed DAR, keyed by main package id;
  // populated lazily by the batch-fetch effect below.
  const [vetting, setVetting] = useState<Record<string, VetState>>({});
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setState({ kind: "loading" });
    fetchDARList(name, role)
      .then((data) => {
        if (cancelled) return;
        setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (
          e instanceof ApiError &&
          e.code === "PARTICIPANT_PORT_NOT_RECORDED"
        ) {
          setState({
            kind: "port-missing",
            remediation:
              e.remediation?.[0] ??
              `Restart the instance to capture Canton API ports.`,
          });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load DARs",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name, role, tick]);

  async function doUpload(files: FileList | File[]) {
    if (!name) return;
    const arr = Array.from(files).filter((f) => f.name.endsWith(".dar"));
    if (arr.length === 0) {
      setUpload({
        kind: "error",
        message: "only .dar files are accepted",
      });
      return;
    }
    if (selectedRoles.length === 0) {
      setUpload({
        kind: "error",
        message:
          "select at least one target participant under 'Vet on upload' before dropping a DAR",
      });
      return;
    }
    // Mirrors the backend's multipart cap (darUploadMax = 64 MiB in
    // internal/ui/handlers/dar.go); reject client-side so an oversized
    // DAR doesn't upload just to fail server-side.
    const MAX_DAR_BYTES = 64 * 1024 * 1024;
    const tooBig = arr.find((f) => f.size > MAX_DAR_BYTES);
    if (tooBig) {
      setUpload({
        kind: "error",
        message: `${tooBig.name} is ${(tooBig.size / 1024 / 1024).toFixed(1)} MiB; per-file cap is 64 MiB`,
      });
      return;
    }
    setUpload({
      kind: "uploading",
      progress: 0,
      filenames: arr.map((f) => f.name),
    });
    try {
      const resp = await uploadDARs(name, arr, selectedRoles, (frac) => {
        setUpload((s) =>
          s.kind === "uploading" ? { ...s, progress: frac } : s,
        );
      });
      const anyFail = resp.results.some((r) => !r.ok);
      setUpload(
        anyFail
          ? { kind: "partial", total: resp.total_uploaded, results: resp.results }
          : { kind: "success", total: resp.total_uploaded, results: resp.results },
      );
      setTick((n) => n + 1);
    } catch (e) {
      setUpload({
        kind: "error",
        message: e instanceof ApiError ? e.message : "upload failed",
      });
    }
  }

  const rows = useMemo(() => {
    if (state.kind !== "ok") return [] as DARRow[];
    let list = state.data.dars;
    if (filter === "app") {
      // Hide the canton/splice/daml system packages.
      list = list.filter(
        (d) =>
          !d.name.startsWith("canton-builtin-") &&
          !d.name.startsWith("splice-") &&
          !d.name.startsWith("daml-"),
      );
    }
    return list;
  }, [state, filter]);

  // Reset the vetting cache when the instance changes or the list is
  // refetched. Keyed by main id, so a role switch — same DARs,
  // different participant's list — reuses already-fetched verdicts.
  useEffect(() => {
    setVetting({});
  }, [name, tick]);

  // Lazily fetch real per-participant vetting for each visible row (the
  // endpoint fans out to all three participants server-side) so the
  // list column reflects ledger state. Rows are marked "loading" in one
  // batch before dispatch so re-renders never double-fetch.
  const visibleMains = useMemo(() => rows.map((d) => d.main).join(","), [rows]);
  useEffect(() => {
    if (!name || state.kind !== "ok") return;
    let cancelled = false;
    const toFetch = rows.filter((d) => vetting[d.main] === undefined);
    if (toFetch.length === 0) return;
    setVetting((prev) => {
      const next = { ...prev };
      for (const d of toFetch) next[d.main] = { kind: "loading" };
      return next;
    });
    for (const d of toFetch) {
      fetchDARVetting(name, d.main)
        .then((resp) => {
          if (cancelled) return;
          setVetting((prev) => ({
            ...prev,
            [d.main]: { kind: "ok", rows: resp.participants ?? [] },
          }));
        })
        .catch(() => {
          if (cancelled) return;
          setVetting((prev) => ({ ...prev, [d.main]: { kind: "err" } }));
        });
    }
    return () => {
      cancelled = true;
    };
    // visibleMains captures the row-set identity; vetting is read via
    // the functional updater so it isn't a dependency (would loop).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, state.kind, visibleMains]);

  const selected = useMemo(
    () =>
      state.kind === "ok"
        ? (rows ?? []).find((d) => d.main === selectedHash) ?? null
        : null,
    [state, rows, selectedHash],
  );

  if (!name) {
    return (
      <section style={{ padding: 24 }}>
        <p style={{ color: W.dim }}>
          No instance selected. Create or pick one from the dashboard first.
        </p>
      </section>
    );
  }

  return (
    <section style={{ padding: 24 }}>
      <header
        style={{
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          marginBottom: 14,
          gap: 16,
          flexWrap: "wrap",
        }}
      >
        <div>
          <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>
            DAR Manager
          </h2>
          <div style={{ color: W.dim, fontSize: 12.5, marginTop: 3 }}>
            {state.kind === "ok"
              ? `${state.data.dars.length} packages on ${role} participant`
              : "loading…"}
          </div>
        </div>
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {state.kind === "loading" && <Status>Loading DAR list…</Status>}
      {state.kind === "err" && <ErrorPanel msg={state.error} />}
      {state.kind === "port-missing" && (
        <EmptyPanel
          title="Participant ports not recorded"
          remediation={state.remediation}
        />
      )}

      {state.kind === "ok" && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "320px 1fr 360px",
            gap: 14,
            alignItems: "start",
          }}
        >
          {/* LEFT — upload + vetting + watch mode */}
          <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
            <Card>
              <div
                role="button"
                tabIndex={0}
                onDragOver={(e) => {
                  e.preventDefault();
                  setDragOver(true);
                }}
                onDragLeave={() => setDragOver(false)}
                onDrop={(e) => {
                  e.preventDefault();
                  setDragOver(false);
                  void doUpload(e.dataTransfer.files);
                }}
                onClick={() => fileInputRef.current?.click()}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    fileInputRef.current?.click();
                  }
                }}
                aria-label="Drop DAR file here or click to choose"
                style={{
                  margin: 14,
                  border: `1.5px dashed ${dragOver ? W.brand : `${W.brand}55`}`,
                  borderRadius: 10,
                  padding: "22px 16px",
                  textAlign: "center",
                  background: dragOver
                    ? `${W.brand}1A`
                    : `linear-gradient(180deg, ${W.brand}0A 0%, transparent 100%)`,
                  cursor: "pointer",
                  transition: "all 120ms",
                }}
              >
                {upload.kind === "uploading" ? (
                  <UploadProgress state={upload} />
                ) : (
                  <>
                    <div style={{ fontSize: 26, color: W.brand, marginBottom: 8 }}>⬆</div>
                    <div
                      style={{
                        fontWeight: 600,
                        marginBottom: 3,
                        fontSize: 13,
                        color: W.text,
                      }}
                    >
                      Drop DAR here
                    </div>
                    <div style={{ color: W.dim, fontSize: 11.5 }}>
                      or click to browse · multi-file ok
                    </div>
                  </>
                )}
              </div>
              <input
                ref={fileInputRef}
                type="file"
                accept=".dar"
                multiple
                style={{ display: "none" }}
                onChange={(e) => {
                  if (e.target.files) void doUpload(e.target.files);
                }}
              />
              <div
                style={{
                  padding: "4px 14px 14px",
                  borderTop: `1px solid ${W.border}`,
                }}
              >
                <SectionLabel>Vet on upload</SectionLabel>
                <div
                  style={{ display: "flex", flexDirection: "column", gap: 6 }}
                >
                  {(["sv", "app-provider", "app-user"] as Role[]).map((r) => (
                    <VetToggle
                      key={r}
                      on={vetTargets[r]}
                      onChange={(v) =>
                        setVetTargets((s) => ({ ...s, [r]: v }))
                      }
                      label={`${r} participant`}
                    />
                  ))}
                </div>
                <div
                  style={{
                    marginTop: 12,
                    padding: "8px 10px",
                    background: W.border,
                    borderRadius: 6,
                    fontSize: 11.5,
                    color: selectedRoles.length === 0 ? W.warn : W.text2,
                    lineHeight: 1.5,
                  }}
                >
                  {selectedRoles.length === 0 ? (
                    <>
                      <span style={{ color: W.warn }}>⚠</span> Pick at least one
                      target. Drops will be refused until a participant is
                      selected.
                    </>
                  ) : (
                    <>
                      <span style={{ color: W.brand }}>ⓘ</span> Each dropped DAR
                      uploads in parallel to <strong>{selectedRoles.length}</strong>{" "}
                      participant{selectedRoles.length === 1 ? "" : "s"} with
                      <code style={{ fontFamily: wMono, marginLeft: 4 }}>
                        vet_all_packages=true
                      </code>
                      .
                    </>
                  )}
                </div>
              </div>
            </Card>

            {(upload.kind === "success" || upload.kind === "partial") && (
              <UploadResultBanner
                kind={upload.kind}
                total={upload.total}
                results={upload.results}
              />
            )}
            {upload.kind === "error" && <ErrorBanner msg={upload.message} />}

            <WatchModeCard instance={name} />

          </div>

          {/* MIDDLE — package list */}
          <div
            style={{
              background: W.surface,
              border: `1px solid ${W.border}`,
              borderRadius: 10,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                padding: "11px 14px",
                borderBottom: `1px solid ${W.border}`,
                display: "flex",
                alignItems: "center",
                gap: 12,
              }}
            >
              <div>
                <div
                  style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}
                >
                  Packages on {role} participant
                </div>
                <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2 }}>
                  {filter === "app" ? "filter: app DARs only" : "all packages"}{" "}
                  · {rows.length} results
                </div>
              </div>
              <span style={{ marginLeft: "auto" }} />
              <FilterBtn
                active={filter === "all"}
                onClick={() => setFilter("all")}
              >
                all
              </FilterBtn>
              <FilterBtn
                active={filter === "app"}
                onClick={() => setFilter("app")}
              >
                app DARs only
              </FilterBtn>
            </div>

            {/* Column header */}
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1.6fr 0.6fr 1fr 0.8fr",
                gap: 14,
                padding: "9px 14px",
                color: W.dim,
                fontSize: 10.5,
                letterSpacing: 1.4,
                textTransform: "uppercase",
                fontWeight: 600,
                borderBottom: `1px solid ${W.border}`,
              }}
            >
              <span>Package</span>
              <span>Version</span>
              <span>Package-id</span>
              <span>Vetting</span>
            </div>

            {rows.length === 0 && (
              <div style={{ padding: 18, color: W.dim, fontSize: 12.5 }}>
                No packages match the current filter.
              </div>
            )}
            <div style={{ maxHeight: "55vh", overflowY: "auto" }}>
              {rows.map((d) => (
                <PkgRow
                  key={d.main}
                  row={d}
                  active={d.main === selectedHash}
                  vet={vetting[d.main]}
                  onClick={() => setSelectedHash(d.main)}
                />
              ))}
            </div>
            <div
              style={{
                padding: "10px 14px",
                color: W.dim,
                fontSize: 11.5,
                display: "flex",
                justifyContent: "space-between",
                borderTop: `1px solid ${W.border}`,
              }}
            >
              <span>{rows.length} packages</span>
              <span>↑↓ navigate · ↵ inspect · esc close</span>
            </div>
          </div>

          {/* RIGHT — inspect drawer / diff viewer */}
          <InspectDrawer
            row={selected}
            instance={name}
            role={role}
            compareWith={compareHash}
            onClearCompare={() => setCompareHash(null)}
            allRows={rows}
            onCompare={(other) => setCompareHash(other)}
          />
        </div>
      )}
    </section>
  );
}

// WatchModeCard subscribes to the DAR watch SSE stream and renders the
// latest lifecycle event as a "Watching" badge with a last-rebuild
// timer. With no events (no `dar watch` running) it stays "Idle".
function WatchModeCard({ instance }: { instance: string }) {
  const [last, setLast] = useState<DARWatchEvent | null>(null);
  const [active, setActive] = useState(false);
  // Re-render every 10s so the "ago" label stays fresh.
  const [, setNow] = useState(Date.now());

  useEffect(() => {
    const es = subscribeDARWatch(instance, "", (ev) => {
      setLast(ev);
      if (ev.event === "watch_started" || ev.event === "rebuild_started") {
        setActive(true);
      } else if (ev.event === "watch_stopped") {
        setActive(false);
      }
    });
    const t = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => {
      es.close();
      window.clearInterval(t);
    };
  }, [instance]);

  const ago = last ? formatAgo(Date.now() / 1000 - last.at) : "never";
  return (
    <Card title="Watch mode" subtitle="dpm dar watch → live rebuild">
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 8,
          fontSize: 12,
          fontFamily: wMono,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span
            style={{
              padding: "2px 8px",
              borderRadius: 4,
              fontSize: 10.5,
              background: active ? "#62E2A022" : W.border,
              color: active ? "#62E2A0" : W.dim,
              fontWeight: 600,
              letterSpacing: 0.8,
              textTransform: "uppercase",
            }}
          >
            {active ? "Watching" : "Idle"}
          </span>
          {last && (
            <span style={{ color: W.dim, fontSize: 11 }}>{last.event}</span>
          )}
        </div>
        <Row k="last event" v={ago} vColor={last ? W.text : W.dim} />
        <Row k="detail" v={last?.detail ?? "—"} vColor={W.dim} />
        <div style={{ height: 1, background: W.border, margin: "4px 0" }} />
        <div style={{ color: W.dim, fontSize: 11.5, fontFamily: "inherit" }}>
          Start a watcher with:
          <pre
            style={{
              background: W.border,
              padding: "6px 8px",
              marginTop: 4,
              borderRadius: 4,
              fontFamily: wMono,
              fontSize: 11,
              color: W.text2,
              whiteSpace: "pre-wrap",
            }}
          >
            {`dpm localnet dar watch \\
  --instance ${instance} \\
  --publish-to http://127.0.0.1:7777`}
          </pre>
        </div>
      </div>
    </Card>
  );
}

// formatAgo renders a "X ago" label for a unix-second delta; bands
// finer than 5s read as noise on this card.
function formatAgo(deltaSec: number): string {
  if (deltaSec < 5) return "just now";
  if (deltaSec < 60) return `${Math.floor(deltaSec)}s ago`;
  if (deltaSec < 3600) return `${Math.floor(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.floor(deltaSec / 3600)}h ago`;
  return `${Math.floor(deltaSec / 86400)}d ago`;
}

// VetState is the per-row vetting cell state; undefined means not yet
// requested.
type VetState =
  | { kind: "loading" }
  | { kind: "ok"; rows: DARVettingRow[] }
  | { kind: "err" };

function PkgRow({
  row,
  active,
  vet,
  onClick,
}: {
  row: DARRow;
  active: boolean;
  vet: VetState | undefined;
  onClick: () => void;
}) {
  return (
    <div
      onClick={onClick}
      style={{
        display: "grid",
        gridTemplateColumns: "1.6fr 0.6fr 1fr 0.8fr",
        gap: 14,
        padding: "10px 14px",
        alignItems: "center",
        background: active ? `${W.brand}10` : "transparent",
        borderLeft: active ? `2px solid ${W.brand}` : "2px solid transparent",
        paddingLeft: active ? 12 : 14,
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
      }}
    >
      <code
        style={{
          fontFamily: wMono,
          color: W.text,
          fontSize: 12.5,
          fontWeight: active ? 600 : 500,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.name}
      >
        {row.name}
      </code>
      <code style={{ fontFamily: wMono, color: W.brand, fontSize: 11.5 }}>
        {row.version}
      </code>
      <code
        style={{
          fontFamily: wMono,
          color: W.dim,
          fontSize: 11,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.main}
      >
        {row.main.slice(0, 12)}…{row.main.slice(-6)}
      </code>
      <VettingCell vet={vet} />
    </div>
  );
}

// VettingCell renders per-participant vetting for one DAR as a compact
// "U P S" trio of dots — green vetted, grey unvetted, amber "?" when
// that participant couldn't be probed. Matches the CLI `dar list
// --vetting` column and the inspect-drawer toggles.
function VettingCell({ vet }: { vet: VetState | undefined }) {
  if (!vet || vet.kind === "loading") {
    return (
      <span style={{ fontSize: 11, color: W.dim, fontFamily: wMono }}>…</span>
    );
  }
  if (vet.kind === "err" || vet.rows.length === 0) {
    return (
      <span
        style={{ fontSize: 11, color: W.warn, fontFamily: wMono }}
        title="vetting state unavailable"
      >
        unknown
      </span>
    );
  }
  return (
    <span
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        fontFamily: wMono,
        fontSize: 10.5,
      }}
    >
      {vet.rows.map((r) => {
        const abbr =
          r.role === "app-user" ? "U" : r.role === "app-provider" ? "P" : "S";
        const color = r.error ? W.warn : r.vetted ? "#62E2A0" : W.dim;
        const title = r.error
          ? `${r.role}: ${r.error}`
          : `${r.role}: ${r.vetted ? "vetted" : "not vetted"}`;
        return (
          <span
            key={r.role}
            title={title}
            style={{ display: "flex", alignItems: "center", gap: 3, color }}
          >
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                background: color,
                flexShrink: 0,
              }}
            />
            {abbr}
            {r.error ? "?" : ""}
          </span>
        );
      })}
    </span>
  );
}

function InspectDrawer({
  row,
  instance,
  role,
  compareWith,
  onClearCompare,
  allRows,
  onCompare,
}: {
  row: DARRow | null;
  instance: string;
  role: Role;
  compareWith: string | null;
  onClearCompare: () => void;
  allRows: DARRow[];
  onCompare: (other: string) => void;
}) {
  if (!row) {
    return (
      <div
        style={{
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
          padding: 32,
          textAlign: "center",
          color: W.dim,
          fontSize: 13,
        }}
      >
        Select a package to inspect.
      </div>
    );
  }
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        overflow: "hidden",
      }}
    >
      <header style={{ padding: "14px 16px", borderBottom: `1px solid ${W.border}` }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span style={{ color: W.text, fontWeight: 600, fontSize: 14 }}>
            {row.name}
          </span>
          <span style={{ color: W.brand, fontWeight: 400, fontSize: 12, fontFamily: wMono }}>
            {row.version}
          </span>
        </div>
        {row.description && row.description !== `${row.name}-${row.version}` && (
          <div style={{ color: W.dim, fontSize: 12, marginTop: 4 }}>
            {row.description}
          </div>
        )}
      </header>
      <Section label="Identity">
        <KV label="pkg-id" value={row.main} mono color="#C4A8F5" />
        <KV label="name" value={row.name} mono />
        <KV label="version" value={row.version} mono />
        {row.description && (
          <KV label="desc" value={row.description} />
        )}
      </Section>
      <Section label="Per-participant vetting">
        <VettingPanel instance={instance} mainID={row.main} />
      </Section>
      <Section
        label={compareWith ? "Structural diff" : "Package contents"}
      >
        {compareWith ? (
          <>
            <button
              type="button"
              onClick={onClearCompare}
              style={smallBtn}
              aria-label="exit diff mode"
            >
              ← back to inspect
            </button>
            <div style={{ marginTop: 8 }}>
              <DARDiff instance={instance} a={row.main} b={compareWith} role={role} />
            </div>
          </>
        ) : (
          <>
            <DARPackageTree instance={instance} mainID={row.main} role={role} />
            <CompareSelector
              allRows={allRows}
              currentMain={row.main}
              onPick={onCompare}
            />
          </>
        )}
      </Section>
    </div>
  );
}

const smallBtn: React.CSSProperties = {
  background: "transparent",
  border: `1px solid ${W.border}`,
  color: W.text2,
  borderRadius: 5,
  padding: "3px 10px",
  fontSize: 11.5,
  fontFamily: wMono,
  cursor: "pointer",
};

// CompareSelector renders a small "compare with…" dropdown of every
// DAR currently visible in the list (excluding the active one).
// Picking a target flips the drawer into diff mode.
function CompareSelector({
  allRows,
  currentMain,
  onPick,
}: {
  allRows: DARRow[];
  currentMain: string;
  onPick: (main: string) => void;
}) {
  const others = allRows.filter((r) => r.main !== currentMain);
  if (others.length === 0) return null;
  return (
    <div style={{ marginTop: 12, display: "flex", alignItems: "center", gap: 8 }}>
      <span style={{ color: W.dim, fontSize: 11.5 }}>Compare with</span>
      <select
        onChange={(e) => {
          if (e.target.value) onPick(e.target.value);
        }}
        defaultValue=""
        style={{
          background: W.border,
          color: W.text,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          padding: "3px 6px",
          fontSize: 11.5,
          fontFamily: wMono,
        }}
        aria-label="compare current DAR with another"
      >
        <option value="" disabled>
          pick a DAR…
        </option>
        {others.map((o) => (
          <option key={o.main} value={o.main}>
            {o.name}@{o.version}
          </option>
        ))}
      </select>
    </div>
  );
}

// VettingPanel renders the per-participant vetting state for one
// DAR and lets the user toggle each. Loads on mount, refetches after
// every successful toggle so the UI never shows a stale "vetted=true"
// after an UnvetDar succeeded.
function VettingPanel({
  instance,
  mainID,
}: {
  instance: string;
  mainID: string;
}) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; rows: DARVettingRow[] }
    | { kind: "err"; msg: string }
  >({ kind: "loading" });
  const [pending, setPending] = useState<Role | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchDARVetting(instance, mainID)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", rows: data.participants });
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setState({
            kind: "err",
            msg: e instanceof Error ? e.message : "failed",
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [instance, mainID, tick]);

  async function flip(role: Role, vetted: boolean) {
    setPending(role);
    setError(null);
    try {
      await setDARVetting(instance, mainID, role, vetted);
      setTick((n) => n + 1);
    } catch (e) {
      if (e instanceof ApiError && e.code === "VETTING_UNSUPPORTED") {
        setError("This Splice/Canton version does not expose vetting toggles.");
      } else {
        setError(e instanceof Error ? e.message : "toggle failed");
      }
    } finally {
      setPending(null);
    }
  }

  if (state.kind === "loading") {
    return <div style={{ color: W.dim, fontSize: 12 }}>Loading vetting state…</div>;
  }
  if (state.kind === "err") {
    return <div style={{ color: W.err, fontSize: 12 }}>Vetting: {state.msg}</div>;
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      {state.rows.map((r) => (
        <div
          key={r.role}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            fontFamily: wMono,
            fontSize: 12,
          }}
        >
          <span style={{ width: 100, color: W.text2 }}>{r.role}</span>
          {r.error ? (
            <span style={{ color: W.warn, fontSize: 11.5 }}>{r.error}</span>
          ) : (
            <button
              type="button"
              role="switch"
              aria-checked={r.vetted}
              aria-label={`toggle vetting on ${r.role}`}
              disabled={pending === r.role}
              onClick={() => flip(r.role, !r.vetted)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                background: "transparent",
                border: "none",
                padding: 0,
                cursor: pending === r.role ? "wait" : "pointer",
                color: r.vetted ? "#62E2A0" : W.dim,
              }}
            >
              <span
                style={{
                  width: 22,
                  height: 12,
                  background: r.vetted ? "#62E2A0" : "#3A4248",
                  borderRadius: 6,
                  position: "relative",
                  flexShrink: 0,
                }}
              >
                <span
                  style={{
                    position: "absolute",
                    top: 2,
                    left: r.vetted ? 12 : 2,
                    width: 8,
                    height: 8,
                    borderRadius: "50%",
                    background: "#FFF",
                    transition: "left 120ms",
                  }}
                />
              </span>
              <span>{r.vetted ? "vetted" : "unvetted"}</span>
            </button>
          )}
        </div>
      ))}
      {error && (
        <div style={{ color: W.err, fontSize: 11.5, marginTop: 4 }}>{error}</div>
      )}
    </div>
  );
}

function UploadProgress({
  state,
}: {
  state: Extract<UploadState, { kind: "uploading" }>;
}) {
  const pct = Math.round(state.progress * 100);
  return (
    <div>
      <div
        style={{
          color: W.text,
          marginBottom: 6,
          fontFamily: wMono,
          fontSize: 12,
        }}
      >
        Uploading {state.filenames.length} file{state.filenames.length === 1 ? "" : "s"} ({pct}%)
      </div>
      <div
        style={{
          height: 6,
          background: W.border,
          borderRadius: 3,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            height: "100%",
            width: `${pct}%`,
            background: W.brand,
            transition: "width 0.18s",
          }}
        />
      </div>
    </div>
  );
}

// UploadResultBanner renders the per-participant outcome of a
// multi-target upload. Partial failures still return 200 from the
// backend, so they land here (not the error banner) and the user sees
// what landed and what didn't.
function UploadResultBanner({
  kind,
  total,
  results,
}: {
  kind: "success" | "partial";
  total: number;
  results: DARUploadRoleResult[];
}) {
  const okCount = results.filter((r) => r.ok).length;
  const accent = kind === "success" ? W.brand : W.warn;
  const heading =
    kind === "success"
      ? `✓ Uploaded ${total} package${total === 1 ? "" : "s"} to ${okCount} participant${okCount === 1 ? "" : "s"}. Refreshing list…`
      : `⚠ Partial upload — ${okCount}/${results.length} participant${results.length === 1 ? "" : "s"} succeeded`;
  return (
    <div
      role={kind === "success" ? "status" : "alert"}
      style={{
        background: `${accent}10`,
        border: `1px solid ${accent}`,
        borderRadius: 6,
        padding: "8px 12px",
        fontSize: 12,
        color: W.text2,
      }}
    >
      <div style={{ color: accent, fontWeight: 600, marginBottom: results.length > 0 ? 6 : 0 }}>
        {heading}
      </div>
      {results.map((r) => (
        <div
          key={r.role}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontFamily: wMono,
            fontSize: 11,
            marginTop: 3,
            color: r.ok ? W.text2 : W.err,
          }}
        >
          <span style={{ color: r.ok ? W.brand : W.err, width: 12 }}>
            {r.ok ? "✓" : "✗"}
          </span>
          <span style={{ width: 110 }}>{r.role}</span>
          <span>
            {r.ok
              ? `${r.count} package${r.count === 1 ? "" : "s"}`
              : (r.error ?? "failed")}
          </span>
        </div>
      ))}
    </div>
  );
}

function ErrorBanner({ msg }: { msg: string }) {
  return (
    <div
      role="alert"
      style={{
        background: `${W.err}10`,
        border: `1px solid ${W.err}`,
        borderRadius: 6,
        padding: "8px 12px",
        fontSize: 12,
        color: W.err,
      }}
    >
      Upload failed: {msg}
    </div>
  );
}

function RoleSwitcher({
  role,
  onChange,
}: {
  role: Role;
  onChange: (r: Role) => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        gap: 4,
        padding: 3,
        background: W.border,
        borderRadius: 9,
      }}
    >
      {ROLES.map((r) => {
        const active = r === role;
        return (
          <button
            key={r}
            onClick={() => onChange(r)}
            style={{
              background: active ? W.surface : "transparent",
              color: active ? W.text : W.dim,
              border: "none",
              borderRadius: 6,
              padding: "5px 12px",
              fontSize: 12,
              fontFamily: wMono,
              fontWeight: active ? 600 : 500,
              cursor: active ? "default" : "pointer",
              boxShadow: active ? `0 0 0 1px ${W.brand}` : "none",
            }}
          >
            {r}
          </button>
        );
      })}
    </div>
  );
}

function VetToggle({
  on,
  label,
  onChange,
}: {
  on: boolean;
  label: string;
  onChange: (next: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={`vet on upload — ${label}`}
      onClick={() => onChange(!on)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 9,
        padding: "5px 8px",
        background: W.border,
        border: "none",
        borderRadius: 6,
        fontSize: 12,
        cursor: "pointer",
        width: "100%",
        textAlign: "left",
        outline: "none",
      }}
    >
      <span
        style={{
          width: 24,
          height: 14,
          background: on ? W.brand : "#3A4248",
          borderRadius: 7,
          position: "relative",
          flexShrink: 0,
          transition: "background 120ms",
        }}
      >
        <span
          style={{
            position: "absolute",
            top: 2,
            left: on ? 12 : 2,
            width: 10,
            height: 10,
            borderRadius: "50%",
            background: "#FFF",
            transition: "left 120ms",
          }}
        />
      </span>
      <span style={{ color: on ? W.text : W.dim }}>{label}</span>
    </button>
  );
}

function FilterBtn({
  active,
  children,
  onClick,
}: {
  active: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: "4px 10px",
        fontSize: 11.5,
        borderRadius: 5,
        border: `1px solid ${active ? W.brand : W.border}`,
        background: active ? `${W.brand}1A` : "transparent",
        color: active ? W.brand : W.dim,
        cursor: "pointer",
        fontFamily: wMono,
        fontWeight: active ? 600 : 500,
      }}
    >
      {children}
    </button>
  );
}

// ─── Tiny shared primitives ─────────────────────────────────

function Card({
  title,
  subtitle,
  children,
}: {
  title?: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        overflow: "hidden",
      }}
    >
      {title && (
        <div
          style={{
            padding: "11px 14px",
            borderBottom: `1px solid ${W.border}`,
          }}
        >
          <div style={{ color: W.text, fontSize: 12.5, fontWeight: 600 }}>
            {title}
          </div>
          {subtitle && (
            <div style={{ color: W.dim, fontSize: 11, marginTop: 1 }}>
              {subtitle}
            </div>
          )}
        </div>
      )}
      <div style={{ padding: title ? 14 : 0 }}>{children}</div>
    </div>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{ padding: "12px 16px", borderBottom: `1px solid ${W.border}` }}>
      <SectionLabel>{label}</SectionLabel>
      <div style={{ marginTop: 8 }}>{children}</div>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        color: W.dim,
        fontSize: 10.5,
        letterSpacing: 1.4,
        textTransform: "uppercase",
        fontWeight: 600,
      }}
    >
      {children}
    </div>
  );
}

function KV({
  label,
  value,
  mono,
  color,
}: {
  label: string;
  value: string;
  mono?: boolean;
  color?: string;
}) {
  return (
    <div
      style={{ display: "grid", gridTemplateColumns: "70px 1fr", gap: 8, marginBottom: 4 }}
    >
      <span style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>{label}</span>
      <span
        style={{
          color: color ?? W.text2,
          fontSize: mono ? 11 : 12,
          fontFamily: mono ? wMono : undefined,
          wordBreak: "break-all",
        }}
      >
        {value}
      </span>
    </div>
  );
}

function Row({
  k,
  v,
  vColor,
}: {
  k: string;
  v: string;
  vColor?: string;
}) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between" }}>
      <span style={{ color: W.dim }}>{k}</span>
      <span style={{ color: vColor ?? W.text }}>{v}</span>
    </div>
  );
}

function Status({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 16,
        color: W.dim,
        fontSize: 13,
      }}
    >
      {children}
    </div>
  );
}

function ErrorPanel({ msg }: { msg: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 16,
        color: W.err,
        fontSize: 13,
      }}
    >
      {msg}
    </div>
  );
}

function EmptyPanel({
  title,
  remediation,
}: {
  title: string;
  remediation: string;
}) {
  return (
    <div
      style={{
        background: `${W.warn}10`,
        border: `1px solid ${W.warn}`,
        borderRadius: 10,
        padding: 20,
      }}
    >
      <h3 style={{ color: W.warn, fontSize: 14, marginTop: 0, marginBottom: 8 }}>
        {title}
      </h3>
      <p style={{ color: W.text2, fontSize: 13 }}>{remediation}</p>
    </div>
  );
}
