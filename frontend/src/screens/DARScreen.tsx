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
import { W, wMono, tableCaps, R, tint, fs } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import {
  Dot,
  IcAlert,
  IcArrowRight,
  IcCheck,
  IcUpload,
  IcX,
} from "../components/icons";
import { DARPackageTree } from "./DARPackageTree";
import { DARDiff } from "./DARDiff";

// Three-column DAR manager: upload + vetting + watch (left), package
// list (middle), inspect tree / structural diff (right). Vetting is
// live per-participant end-to-end.

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
  // Participants an upload fans out to (parallel, backend-side).
  // Orthogonal to `role`, which drives only the package LIST.
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
  // Separate from selectedHash so toggling the comparison off keeps the
  // primary selection.
  const [compareHash, setCompareHash] = useState<string | null>(null);
  const [upload, setUpload] = useState<UploadState>({ kind: "idle" });
  const [dragOver, setDragOver] = useState(false);
  const [filter, setFilter] = useState<"all" | "app">("all");
  const [tick, setTick] = useState(0); // bump to refetch after upload
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
    // Mirrors the backend multipart cap (darUploadMax, dar.go).
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
      list = list.filter(
        (d) =>
          !d.name.startsWith("canton-builtin-") &&
          !d.name.startsWith("splice-") &&
          !d.name.startsWith("daml-"),
      );
    }
    return list;
  }, [state, filter]);

  useEffect(() => {
    setVetting({});
  }, [name, tick]);

  // Lazily fetch per-participant vetting for each visible row; rows are
  // marked "loading" in one batch so re-renders never double-fetch.
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
    // vetting read via functional updater to keep it out of the deps (would loop).
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
          <h2 style={{ color: W.text, fontSize: fs.h3, margin: 0 }}>
            DAR Manager
          </h2>
          <div style={{ color: W.dim, fontSize: fs.small, marginTop: 3 }}>
            {state.kind === "ok"
              ? `${state.data.dars.length} packages on ${role} participant`
              : "loading…"}
          </div>
        </div>
        <RoleSwitcher role={role} onChange={setRole} />
      </header>

      {state.kind === "loading" && <DARListLoading />}
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
                  border: `1px dashed ${dragOver ? W.brand : tint(W.brand, 33)}`,
                  borderRadius: R.control,
                  padding: "22px 16px",
                  textAlign: "center",
                  background: dragOver ? tint(W.brand, 10) : "transparent",
                  cursor: "pointer",
                  transition: "background-color 120ms, border-color 120ms",
                }}
              >
                {upload.kind === "uploading" ? (
                  <UploadProgress state={upload} />
                ) : (
                  <>
                    <div
                      style={{
                        color: W.brand,
                        marginBottom: 8,
                        display: "flex",
                        justifyContent: "center",
                      }}
                    >
                      <IcUpload size={22} />
                    </div>
                    <div
                      style={{
                        fontWeight: 600,
                        marginBottom: 3,
                        fontSize: fs.body,
                        color: W.text,
                      }}
                    >
                      Drop DAR here
                    </div>
                    <div style={{ color: W.dim, fontSize: fs.small }}>
                      or click to browse · multiple .dar accepted
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
                    borderRadius: 2,
                    fontSize: fs.small,
                    color: selectedRoles.length === 0 ? W.warn : W.text2,
                    lineHeight: 1.5,
                  }}
                >
                  {selectedRoles.length === 0 ? (
                    <>
                      <span
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: 6,
                          color: W.warn,
                        }}
                      >
                        <IcAlert size={12} /> Pick at least one target.
                      </span>{" "}
                      Drops will be refused until a participant is selected.
                    </>
                  ) : (
                    <>
                      Each dropped DAR
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

          <div
            style={{
              background: W.surface,
              border: `1px solid ${W.border}`,
              borderRadius: 4,
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
                  style={{ color: W.text, fontSize: fs.body, fontWeight: 600 }}
                >
                  Packages on {role} participant
                </div>
                <div style={{ color: W.dim, fontSize: fs.small, marginTop: 2 }}>
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

            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1.6fr 0.6fr 1fr 0.8fr",
                gap: 14,
                padding: "9px 14px",
                color: W.dim,
                fontSize: fs.caption,
                ...tableCaps,
                borderBottom: `1px solid ${W.border}`,
              }}
            >
              <span>Package</span>
              <span>Version</span>
              <span>Package-id</span>
              <span>Vetting</span>
            </div>

            {rows.length === 0 && (
              <div style={{ padding: 18, color: W.dim, fontSize: fs.small }}>
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
                fontSize: fs.small,
                display: "flex",
                justifyContent: "space-between",
                borderTop: `1px solid ${W.border}`,
              }}
            >
              <span>{rows.length} packages</span>
              <span>↑↓ navigate · ↵ inspect · esc close</span>
            </div>
          </div>

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

// Renders the latest DAR-watch SSE event as a "Watching"/"Idle" badge.
function WatchModeCard({ instance }: { instance: string }) {
  const [last, setLast] = useState<DARWatchEvent | null>(null);
  const [active, setActive] = useState(false);
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
          fontSize: fs.small,
          fontFamily: wMono,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              padding: "2px 8px",
              borderRadius: R.control,
              fontSize: fs.small,
              background: active ? tint(W.ok, 13) : W.border,
              color: active ? W.ok : W.dim,
              fontWeight: 500,
            }}
          >
            <Dot color={active ? W.ok : W.dim} size={6} pulse={active} />
            {active ? "Watching" : "Idle"}
          </span>
          {last && (
            <span style={{ color: W.dim, fontSize: fs.small }}>{last.event}</span>
          )}
        </div>
        <Row k="last event" v={ago} vColor={last ? W.text : W.dim} />
        <Row k="detail" v={last?.detail ?? "—"} vColor={W.dim} />
        <div style={{ height: 1, background: W.border, margin: "4px 0" }} />
        <div style={{ color: W.dim, fontSize: fs.small, fontFamily: "inherit" }}>
          Start a watcher with:
          <pre
            style={{
              background: W.border,
              padding: "6px 8px",
              marginTop: 4,
              borderRadius: 2,
              fontFamily: wMono,
              fontSize: fs.small,
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

function formatAgo(deltaSec: number): string {
  if (deltaSec < 5) return "just now";
  if (deltaSec < 60) return `${Math.floor(deltaSec)}s ago`;
  if (deltaSec < 3600) return `${Math.floor(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.floor(deltaSec / 3600)}h ago`;
  return `${Math.floor(deltaSec / 86400)}d ago`;
}

// undefined means not yet requested.
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
        background: active ? tint(W.brand, 12) : "transparent",
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
        transition: "background-color 120ms",
      }}
    >
      <code
        style={{
          fontFamily: wMono,
          color: W.text,
          fontSize: fs.small,
          fontWeight: active ? 600 : 500,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.name}
      >
        {row.name}
      </code>
      <code
        style={{
          fontFamily: wMono,
          color: W.brand,
          fontSize: fs.small,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {row.version}
      </code>
      <MonoId value={row.main} head={12} tail={6} size={fs.small} color={W.dim} />
      <VettingCell vet={vet} />
    </div>
  );
}

// Per-participant vetting as a "U P S" dot trio: green vetted, grey
// unvetted, amber "?" when a participant couldn't be probed.
function VettingCell({ vet }: { vet: VetState | undefined }) {
  if (!vet || vet.kind === "loading") {
    return (
      <span style={{ fontSize: fs.small, color: W.dim, fontFamily: wMono }}>…</span>
    );
  }
  if (vet.kind === "err" || vet.rows.length === 0) {
    return (
      <span
        style={{ fontSize: fs.small, color: W.warn, fontFamily: wMono }}
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
        fontSize: fs.caption,
      }}
    >
      {vet.rows.map((r) => {
        const abbr =
          r.role === "app-user" ? "U" : r.role === "app-provider" ? "P" : "S";
        const color = r.error ? W.warn : r.vetted ? W.ok : W.dim;
        const title = r.error
          ? `${r.role}: ${r.error}`
          : `${r.role}: ${r.vetted ? "vetted" : "not vetted"}`;
        return (
          <span
            key={r.role}
            title={title}
            style={{ display: "flex", alignItems: "center", gap: 3, color }}
          >
            <Dot color={color} size={6} />
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
          borderRadius: R.card,
          padding: 14,
          textAlign: "left",
          color: W.dim,
          fontSize: fs.body,
          lineHeight: 1.5,
        }}
      >
        Select a package to inspect its tree, per-participant vetting, and
        structural diff.
      </div>
    );
  }
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        overflow: "hidden",
      }}
    >
      <header style={{ padding: "14px 16px", borderBottom: `1px solid ${W.border}` }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span style={{ color: W.text, fontWeight: 600, fontSize: fs.body }}>
            {row.name}
          </span>
          <span style={{ color: W.brand, fontWeight: 400, fontSize: fs.small, fontFamily: wMono }}>
            {row.version}
          </span>
        </div>
        {row.description && row.description !== `${row.name}-${row.version}` && (
          <div style={{ color: W.dim, fontSize: fs.small, marginTop: 4 }}>
            {row.description}
          </div>
        )}
      </header>
      <Section label="Identity">
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "70px 1fr",
            gap: 12,
            padding: "4px 0",
            alignItems: "center",
          }}
        >
          <span style={{ color: W.dim, fontSize: fs.small }}>pkg-id</span>
          <MonoId value={row.main} head={10} tail={8} size={fs.small} color={W.mag} />
        </div>
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
            <Button
              variant="ghost"
              size="sm"
              onClick={onClearCompare}
              aria-label="exit diff mode"
              icon={<IcArrowRight style={{ transform: "rotate(180deg)" }} />}
            >
              back to inspect
            </Button>
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

