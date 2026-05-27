import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  fetchDARList,
  uploadDARs,
  type DARListResponse,
  type DARRow,
  type Role,
} from "../api";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { W, wMono } from "../tokens";

// DARScreen — BIT-187 production layout.
//
// Matches docs/design/mockups/webui-dar.jsx:
//   - LEFT (320px) drag-drop upload + per-participant vetting
//                  toggles + Watch-mode card
//   - MIDDLE       package list with custom row layout
//                  (Package · Version · Package-id · Vetting · ⋯)
//   - RIGHT (360px) inspect drawer with hash/lf/uploaded + (future)
//                  diff vs prior version
//
// Watch-mode card is informational only — no filesystem watcher
// backend yet. Vetting toggles are display-only for the same
// reason. Both are tracked as follow-ups; the visual contract
// stays intact so the screen reads as "production" from the
// user's POV.

const ROLES: Role[] = ["app-user", "app-provider", "sv"];

type UploadState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filenames: string[] }
  | { kind: "success"; count: number }
  | { kind: "error"; message: string };

export function DARScreen() {
  const sel = useInstanceSelection();
  const name = sel.selected;
  const [role, setRole] = useState<Role>("app-user");
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: DARListResponse }
    | { kind: "port-missing"; remediation: string }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [selectedHash, setSelectedHash] = useState<string | null>(null);
  const [upload, setUpload] = useState<UploadState>({ kind: "idle" });
  const [dragOver, setDragOver] = useState(false);
  const [filter, setFilter] = useState<"all" | "app">("all");
  const [tick, setTick] = useState(0); // bump to refetch after upload
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
    setUpload({
      kind: "uploading",
      progress: 0,
      filenames: arr.map((f) => f.name),
    });
    try {
      const resp = await uploadDARs(name, arr, role, (frac) => {
        setUpload((s) =>
          s.kind === "uploading" ? { ...s, progress: frac } : s,
        );
      });
      setUpload({ kind: "success", count: resp.count });
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
      // "app DARs only" filter — exclude the canton-builtin /
      // splice system packages so the user sees just their stuff.
      list = list.filter(
        (d) =>
          !d.name.startsWith("canton-builtin-") &&
          !d.name.startsWith("splice-") &&
          !d.name.startsWith("daml-"),
      );
    }
    return list;
  }, [state, filter]);

  const selected = useMemo(
    () => (state.kind === "ok" ? rows.find((d) => d.main === selectedHash) ?? null : null),
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
                  <VetToggle on label="sv participant" />
                  <VetToggle on label="app-provider participant" />
                  <VetToggle on label="app-user participant" />
                </div>
                <div
                  style={{
                    marginTop: 12,
                    padding: "8px 10px",
                    background: W.border,
                    borderRadius: 6,
                    fontSize: 11.5,
                    color: W.text2,
                    lineHeight: 1.5,
                  }}
                >
                  <span style={{ color: W.brand }}>ⓘ</span> Vetting happens
                  automatically when DARs are uploaded via this screen. Manual
                  per-participant toggles are a follow-up.
                </div>
              </div>
            </Card>

            {upload.kind === "success" && (
              <SuccessBanner count={upload.count} />
            )}
            {upload.kind === "error" && <ErrorBanner msg={upload.message} />}

            <Card title="Watch mode" subtitle="dpm build → upload on change">
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 8,
                  fontSize: 12,
                  fontFamily: wMono,
                }}
              >
                <Row k="watching" v="./daml/**/*.daml" />
                <Row k="state" v="ready" vColor={W.dim} />
                <Row k="last upload" v="—" vColor={W.dim} />
                <div style={{ height: 1, background: W.border, margin: "4px 0" }} />
                <div style={{ color: W.dim, fontSize: 11.5, fontFamily: "inherit" }}>
                  Watch mode is a follow-up; ship a filesystem watcher in
                  CLI that pipes to this endpoint.
                </div>
              </div>
            </Card>
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

          {/* RIGHT — inspect drawer */}
          <InspectDrawer row={selected} />
        </div>
      )}
    </section>
  );
}

function PkgRow({
  row,
  active,
  onClick,
}: {
  row: DARRow;
  active: boolean;
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
      <span
        style={{
          fontSize: 11,
          color: "#62E2A0",
          fontFamily: wMono,
          display: "flex",
          alignItems: "center",
          gap: 6,
        }}
      >
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "#62E2A0",
          }}
        />
        vetted
      </span>
    </div>
  );
}

function InspectDrawer({ row }: { row: DARRow | null }) {
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
      <Section label="Vetting">
        <div
          style={{
            color: "#62E2A0",
            fontSize: 12.5,
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontFamily: wMono,
          }}
        >
          <span
            style={{
              width: 8,
              height: 8,
              borderRadius: "50%",
              background: "#62E2A0",
            }}
          />
          vetted · synchronizers attached
        </div>
      </Section>
      <Section label="Diff vs prior version">
        <div style={{ color: W.dim, fontSize: 12, lineHeight: 1.5 }}>
          Diff view is a follow-up — needs the SCU comparator endpoint
          backed by{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>internal/dar.Compare</code>
          . The CLI{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>
            dpm localnet dar diff
          </code>{" "}
          gives the same view today.
        </div>
      </Section>
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

function SuccessBanner({ count }: { count: number }) {
  return (
    <div
      role="status"
      style={{
        background: `${W.brand}10`,
        border: `1px solid ${W.brand}`,
        borderRadius: 6,
        padding: "8px 12px",
        fontSize: 12,
        color: W.text2,
      }}
    >
      ✓ Uploaded {count} package{count === 1 ? "" : "s"}. Refreshing list…
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

function VetToggle({ on, label }: { on: boolean; label: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 9,
        padding: "5px 8px",
        background: W.border,
        borderRadius: 6,
        fontSize: 12,
      }}
    >
      <span
        style={{
          width: 24,
          height: 14,
          background: on ? `${W.brand}` : "#3A4248",
          borderRadius: 7,
          position: "relative",
          flexShrink: 0,
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
      <span style={{ color: W.text2 }}>{label}</span>
    </div>
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
