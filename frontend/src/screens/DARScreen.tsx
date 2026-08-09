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
import { W, wMono, tableCaps, wideCaps, R, tint, fs } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";
import { SkeletonTable, useLoadingDelay } from "../components/Skeleton";
import {
  Dot,
  IcAlert,
  IcArrowRight,
  IcCheck,
  IcExplorer,
  IcRefresh,
  IcUpload,
  IcX,
} from "../components/icons";
import { DARPackageTree } from "./DARPackageTree";
import { DARDiff } from "./DARDiff";

// DAR manager: a horizontal upload bar, a package table (with an
// inline filter/search/refresh header and a watch-mode footer), and a
// right detail rail that only exists once a package is selected.
// Vetting is live per-participant end-to-end.

// Default (app-provider) first — matches CLI dar --role and handler
// empty-role fallback.
const ROLES: Role[] = ["app-provider", "app-user", "sv"];

// Package table column template — shared by the header band and rows.
const COLS = "1.6fr 0.55fr 1.1fr 1.15fr";

// Built-in packages the app-DARs filter hides (Canton/Splice/Daml SDK).
function isBuiltin(name: string): boolean {
  return (
    name.startsWith("canton-builtin-") ||
    name.startsWith("splice-") ||
    name.startsWith("daml-")
  );
}

type UploadState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filenames: string[] }
  | { kind: "success"; total: number; results: DARUploadRoleResult[] }
  | { kind: "partial"; total: number; results: DARUploadRoleResult[] }
  | { kind: "error"; message: string };

