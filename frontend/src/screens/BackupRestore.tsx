import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  downloadSnapshot,
  restoreSnapshot,
  type RestoreResponse,
} from "../api";
import { W, wMono } from "../tokens";

// Backup & restore card.
//
// Two actions in one card:
//   1. Download snapshot — POST /api/instances/:name/snapshot;
//      browser saves the tar via Content-Disposition. Single click,
//      no extra dialog; the server picks a stable filename.
//   2. Restore from snapshot — drag-drop OR file picker, with an
//      optional target-name override and a `--force` checkbox for
//      cross-version restores.
//
// The card lives inside InstanceDetail so the "current instance"
// context is already known. Restore-from-here uploads to /restore
// with name=currentInstance by default, but the user can type a
// different name (unblocks the cross-name case properly;
// today it works modulo the volume-rename limitation documented
// in that ticket).
//
// # CLI ↔ UI parity (AGENTS.md)
//
// Mirrors `localnet snapshot --name X --to <path>` and
// `localnet restore --name X --from <path> [--force]`. Same
// server-side validation, same error taxonomy (the toast text
// comes from the same ErrorCode list).

interface Props {
  // The instance this card lives under. Snapshot downloads
  // ALWAYS use this name. Restore defaults to this name but lets
  // the user override.
  instanceName: string;
}

type RestoreState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filename: string }
  | { kind: "success"; response: RestoreResponse }
  | { kind: "error"; message: string };

