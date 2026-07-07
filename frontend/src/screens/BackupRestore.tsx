import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  downloadSnapshot,
  restoreSnapshot,
  type RestoreResponse,
} from "../api";
import { W, wMono } from "../tokens";
import { Button } from "../components/Button";
import { IcCheck, IcDownload } from "../components/icons";

// Backup & restore card. Two actions:
//   1. Download snapshot — POST /api/instances/:name/snapshot; the
//      browser saves the tar via Content-Disposition.
//   2. Restore from snapshot — drag-drop or file picker, with an
//      optional target-name override and a `--force` checkbox for
//      cross-version restores.
//
// CLI ↔ UI parity (CONTRIBUTING.md): mirrors `localnet snapshot --name
// X --to <path>` and `localnet restore --name X --from <path>
// [--force]` — same server-side validation, same error taxonomy.

interface Props {
  // Snapshot downloads always use this name; restore defaults to it
  // but lets the user override.
  instanceName: string;
}

type RestoreState =
  | { kind: "idle" }
  | { kind: "uploading"; progress: number; filename: string }
  | { kind: "success"; response: RestoreResponse }
  | { kind: "error"; message: string };

export function BackupRestore({ instanceName }: Props) {
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [restore, setRestore] = useState<RestoreState>({ kind: "idle" });
  const [targetName, setTargetName] = useState(instanceName);
  const [force, setForce] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // useState only honors its initial value on first mount, so switching
  // instances must resync targetName explicitly — and reset the result
  // banner/options so state doesn't bleed across instances.
  useEffect(() => {
    setTargetName(instanceName);
    setRestore({ kind: "idle" });
    setForce(false);
    setDownloadError(null);
  }, [instanceName]);

  async function onDownload() {
    // The snapshot is application-consistent: the backend pauses the
    // instance's node containers for the duration of the dump (the same
    // quiesce the CLI does), so there is no crash-consistency caveat to
    // surface.
    setDownloading(true);
    setDownloadError(null);
    try {
      await downloadSnapshot(instanceName);
    } catch (e) {
      // downloadSnapshot rejects when the server returned an error
      // document instead of a file; without surfacing it the button
      // would just flash and the user would assume success.
      setDownloadError(
        e instanceof ApiError ? e.message : "snapshot download failed",
      );
    } finally {
      setDownloading(false);
    }
  }

  async function onFileChosen(file: File | null) {
    if (!file) return;
    // 4 GiB is the practical ceiling for an XHR upload (browsers buffer
    // the whole body in memory) — refuse client-side rather than OOM
    // the tab on a stray drop.
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
        borderRadius: 4,
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
        <Button
          variant="secondary"
          icon={<IcDownload />}
          onClick={onDownload}
          disabled={downloading}
        >
          {downloading ? "Preparing…" : "Download snapshot"}
        </Button>
        <span style={{ color: W.dim, fontSize: 12 }}>
          mirrors{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>
            dpm localnet snapshot --name {instanceName} --to ./
            {instanceName}.tgz
          </code>
        </span>
      </div>

      {/* Download error banner */}
      {downloadError && (
        <div
          role="alert"
          style={{
            marginTop: 10,
            background: `${W.err}10`,
            border: `1px solid ${W.err}`,
            borderRadius: 2,
            padding: "8px 12px",
            fontSize: 12,
            color: W.err,
          }}
        >
          Download failed: {downloadError}
        </div>
      )}

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
            borderRadius: 4,
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
                borderRadius: 2,
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
              borderRadius: 2,
              padding: "8px 12px",
              fontSize: 12,
              color: W.text2,
            }}
          >
            <span
              style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
            >
              <IcCheck size={12} /> Restored
            </span>{" "}
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
              borderRadius: 2,
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