export function DARScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-provider");
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
  // Default to app DARs — the SDK built-ins are noise for most flows.
  const [filter, setFilter] = useState<"all" | "app">("app");
  const [search, setSearch] = useState("");
  const [tick, setTick] = useState(0); // bump to refetch (after upload / manual refresh)
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
    if (filter === "app") list = list.filter((d) => !isBuiltin(d.name));
    const q = search.trim().toLowerCase();
    if (q) {
      list = list.filter(
        (d) =>
          d.name.toLowerCase().includes(q) ||
          d.main.toLowerCase().includes(q),
      );
    }
    return list;
  }, [state, filter, search]);

  // Subtitle counts describe the whole listing, independent of the tab.
  const builtinHidden =
    state.kind === "ok"
      ? state.data.dars.filter((d) => isBuiltin(d.name)).length
      : 0;
  const uploadedCount =
    state.kind === "ok" ? state.data.dars.length - builtinHidden : 0;

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

  // Switching the selected package drops any stale comparison so the
  // new drawer opens on the inspect view, not a leftover diff.
  useEffect(() => {
    setCompareHash(null);
  }, [selectedHash]);

  // Escape closes the detail rail (and any active comparison).
  useEffect(() => {
    if (!selectedHash) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setSelectedHash(null);
        setCompareHash(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedHash]);

  function closeDrawer() {
    setSelectedHash(null);
    setCompareHash(null);
  }

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
    <section
      style={{
        padding: 24,
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          gap: 16,
          flexWrap: "wrap",
        }}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <h1 style={{ color: W.text, fontSize: fs.title, fontWeight: 600, margin: 0 }}>
            Packages
          </h1>
          <div style={{ color: W.dim, fontSize: fs.meta, fontFamily: wMono }}>
            {state.kind === "ok"
              ? `${uploadedCount} uploaded on ${name} · ${builtinHidden} built-in hidden`
              : state.kind === "loading"
                ? "loading…"
                : state.kind === "port-missing"
                  ? `participant ports unavailable on ${name}`
                  : `could not load packages on ${name}`}
          </div>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ color: W.dim, fontSize: fs.meta }}>Viewing</span>
          <Segmented
            variant="solid"
            ariaLabel="Viewing participant"
            value={role}
            onChange={(v) => setRole(v as Role)}
            options={ROLES.map((r) => ({ label: r, value: r }))}
          />
        </div>
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
        <>
          <UploadBar
            dragOver={dragOver}
            uploading={upload.kind === "uploading" ? upload : null}
            vetTargets={vetTargets}
            selectedCount={selectedRoles.length}
            onToggleTarget={(r, v) =>
              setVetTargets((s) => ({ ...s, [r]: v }))
            }
            onBrowse={() => fileInputRef.current?.click()}
            onDragOver={() => setDragOver(true)}
            onDragLeave={() => setDragOver(false)}
            onDrop={(files) => {
              setDragOver(false);
              void doUpload(files);
            }}
          />
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

          {(upload.kind === "success" || upload.kind === "partial") && (
            <UploadResultBanner
              kind={upload.kind}
              total={upload.total}
              results={upload.results}
            />
          )}
          {upload.kind === "error" && <ErrorBanner msg={upload.message} />}

          <div
            style={{
              display: "grid",
              gridTemplateColumns: selected ? "1fr 380px" : "1fr",
              gap: 14,
              alignItems: "start",
            }}
          >
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
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  padding: "10px 14px",
                  borderBottom: `1px solid ${W.border}`,
                  flexWrap: "wrap",
                }}
              >
                <Segmented
                  variant="outline"
                  ariaLabel="Package filter"
                  value={filter}
                  onChange={(v) => setFilter(v as "all" | "app")}
                  options={[
                    { label: "App DARs", value: "app" },
                    { label: "All packages", value: "all" },
                  ]}
                />
                <label
                  style={{
                    marginLeft: "auto",
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 8,
                    height: 28,
                    padding: "0 10px",
                    border: `1px solid ${W.border}`,
                    borderRadius: R.control,
                    color: W.dim,
                    minWidth: 200,
                    background: W.surface,
                  }}
                >
                  <IcExplorer size={13} />
                  <input
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder="Filter by name or id"
                    aria-label="Filter packages by name or id"
                    style={{
                      border: "none",
                      background: "transparent",
                      outline: "none",
                      color: W.text,
                      fontSize: fs.meta,
                      fontFamily: "inherit",
                      width: "100%",
                    }}
                  />
                </label>
                <button
                  type="button"
                  onClick={() => setTick((n) => n + 1)}
                  aria-label="Refresh package list"
                  title="Refresh"
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    width: 28,
                    height: 28,
                    border: `1px solid ${W.border}`,
                    borderRadius: R.control,
                    background: W.surface,
                    color: W.text2,
                    cursor: "pointer",
                  }}
                >
                  <IcRefresh size={13} />
                </button>
              </div>

              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: COLS,
                  gap: 14,
                  padding: "7px 14px",
                  background: W.surface2,
                  borderBottom: `1px solid ${W.border}`,
                  color: W.dim,
                  fontSize: fs.label,
                  ...tableCaps,
                }}
              >
                <span>Package</span>
                <span>Version</span>
                <span>Package-id</span>
                <span>Vetted on</span>
              </div>

              {rows.length === 0 && (
                <div style={{ padding: 18, color: W.dim, fontSize: fs.body }}>
                  {search.trim()
                    ? "No packages match your filter."
                    : "No packages match the current filter."}
                </div>
              )}
              <div style={{ maxHeight: "55vh", overflowY: "auto" }}>
                {rows.map((d) => (
                  <PkgRow
                    key={d.main}
                    row={d}
                    active={d.main === selectedHash}
                    vet={vetting[d.main]}
                    onSelect={() => setSelectedHash(d.main)}
                  />
                ))}
              </div>

              <WatchFooter instance={name} />
            </div>

            {selected && (
              <InspectDrawer
                key={selected.main}
                row={selected}
                instance={name}
                role={role}
                allRows={rows}
                compareWith={compareHash}
                onCompare={(other) => setCompareHash(other)}
                onClearCompare={() => setCompareHash(null)}
                onClose={closeDrawer}
              />
            )}
          </div>
        </>
      )}
    </section>
  );
}

