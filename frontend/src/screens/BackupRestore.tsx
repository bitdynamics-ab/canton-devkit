import { useEffect, useRef, useState, type CSSProperties } from "react";
import {
  ApiError,
  downloadSnapshot,
  restoreSnapshot,
  type RestoreResponse,
} from "../api";
import { W, wMono, tint, R, FAST, fs } from "../tokens";
import { Button } from "../components/Button";
import { IcCheck, IcDownload } from "../components/icons";

interface Props {
  instanceName: string;
  /** Flips true while a snapshot capture is in flight so the parent header
   *  can show "Snapshotting…" instead of the paused-container "partial". */
  onSnapshotting?: (active: boolean) => void;
}

type RestoreState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filename: string }
  | { kind: "success"; response: RestoreResponse }
  | { kind: "error"; message: string };

export function BackupRestore({ instanceName, onSnapshotting }: Props) {
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [restore, setRestore] = useState<RestoreState>({ kind: "idle" });
  const [targetName, setTargetName] = useState(instanceName);
  const [force, setForce] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Resync on instance switch: useState keeps its first-mount value.
  useEffect(() => {
    setTargetName(instanceName);
    setRestore({ kind: "idle" });
    setForce(false);
    setDownloadError(null);
  }, [instanceName]);

  async function onDownload() {
    setDownloading(true);
    onSnapshotting?.(true);
    setDownloadError(null);
    try {
      await downloadSnapshot(instanceName);
    } catch (e) {
      setDownloadError(
        e instanceof ApiError ? e.message : "snapshot download failed",
      );
    } finally {
      setDownloading(false);
      onSnapshotting?.(false);
    }
  }

  async function onFileChosen(file: File | null) {
    if (!file) return;
    // XHR buffers the whole body in memory; refuse >4 GiB client-side
    // rather than OOM the tab.
    const MAX_TARBALL_BYTES = 4 * 1024 * 1024 * 1024;
    if (file.size > MAX_TARBALL_BYTES) {
      setRestore({
        kind: "error",
        message: `${file.name} is ${(file.size / 1024 / 1024 / 1024).toFixed(2)} GiB; per-file cap is 4 GiB. Restore directly from the CLI: dpm localnet restore --name ${targetName} --from ${file.name}`,
      });
      return;
    }
    setRestore({ kind: "uploading", progress: 0, filename: file.name });
    try {
      const response = await restoreSnapshot(file, targetName, {
        force,
        onProgress: (frac) => {
          setRestore((s) =>
            s.kind === "uploading" ? { ...s, progress: frac } : s,
          );
        },
      });
      setRestore({ kind: "success", response });
    } catch (e) {
      const message =
        e instanceof ApiError ? e.message : "upload failed";
      setRestore({ kind: "error", message });
    }
  }

  const cliBox: CSSProperties = {
    fontFamily: wMono,
    fontSize: fs.label,
    color: W.text2,
    background: W.inset,
    border: `1px solid ${W.border}`,
    borderRadius: R.control,
    padding: "8px 10px",
    overflowX: "auto",
    whiteSpace: "nowrap",
  };

  return (
    // Two side-by-side cards (Take a snapshot | Restore); rendered flush —
    // the parent Panel supplies the outer card, so no self-separator.
    <div style={{ padding: 14, display: "grid", gridTemplateColumns: "1fr 1fr", gap: 14 }} aria-label="Backup and restore">
      {/* Take a snapshot */}
      <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: R.card, overflow: "hidden", display: "flex", flexDirection: "column" }}>
        <div style={{ padding: "12px 14px", borderBottom: `1px solid ${W.border}` }}>
          <div style={{ fontWeight: 600, fontSize: fs.lead, color: W.text }}>Take a snapshot</div>
          <div style={{ color: W.dim, fontSize: fs.label, marginTop: 2 }}>logical database dump + registry state</div>
        </div>
        <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
          <div>
            <Button variant="secondary" icon={<IcDownload />} onClick={onDownload} disabled={downloading}>
              {downloading ? "Preparing…" : "Download snapshot"}
            </Button>
          </div>
          <div style={{ fontSize: fs.label, color: W.dim }}>
            pg_dumpall of the instance's Postgres · writers pause for a consistent
            capture (the instance shows <b>Snapshotting…</b> and resumes automatically)
          </div>
          <div style={cliBox}>
            dpm localnet snapshot --name {instanceName} --to ./{instanceName}.tgz
          </div>
          {downloadError && (
            <div role="alert" style={{ background: W.errBg, border: `1px solid ${W.errBorder}`, borderRadius: R.control, padding: "8px 12px", fontSize: fs.meta, color: W.err }}>
              Download failed: {downloadError}
            </div>
          )}
        </div>
      </div>

      {/* Restore */}
      <div style={{ background: W.surface, border: `1px solid ${W.border}`, borderRadius: R.card, overflow: "hidden", display: "flex", flexDirection: "column" }}>
        <div style={{ padding: "12px 14px", borderBottom: `1px solid ${W.border}` }}>
          <div style={{ fontWeight: 600, fontSize: fs.lead, color: W.text }}>Restore</div>
          <div style={{ color: W.dim, fontSize: fs.label, marginTop: 2 }}>
            replaces the state of{" "}
            <code style={{ fontFamily: wMono, color: W.text2 }}>{instanceName}</code>
          </div>
        </div>
        <div style={{ padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
          <div
            onDragOver={(e) => {
              e.preventDefault();
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOver(false);
              void onFileChosen(e.dataTransfer.files?.[0] ?? null);
            }}
            onClick={() => fileInputRef.current?.click()}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                fileInputRef.current?.click();
              }
            }}
            role="button"
            tabIndex={0}
            aria-label="Drop snapshot file here or click to choose"
            style={{
              border: `1px dashed ${dragOver ? W.brand : W.borderHi}`,
              background: dragOver ? tint(W.brand, 6) : W.sunken,
              borderRadius: R.control,
              padding: "18px 16px",
              cursor: "pointer",
              textAlign: "center",
              transition: `background-color ${FAST}, border-color ${FAST}`,
            }}
          >
            {restore.kind === "uploading" ? (
              <UploadProgress filename={restore.filename} progress={restore.progress} />
            ) : (
              <>
                <div style={{ color: W.text2, marginBottom: 4, fontSize: fs.meta }}>Drop a .tgz here or click to choose</div>
                <div style={{ fontSize: fs.label, color: W.dim }}>
                  will restore to instance{" "}
                  <code style={{ fontFamily: wMono, color: W.text2 }}>{targetName || "—"}</code>
                </div>
              </>
            )}
          </div>
          <input
            ref={fileInputRef}
            type="file"
            accept=".tgz,.tar.gz,application/gzip"
            style={{ display: "none" }}
            onChange={(e) => void onFileChosen(e.target.files?.[0] ?? null)}
          />

          <div style={{ display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap", fontSize: fs.meta }}>
            <label style={{ display: "flex", gap: 6, alignItems: "center" }}>
              <span style={{ color: W.dim }}>target name:</span>
              <input
                type="text"
                value={targetName}
                onChange={(e) => setTargetName(e.target.value)}
                style={{ background: "transparent", color: W.text, border: `1px solid ${W.border}`, borderRadius: R.control, padding: "2px 6px", fontSize: fs.meta, fontFamily: wMono, width: 130 }}
                aria-label="Restore target instance name"
              />
            </label>
            <label style={{ display: "flex", gap: 6, alignItems: "center", color: W.dim }}>
              <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} aria-label="Force restore on version mismatch" />
              <span>
                <code style={{ fontFamily: wMono, color: W.text2 }}>--force</code> (bypass Splice-version check)
              </span>
            </label>
          </div>

          {restore.kind === "success" && (
            <div role="status" style={{ background: W.okBg, border: `1px solid ${W.okBorder}`, borderRadius: R.control, padding: "8px 12px", fontSize: fs.meta, color: W.text2 }}>
              <span style={{ display: "inline-flex", alignItems: "center", gap: 6, color: W.ok }}>
                <IcCheck size={12} /> Restored
              </span>{" "}
              <code style={{ fontFamily: wMono, color: W.text }}>{restore.response.name}</code>. Bring it up via{" "}
              <code style={{ fontFamily: wMono, color: W.text }}>dpm localnet up --name {restore.response.name}</code>.
            </div>
          )}
          {restore.kind === "error" && (
            <div role="alert" style={{ background: W.errBg, border: `1px solid ${W.errBorder}`, borderRadius: R.control, padding: "8px 12px", fontSize: fs.meta, color: W.err }}>
              Restore failed: {restore.message}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function UploadProgress({
  filename,
  progress,
}: {
  filename: string;
  progress: number;
}) {
  const pct = Math.round(progress * 100);
  return (
    <div>
      <div style={{ color: W.text2, marginBottom: 6, fontFamily: wMono }}>
        Uploading {filename} ({pct}%)
      </div>
      <div
        style={{
          height: 6,
          background: W.border,
          borderRadius: R.control,
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