export function BackupRestore({ instanceName }: Props) {
  const [downloading, setDownloading] = useState(false);
  const [restore, setRestore] = useState<RestoreState>({ kind: "idle" });
  const [targetName, setTargetName] = useState(instanceName);
  const [force, setForce] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Sync targetName when the user navigates between instances.
  // Without this, the card mounted under "dev" keeps the initial
  // value forever — clicking into "pebble" still shows "dev" in
  // the target-name input. useState only honors its initial value
  // on first mount; the parent's prop change must be reflected
  // explicitly. Also resets the result banner on switch so a
  // success message from a previous restore doesn't bleed across
  // instances.
  useEffect(() => {
    setTargetName(instanceName);
    setRestore({ kind: "idle" });
    setForce(false);
  }, [instanceName]);

  async function onDownload() {
    // TODO(#issue): UI parity — when the instance is RUNNING the
    // backend now emits an X-Snapshot-Warning header (shared wording
    // with the CLI's stderr caveat, snapshot.RunningSnapshotWarning)
    // because a live snapshot is only crash-consistent. downloadSnapshot
    // uses a hidden-iframe form submit (so the browser owns the 100s-of-MB
    // download), which can't read response headers — so this card can't
    // surface the header directly. To reach full parity, thread the
    // instance status into <BackupRestore> (Props currently only carries
    // instanceName) and show the same caveat before/around the download
    // button when status === "running".
    setDownloading(true);
    try {
      await downloadSnapshot(instanceName);
    } finally {
      // The form-submit dispatches synchronously; the
      // browser owns the rest. A short flash on the button
      // is enough visual feedback.
      setTimeout(() => setDownloading(false), 600);
    }
  }

  async function onFileChosen(file: File | null) {
    if (!file) return;
    // Yellow Y15: client-side size cap. Snapshots can be large but
    // 4 GiB is the practical ceiling for an XHR upload (browsers
    // buffer the whole body in memory). Refuse client-side rather
    // than OOM the tab on a stray drop.
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

  return (
    <section
      style={{
        marginTop: 16,
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 16,
      }}
      aria-label="Backup and restore"
    >
      <header
        style={{
          marginBottom: 12,
          display: "flex",
          alignItems: "baseline",
          gap: 12,
        }}
      >
        <div style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
          Backup &amp; restore
        </div>
        <span style={{ color: W.dim, fontSize: 11.5 }}>
          tar archive of docker volumes + registry state
        </span>
      </header>

      {/* Download row */}
      <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
        <button
          onClick={onDownload}
          disabled={downloading}
          style={btn(W.brand, downloading)}
        >
          {downloading ? "Preparing…" : "↓ Download snapshot"}
        </button>
        <span style={{ color: W.dim, fontSize: 12 }}>
          mirrors{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>
            dpm localnet snapshot --name {instanceName} --to ./
            {instanceName}.tgz
          </code>
        </span>
      </div>

      {/* Restore row */}
      <div style={{ marginTop: 18 }}>
        <div
          style={{
            color: W.text,
            fontSize: 12.5,
            fontWeight: 600,
            marginBottom: 6,
          }}
        >
          Restore from snapshot
        </div>
        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragOver(false);
            const file = e.dataTransfer.files?.[0] ?? null;
            void onFileChosen(file);
          }}
          onClick={() => fileInputRef.current?.click()}
          role="button"
          tabIndex={0}
          aria-label="Drop snapshot file here or click to choose"
          style={{
            border: `1.5px dashed ${dragOver ? W.brand : W.border}`,
            background: dragOver ? `${W.brand}10` : "transparent",
            borderRadius: 8,
            padding: "14px 16px",
            cursor: "pointer",
            color: W.dim,
            fontSize: 12.5,
            textAlign: "center",
            transition: "all 0.12s",
          }}
        >
          {restore.kind === "uploading" ? (
            <UploadProgress
              filename={restore.filename}
              progress={restore.progress}
            />
          ) : (
            <>
              <div style={{ color: W.text2, marginBottom: 4 }}>
                Drop a .tgz here or click to choose
              </div>
              <div style={{ fontSize: 11, color: W.dim }}>
                will restore to instance{" "}
                <code style={{ fontFamily: wMono, color: W.text2 }}>
                  {targetName || "—"}
                </code>
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

        {/* Options row */}
        <div
          style={{
            marginTop: 10,
            display: "flex",
            alignItems: "center",
            gap: 14,
            flexWrap: "wrap",
            fontSize: 12,
          }}
        >
          <label style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <span style={{ color: W.dim }}>target name:</span>
            <input
              type="text"
              value={targetName}
              onChange={(e) => setTargetName(e.target.value)}
              style={{
                background: "transparent",
                color: W.text,
                border: `1px solid ${W.border}`,
                borderRadius: 4,
                padding: "2px 6px",
                fontSize: 12,
                fontFamily: wMono,
                width: 140,
              }}
              aria-label="Restore target instance name"
            />
          </label>
          <label
            style={{
              display: "flex",
              gap: 6,
              alignItems: "center",
              color: W.dim,
            }}
          >
            <input
              type="checkbox"
              checked={force}
              onChange={(e) => setForce(e.target.checked)}
              aria-label="Force restore on version mismatch"
            />
            <span>
              <code style={{ fontFamily: wMono, color: W.text2 }}>--force</code>{" "}
              (bypass Splice-version check)
            </span>
          </label>
        </div>

        {/* Result banner */}
        {restore.kind === "success" && (
          <div
            role="status"
            style={{
              marginTop: 10,
              background: `${W.brand}10`,
              border: `1px solid ${W.brand}`,
              borderRadius: 6,
              padding: "8px 12px",
              fontSize: 12,
              color: W.text2,
            }}
          >
            ✓ Restored{" "}
            <code style={{ fontFamily: wMono, color: W.brand }}>
              {restore.response.name}
            </code>
            . Bring it up via{" "}
            <code style={{ fontFamily: wMono, color: W.text }}>
              dpm localnet up --name {restore.response.name}
            </code>{" "}
            (or use the create flow).
          </div>
        )}
        {restore.kind === "error" && (
          <div
            role="alert"
            style={{
              marginTop: 10,
              background: `${W.err}10`,
              border: `1px solid ${W.err}`,
              borderRadius: 6,
              padding: "8px 12px",
              fontSize: 12,
              color: W.err,
            }}
          >
            Restore failed: {restore.message}
          </div>
        )}
      </div>
    </section>
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

function btn(accent: string, busy: boolean): React.CSSProperties {
  return {
    background: "transparent",
    color: busy ? W.dim : accent,
    border: `1px solid ${busy ? W.dim : accent}`,
    borderRadius: 6,
    padding: "5px 14px",
    fontSize: 12,
    fontWeight: 600,
    cursor: busy ? "wait" : "pointer",
  };
}