// ── Upload bar ──────────────────────────────────────────
// Collapsed dropzone: dropping (or browsing) IS the action, so there's
// no separate submit button. Per-participant "Vet on upload" checkboxes
// sit inline; the whole bar is a drop target.
function UploadBar({
  dragOver,
  uploading,
  vetTargets,
  selectedCount,
  onToggleTarget,
  onBrowse,
  onDragOver,
  onDragLeave,
  onDrop,
}: {
  dragOver: boolean;
  uploading: Extract<UploadState, { kind: "uploading" }> | null;
  vetTargets: Record<Role, boolean>;
  selectedCount: number;
  onToggleTarget: (r: Role, v: boolean) => void;
  onBrowse: () => void;
  onDragOver: () => void;
  onDragLeave: () => void;
  onDrop: (files: FileList) => void;
}) {
  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        onDragOver();
      }}
      onDragLeave={onDragLeave}
      onDrop={(e) => {
        e.preventDefault();
        onDrop(e.dataTransfer.files);
      }}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 16,
        flexWrap: "wrap",
        padding: "12px 14px",
        background: dragOver ? tint(W.brand, 8) : W.surface,
        border: `1px dashed ${dragOver ? W.brand : W.borderHi}`,
        borderRadius: R.card,
        transition: "background-color 120ms, border-color 120ms",
      }}
    >
      {uploading ? (
        <div style={{ flex: 1, minWidth: 200 }}>
          <UploadProgress state={uploading} />
        </div>
      ) : (
        <>
          <button
            type="button"
            onClick={onBrowse}
            aria-label="Drop .dar files here or browse"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 10,
              background: "transparent",
              border: "none",
              padding: 0,
              cursor: "pointer",
              color: W.text,
              textAlign: "left",
            }}
          >
            <span style={{ color: W.brand, display: "inline-flex" }}>
              <IcUpload size={17} />
            </span>
            <span style={{ fontSize: fs.data, fontWeight: 600, color: W.text }}>
              Drop .dar files here
            </span>
            <span style={{ fontSize: fs.meta, color: W.dim }}>
              or{" "}
              <span style={{ color: W.brandText, fontWeight: 500 }}>browse</span>{" "}
              · 64 MB per file
            </span>
          </button>
          <span
            aria-hidden
            style={{ width: 1, height: 24, background: W.border }}
          />
          <span style={{ fontSize: fs.meta, color: W.dim }}>Vet on upload</span>
          <div style={{ display: "flex", gap: 14, alignItems: "center" }}>
            {(["sv", "app-provider", "app-user"] as Role[]).map((r) => (
              <VetCheckbox
                key={r}
                on={vetTargets[r]}
                label={r}
                onChange={(v) => onToggleTarget(r, v)}
              />
            ))}
          </div>
          {selectedCount === 0 ? (
            <span
              style={{
                marginLeft: "auto",
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
                color: W.warn,
                fontSize: fs.label,
              }}
            >
              <IcAlert size={12} /> pick at least one target
            </span>
          ) : (
            <span
              style={{
                marginLeft: "auto",
                fontFamily: wMono,
                fontSize: fs.label,
                color: W.faint,
              }}
            >
              uploads in parallel · vet_all_packages=true
            </span>
          )}
        </>
      )}
    </div>
  );
}

function VetCheckbox({
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
      role="checkbox"
      aria-checked={on}
      aria-label={`vet on upload — ${label}`}
      onClick={() => onChange(!on)}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 7,
        background: "transparent",
        border: "none",
        padding: 0,
        cursor: "pointer",
        fontFamily: wMono,
        fontSize: fs.meta,
        color: W.text,
      }}
    >
      <span
        aria-hidden
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 14,
          height: 14,
          borderRadius: R.control,
          background: on ? W.accentSolid : "transparent",
          border: `1px solid ${on ? W.accentSolid : W.borderHi}`,
        }}
      >
        {on && <IcCheck size={10} style={{ color: W.onAccentSolid }} />}
      </span>
      {label}
    </button>
  );
}