// "compare with…" dropdown; picking a target flips the drawer to diff mode.
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
      <span style={{ color: W.dim, fontSize: fs.small }}>Compare with</span>
      <select
        onChange={(e) => {
          if (e.target.value) onPick(e.target.value);
        }}
        defaultValue=""
        style={{
          background: W.border,
          color: W.text,
          border: `1px solid ${W.border}`,
          borderRadius: 2,
          padding: "3px 6px",
          fontSize: fs.small,
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

// Per-participant vetting toggles; refetches after each successful
// toggle so state never goes stale.
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
    return <div style={{ color: W.dim, fontSize: fs.small }}>Loading vetting state…</div>;
  }
  if (state.kind === "err") {
    return <div style={{ color: W.err, fontSize: fs.small }}>Vetting: {state.msg}</div>;
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
            fontSize: fs.small,
          }}
        >
          <span style={{ width: 100, color: W.text2 }}>{r.role}</span>
          {r.error ? (
            <span style={{ color: W.warn, fontSize: fs.small }}>{r.error}</span>
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
                color: r.vetted ? W.ok : W.dim,
              }}
            >
              <span
                style={{
                  width: 22,
                  height: 12,
                  background: r.vetted ? W.ok : W.borderHi,
                  borderRadius: 999,
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
        <div style={{ color: W.err, fontSize: fs.small, marginTop: 4 }}>{error}</div>
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
          fontSize: fs.small,
        }}
      >
        Uploading {state.filenames.length} file{state.filenames.length === 1 ? "" : "s"} ({pct}%)
      </div>
      <div
        style={{
          height: 6,
          background: W.border,
          borderRadius: 2,
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

// Per-participant outcome of a multi-target upload. Partial failures
// still return 200, so they land here, not the error banner.
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
      ? `Uploaded ${total} package${total === 1 ? "" : "s"} to ${okCount} participant${okCount === 1 ? "" : "s"}. Refreshing list…`
      : `Partial upload. ${okCount}/${results.length} participant${results.length === 1 ? "" : "s"} succeeded.`;
  return (
    <div
      role={kind === "success" ? "status" : "alert"}
      style={{
        background: tint(accent, 10),
        border: `1px solid ${accent}`,
        borderRadius: R.control,
        padding: "8px 12px",
        fontSize: fs.small,
        color: W.text2,
      }}
    >
      <div
        style={{
          color: accent,
          fontWeight: 600,
          marginBottom: results.length > 0 ? 6 : 0,
          display: "flex",
          alignItems: "center",
          gap: 6,
        }}
      >
        {kind === "success" ? <IcCheck size={12} /> : <IcAlert size={12} />}
        <span>{heading}</span>
      </div>
      {results.map((r) => (
        <div
          key={r.role}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontFamily: wMono,
            fontSize: fs.small,
            marginTop: 3,
            color: r.ok ? W.text2 : W.err,
          }}
        >
          <span
            style={{
              color: r.ok ? W.brand : W.err,
              width: 12,
              display: "inline-flex",
              alignItems: "center",
            }}
          >
            {r.ok ? <IcCheck size={12} /> : <IcX size={12} />}
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
        background: `${tint(W.err, 6)}`,
        border: `1px solid ${W.err}`,
        borderRadius: 2,
        padding: "8px 12px",
        fontSize: fs.small,
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
        borderRadius: 2,
      }}
    >
      {ROLES.map((r) => {
        const active = r === role;
        return (
          <button
            key={r}
            onClick={() => onChange(r)}
            style={{
              background: active ? tint(W.brand, 16) : "transparent",
              color: active ? W.brand : W.dim,
              border: "none",
              borderRadius: R.control,
              padding: "5px 12px",
              fontSize: fs.small,
              fontFamily: wMono,
              fontWeight: active ? 600 : 500,
              cursor: active ? "default" : "pointer",
              transition: "background-color 120ms",
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
        borderRadius: 2,
        fontSize: fs.small,
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
          background: on ? W.brand : W.borderHi,
          borderRadius: 999,
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
        fontSize: fs.small,
        borderRadius: 2,
        border: `1px solid ${active ? W.brand : W.border}`,
        background: active ? `${tint(W.brand, 10)}` : "transparent",
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
        borderRadius: 4,
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
          <div style={{ color: W.text, fontSize: fs.small, fontWeight: 600 }}>
            {title}
          </div>
          {subtitle && (
            <div style={{ color: W.dim, fontSize: fs.small, marginTop: 1 }}>
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
        fontSize: fs.caption,
        ...tableCaps,
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
      style={{
        display: "grid",
        gridTemplateColumns: "70px 1fr",
        gap: 12,
        padding: "4px 0",
      }}
    >
      <span style={{ color: W.dim, fontSize: fs.small }}>{label}</span>
      <span
        style={{
          color: color ?? W.text2,
          fontSize: fs.small,
          fontFamily: mono ? wMono : undefined,
          fontVariantNumeric: mono ? "tabular-nums" : undefined,
          wordBreak: "break-word",
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
    <div
      style={{
        display: "flex",
        justifyContent: "space-between",
        gap: 12,
        padding: "4px 0",
      }}
    >
      <span style={{ color: W.dim, fontSize: fs.small }}>{k}</span>
      <span style={{ color: vColor ?? W.text }}>{v}</span>
    </div>
  );
}

// Package-list skeleton, gated so a fast local fetch never flashes it.
function DARListLoading() {
  const show = useLoadingDelay(true);
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "11px 14px",
          borderBottom: `1px solid ${W.border}`,
          color: W.dim,
          fontSize: fs.small,
        }}
      >
        Loading package list
      </div>
      {show ? (
        <SkeletonTable
          columns={[1.6, 0.6, 1, 0.8]}
          rows={5}
          rowHeight={40}
          label="Loading package list"
        />
      ) : (
        <div style={{ height: 200 }} />
      )}
    </div>
  );
}

function ErrorPanel({ msg }: { msg: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 16,
        color: W.err,
        fontSize: fs.body,
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
        background: `${tint(W.warn, 6)}`,
        border: `1px solid ${W.warn}`,
        borderRadius: 4,
        padding: 20,
      }}
    >
      <h3 style={{ color: W.warn, fontSize: fs.body, marginTop: 0, marginBottom: 8 }}>
        {title}
      </h3>
      <p style={{ color: W.text2, fontSize: fs.body }}>{remediation}</p>
    </div>
  );
}
