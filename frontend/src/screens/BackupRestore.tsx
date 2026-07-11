import { useEffect, useRef, useState } from "react";
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

  // Resync on instance switch: useState keeps its first-mount value.
  useEffect(() => {
    setTargetName(instanceName);
    setRestore({ kind: "idle" });
    setForce(false);
    setDownloadError(null);
  }, [instanceName]);

  async function onDownload() {
    setDownloading(true);
    setDownloadError(null);
    try {
      await downloadSnapshot(instanceName);
    } catch (e) {
      setDownloadError(
        e instanceof ApiError ? e.message : "snapshot download failed",
      );
    } finally {
      setDownloading(false);
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

  return (
    <section
      style={{
        marginTop: 16,
        borderTop: `1px solid ${W.border}`,
        paddingTop: 16,
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
        <div style={{ fontWeight: 600, fontSize: fs.body, color: W.text }}>
          Backup &amp; restore
        </div>
        <span style={{ color: W.dim, fontSize: fs.small }}>
          logical database dump + registry state
        </span>
      </header>

      <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
        <Button
          variant="secondary"
          icon={<IcDownload />}
          onClick={onDownload}
          disabled={downloading}
        >
          {downloading ? "Preparing…" : "Download snapshot"}
        </Button>
        <span style={{ color: W.dim, fontSize: fs.small }}>
          mirrors{" "}
          <code style={{ fontFamily: wMono, color: W.text2 }}>
            dpm localnet snapshot --name {instanceName} --to ./
            {instanceName}.tgz
          </code>
        </span>
      </div>

      {downloadError && (
        <div
          role="alert"
          style={{
            marginTop: 10,
            background: `${tint(W.err, 6)}`,
            border: `1px solid ${W.err}`,
            borderRadius: R.control,
            padding: "8px 12px",
            fontSize: fs.small,
            color: W.err,
          }}
        >
          Download failed: {downloadError}
        </div>
      )}

      <div style={{ marginTop: 18 }}>
        <div
          style={{
            color: W.text,
            fontSize: fs.small,
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
            border: `1px dashed ${dragOver ? W.brand : W.border}`,
            background: dragOver ? `${tint(W.brand, 6)}` : "transparent",
            borderRadius: R.control,
            padding: "14px 16px",
            cursor: "pointer",
            color: W.dim,
            fontSize: fs.small,
            textAlign: "center",
            transition: `background-color ${FAST}, border-color ${FAST}`,
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
              <div style={{ fontSize: fs.small, color: W.dim }}>
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

        <div
          style={{
            marginTop: 10,
            display: "flex",
            alignItems: "center",
            gap: 14,
            flexWrap: "wrap",
            fontSize: fs.small,
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
                borderRadius: R.control,
                padding: "2px 6px",
                fontSize: fs.small,
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

        {restore.kind === "success" && (
          <div
            role="status"
            style={{
              marginTop: 10,
              background: `${tint(W.brand, 6)}`,
              border: `1px solid ${W.brand}`,
              borderRadius: R.control,
              padding: "8px 12px",
              fontSize: fs.small,
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
              background: `${tint(W.err, 6)}`,
              border: `1px solid ${W.err}`,
              borderRadius: R.control,
              padding: "8px 12px",
              fontSize: fs.small,
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