// ── Watch-mode footer ───────────────────────────────────
// Reflects the live dar-watch SSE state (there is no start/stop API —
// the watcher is a CLI process, so this is a status indicator, not a
// control). Shows the command to start one.
function WatchFooter({ instance }: { instance: string }) {
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

  const ago = last ? formatAgo(Date.now() / 1000 - last.at) : null;
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        flexWrap: "wrap",
        padding: "10px 14px",
        background: W.sunken,
        borderTop: `1px solid ${W.border}`,
      }}
    >
      <span
        title={
          active
            ? "A dar watcher is running for this instance"
            : "Idle — start a watcher with the command shown"
        }
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          fontSize: fs.meta,
          color: W.text2,
        }}
      >
        <span
          aria-hidden
          style={{
            width: 26,
            height: 15,
            borderRadius: 999,
            background: active ? W.ok : W.borderHi,
            display: "inline-flex",
            alignItems: "center",
            padding: 2,
            transition: "background 120ms",
          }}
        >
          <span
            style={{
              width: 11,
              height: 11,
              borderRadius: "50%",
              background: "#FFF",
              transform: active ? "translateX(11px)" : "translateX(0)",
              transition: "transform 120ms",
            }}
          />
        </span>
        <span style={{ fontWeight: 600, color: active ? W.ok : W.text2 }}>
          {active ? "Watching" : "Idle"}
        </span>
        <span style={{ color: W.dim }}>Watch mode</span>
      </span>
      <span style={{ fontSize: fs.meta, color: W.dim }}>
        rebuild and re-upload when a .daml file changes
      </span>
      {last && ago && (
        <span
          style={{ fontFamily: wMono, fontSize: fs.label, color: W.faint }}
        >
          {last.event} · {ago}
        </span>
      )}
      <span
        style={{
          marginLeft: "auto",
          fontFamily: wMono,
          fontSize: fs.label,
          color: W.faint,
        }}
      >
        dpm localnet dar watch --instance {instance}
      </span>
    </div>
  );
}

function formatAgo(deltaSec: number): string {
  if (deltaSec < 5) return "just now";
  if (deltaSec < 60) return `${Math.floor(deltaSec)}s ago`;
  if (deltaSec < 3600) return `${Math.floor(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.floor(deltaSec / 3600)}h ago`;
  return `${Math.floor(deltaSec / 86400)}d ago`;
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(i > 0 && v < 10 ? 1 : 0)} ${units[i]}`;
}

// undefined means not yet requested.
type VetState =
  | { kind: "loading" }
  | { kind: "ok"; rows: DARVettingRow[] }
  | { kind: "err" };

function roleAbbr(role: Role): string {
  return role === "app-user" ? "user" : role === "app-provider" ? "prov" : "sv";
}

function PkgRow({
  row,
  active,
  vet,
  onSelect,
}: {
  row: DARRow;
  active: boolean;
  vet: VetState | undefined;
  onSelect: () => void;
}) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={active}
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect();
        }
      }}
      style={{
        display: "grid",
        gridTemplateColumns: COLS,
        gap: 14,
        padding: "11px 14px",
        alignItems: "center",
        background: active ? W.selRow : "transparent",
        borderLeft: `2px solid ${active ? W.brand : "transparent"}`,
        borderBottom: `1px solid ${W.border}`,
        cursor: "pointer",
        transition: "background-color 120ms",
      }}
    >
      <span
        style={{
          fontSize: fs.data,
          fontWeight: active ? 600 : 500,
          color: active ? W.brandText : W.text,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.name}
      >
        {row.name}
      </span>
      <span
        style={{
          fontFamily: wMono,
          color: W.text2,
          fontSize: fs.meta,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {row.version}
      </span>
      <code
        style={{
          fontFamily: wMono,
          color: W.dim,
          fontSize: fs.meta,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={row.main}
      >
        {row.main.length > 13
          ? `${row.main.slice(0, 6)}…${row.main.slice(-5)}`
          : row.main}
      </code>
      <VettingCell vet={vet} />
    </div>
  );
}

// Per-participant vetting as a relabeled "sv prov user" dot trio: green
// vetted, grey unvetted, amber when a participant couldn't be probed.
function VettingCell({ vet }: { vet: VetState | undefined }) {
  if (!vet || vet.kind === "loading") {
    return (
      <span style={{ fontSize: fs.label, color: W.dim, fontFamily: wMono }}>…</span>
    );
  }
  if (vet.kind === "err" || vet.rows.length === 0) {
    return (
      <span
        style={{ fontSize: fs.label, color: W.warn, fontFamily: wMono }}
        title="vetting state unavailable"
      >
        unknown
      </span>
    );
  }
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        flexWrap: "wrap",
        fontFamily: wMono,
        fontSize: fs.label,
      }}
    >
      {vet.rows.map((r) => {
        const color = r.error ? W.warn : r.vetted ? W.ok : W.dim;
        const title = r.error
          ? `${r.role}: ${r.error}`
          : `${r.role}: ${r.vetted ? "vetted" : "not vetted"}`;
        return (
          <span
            key={r.role}
            title={title}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 5,
              color: r.error ? W.warn : W.text2,
            }}
          >
            <Dot color={color} size={6} />
            {roleAbbr(r.role)}
          </span>
        );
      })}
    </span>
  );
}

// ── Detail rail ─────────────────────────────────────────
function InspectDrawer({
  row,
  instance,
  role,
  allRows,
  compareWith,
  onCompare,
  onClearCompare,
  onClose,
}: {
  row: DARRow;
  instance: string;
  role: Role;
  allRows: DARRow[];
  compareWith: string | null;
  onCompare: (other: string) => void;
  onClearCompare: () => void;
  onClose: () => void;
}) {
  const [summary, setSummary] = useState<{
    modules: number;
    templates: number;
    sizeBytes: number;
  } | null>(null);
  const [showCompare, setShowCompare] = useState(false);
  const compareRow = allRows.find((r) => r.main === compareWith) ?? null;

  return (
    <aside
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        overflow: "hidden",
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 10,
          padding: "12px 14px",
          borderBottom: `1px solid ${W.border}`,
        }}
      >
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 2,
            minWidth: 0,
          }}
        >
          <span style={{ fontSize: fs.lead, fontWeight: 600, color: W.text }}>
            {row.name} {row.version}
          </span>
          {summary && (
            <span
              style={{ fontSize: fs.label, color: W.dim, fontFamily: wMono }}
              title="total package size (.dalf bytes)"
            >
              {formatBytes(summary.sizeBytes)}
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close details"
          title="Close"
          style={{
            marginLeft: "auto",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            width: 24,
            height: 24,
            borderRadius: R.control,
            border: "none",
            background: "transparent",
            color: W.dim,
            cursor: "pointer",
          }}
        >
          <IcX size={13} />
        </button>
      </header>

      <DrawerSection label="Package id">
        <MonoId
          value={row.main}
          full
          size={fs.label}
          color={W.text2}
          style={{ wordBreak: "break-all", whiteSpace: "normal", lineHeight: 1.6 }}
        />
      </DrawerSection>

      <DrawerSection label="Vetting">
        <VettingPanel instance={instance} mainID={row.main} />
      </DrawerSection>

      <DrawerSection
        label={compareWith ? "Structural diff" : "Structure"}
        aside={
          !compareWith && summary
            ? `${summary.modules} module${summary.modules === 1 ? "" : "s"} · ${summary.templates} template${summary.templates === 1 ? "" : "s"}`
            : undefined
        }
        last
      >
        {compareWith ? (
          <DARDiff instance={instance} a={row.main} b={compareWith} role={role} />
        ) : (
          <DARPackageTree
            instance={instance}
            mainID={row.main}
            role={role}
            onLoaded={setSummary}
          />
        )}
      </DrawerSection>

      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 8,
          alignItems: "center",
          padding: "12px 14px",
          background: W.sunken,
          borderTop: `1px solid ${W.border}`,
        }}
      >
        {compareWith ? (
          <>
            <span style={{ fontSize: fs.meta, color: W.dim }}>
              Comparing with{" "}
              {compareRow ? `${compareRow.name}@${compareRow.version}` : "—"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              style={{ marginLeft: "auto" }}
              onClick={onClearCompare}
              aria-label="exit compare"
              icon={<IcArrowRight style={{ transform: "rotate(180deg)" }} />}
            >
              Back to inspect
            </Button>
          </>
        ) : showCompare ? (
          <CompareSelector
            allRows={allRows}
            currentMain={row.main}
            onPick={(m) => {
              onCompare(m);
              setShowCompare(false);
            }}
          />
        ) : (
          <Button
            variant="secondary"
            size="sm"
            icon={<IcArrowRight />}
            onClick={() => setShowCompare(true)}
            disabled={allRows.length < 2}
          >
            Compare with…
          </Button>
        )}
      </div>
    </aside>
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
    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
      <span style={{ color: W.dim, fontSize: fs.label }}>Compare with</span>
      <select
        onChange={(e) => {
          if (e.target.value) onPick(e.target.value);
        }}
        defaultValue=""
        style={{
          background: W.surface,
          color: W.text,
          border: `1px solid ${W.border}`,
          borderRadius: R.control,
          padding: "4px 6px",
          fontSize: fs.label,
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
    return <div style={{ color: W.dim, fontSize: fs.meta }}>Loading vetting state…</div>;
  }
  if (state.kind === "err") {
    return <div style={{ color: W.err, fontSize: fs.meta }}>Vetting: {state.msg}</div>;
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      {state.rows.map((r) => (
        <div
          key={r.role}
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            gap: 10,
            fontSize: fs.meta,
          }}
        >
          <span style={{ fontFamily: wMono, color: W.text2 }}>
            {r.role === "sv" ? "sv participant" : r.role}
          </span>
          {r.error ? (
            <span style={{ color: W.warn, fontSize: fs.label }} title={r.error}>
              unavailable
            </span>
          ) : (
            <button
              type="button"
              role="switch"
              aria-checked={r.vetted}
              aria-label={`toggle vetting on ${r.role}`}
              disabled={pending === r.role}
              onClick={() => flip(r.role, !r.vetted)}
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 8,
                background: "transparent",
                border: "none",
                padding: 0,
                cursor: pending === r.role ? "wait" : "pointer",
                color: r.vetted ? W.ok : W.dim,
                fontFamily: wMono,
                fontSize: fs.meta,
              }}
            >
              <span>{r.vetted ? "vetted" : "unvetted"}</span>
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
            </button>
          )}
        </div>
      ))}
      {error && (
        <div style={{ color: W.err, fontSize: fs.label, marginTop: 4 }}>{error}</div>
      )}
    </div>
  );
}

function DrawerSection({
  label,
  aside,
  last,
  children,
}: {
  label: string;
  aside?: React.ReactNode;
  last?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      style={{
        padding: "12px 14px",
        borderBottom: last ? undefined : `1px solid ${W.border}`,
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
        <span style={{ ...wideCaps, fontSize: fs.micro, color: W.dim }}>
          {label}
        </span>
        {aside && (
          <span
            style={{ fontSize: fs.label, color: W.faint, fontFamily: wMono }}
          >
            {aside}
          </span>
        )}
      </div>
      <div>{children}</div>
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
          fontSize: fs.meta,
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
        fontSize: fs.meta,
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
            fontSize: fs.label,
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
        fontSize: fs.meta,
        color: W.err,
      }}
    >
      Upload failed: {msg}
    </div>
  );
}

function Segmented({
  options,
  value,
  onChange,
  variant = "solid",
  ariaLabel,
}: {
  options: Array<{ label: string; value: string }>;
  value: string;
  onChange: (v: string) => void;
  variant?: "solid" | "outline";
  ariaLabel?: string;
}) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      style={{
        display: "inline-flex",
        padding: 2,
        gap: 2,
        background: W.inset,
        borderRadius: R.control,
        width: "max-content",
      }}
    >
      {options.map((o) => {
        const active = o.value === value;
        const activeBg =
          variant === "solid" ? W.accentSolid : W.surface;
        const activeColor =
          variant === "solid" ? W.onAccentSolid : W.text;
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            aria-pressed={active}
            style={{
              appearance: "none",
              border:
                variant === "outline" && active
                  ? `1px solid ${W.border}`
                  : "1px solid transparent",
              cursor: "pointer",
              padding: "4px 12px",
              borderRadius: R.control,
              fontSize: fs.meta,
              fontFamily: "inherit",
              fontWeight: active ? 600 : 500,
              whiteSpace: "nowrap",
              background: active ? activeBg : "transparent",
              color: active ? activeColor : W.text2,
            }}
          >
            {o.label}
          </button>
        );
      })}
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
          fontSize: fs.meta,
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
      role="alert"
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        color: W.err,
        fontSize: fs.data,
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
        borderRadius: R.card,
        padding: 20,
      }}
    >
      <h3 style={{ color: W.warn, fontSize: fs.lead, marginTop: 0, marginBottom: 8 }}>
        {title}
      </h3>
      <p style={{ color: W.text2, fontSize: fs.body }}>{remediation}</p>
    </div>
  );
}
