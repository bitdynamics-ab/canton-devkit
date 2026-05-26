import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  createInstance,
  fetchSpliceVersions,
  type CreateInstanceAcceptedResponse,
  type SpliceVersionEntry,
} from "../api";
import { W, wMono, wSans } from "../tokens";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// CreateLocalNetModal — the "Create LocalNet" flow from
// webui-create.jsx. Drives three top-level stages internally:
//
//   1. form        — empty/validating; name + version + advanced
//   2. submitting  — POST in flight; brief (<1s)
//   3. progress    — 202 received; EventSource open; render steps
//
// "Done" / "failed" / "cancelled" are sub-states of progress —
// the modal stays open until the user closes it.
//
// Validation: name regex matches internal/registry's RFC 1123
// DNS-label rule. We pre-validate client-side for snappy feedback;
// the server validates too, so a stale regex here is a UX bug
// not a security one.
const NAME_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

interface Props {
  open: boolean;
  onClose: () => void;
  // Called on success so the dashboard refreshes its instance
  // list and selects the new instance. Optional — most callers
  // pass () => sel.refresh().
  onCreated?: (name: string) => void;
}

type Stage =
  | { kind: "form" }
  | { kind: "submitting" }
  | { kind: "progress"; accepted: CreateInstanceAcceptedResponse }
  | { kind: "error"; message: string; remediation?: string[] };

export function CreateLocalNetModal({ open, onClose, onCreated }: Props) {
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [allowUncurated, setAllowUncurated] = useState(false);
  const [versions, setVersions] = useState<SpliceVersionEntry[]>([]);
  const [stage, setStage] = useState<Stage>({ kind: "form" });
  const inputRef = useRef<HTMLInputElement>(null);

  const progress = useCreateProgress(
    stage.kind === "progress" ? stage.accepted.events_url : null,
  );

  // Reset every time the modal opens. Stale form values from a
  // previous launch are confusing — each open is a fresh form.
  useEffect(() => {
    if (open) {
      setName("");
      setVersion("");
      setAllowUncurated(false);
      setStage({ kind: "form" });
      requestAnimationFrame(() => inputRef.current?.focus());
      // Refresh the version catalogue on open. Cached on the
      // server (versions.json is embedded), so this is fast.
      fetchSpliceVersions()
        .then((r) => {
          setVersions(r.versions);
          // Pre-select the "latest" entry so the user doesn't
          // have to click anything for the common case.
          if (!version) {
            const latest = r.versions.find((v) => v.status === "latest");
            if (latest) setVersion(latest.tag);
          }
        })
        .catch(() => {
          // Non-fatal — the picker shows an "unavailable" hint
          // and the user can still type a version.
          setVersions([]);
        });
    }
    // versions captured intentionally — only re-run on open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Escape closes the modal — but only from the form/done/failed
  // stages, not mid-submit. Accidentally cancelling a 90-second
  // up because of a stray Esc is much worse than the extra click.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      if (stage.kind === "submitting") return;
      if (stage.kind === "progress" && progress.banner.kind === "running") {
        return; // running — require explicit Cancel button click
      }
      onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, stage, progress.banner.kind, onClose]);

  // When the up succeeds, fire onCreated so the dashboard can
  // refresh its list and pick up the new instance. The modal
  // stays open until the user closes it explicitly — the
  // "is ready" banner is the celebratory beat.
  //
  // Fires AT MOST ONCE per (stage instance) — without this the
  // callback gets re-invoked on every render because parents
  // typically pass a fresh arrow function as `onCreated`, which
  // changes the effect's identity each render and re-triggers
  // the body. With `firedRef` we guarantee one call per accepted
  // instance, regardless of how the parent typed the callback.
  const firedRef = useRef<string | null>(null);
  useEffect(() => {
    if (
      progress.banner.kind === "done" &&
      stage.kind === "progress" &&
      firedRef.current !== stage.accepted.instance
    ) {
      firedRef.current = stage.accepted.instance;
      onCreated?.(stage.accepted.instance);
    }
  }, [progress.banner.kind, stage, onCreated]);
  // Reset the fired guard whenever the modal closes so a NEW
  // open with a NEW create can fire onCreated again.
  useEffect(() => {
    if (!open) firedRef.current = null;
  }, [open]);

  if (!open) return null;

  const nameValid = NAME_RE.test(name);
  const canSubmit = nameValid && stage.kind === "form";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStage({ kind: "submitting" });
    try {
      const accepted = await createInstance({
        name,
        ...(version ? { version } : {}),
        ...(allowUncurated ? { allow_uncurated: true } : {}),
      });
      setStage({ kind: "progress", accepted });
    } catch (e) {
      const apiErr = e instanceof ApiError ? e : null;
      setStage({
        kind: "error",
        message: apiErr?.message ?? "failed to create instance",
        remediation: apiErr?.remediation,
      });
    }
  }

  async function cancelInFlight() {
    if (stage.kind !== "progress") return;
    try {
      await cancelInstanceUp(stage.accepted.instance);
      // The SSE stream will deliver the kind=cancelled event;
      // the reducer flips banner.kind to "cancelled". No local
      // state mutation needed.
    } catch {
      // Cancel-already-finished is a 404 swallowed by the API
      // client; other errors are rare enough that an inline
      // toast is overkill.
    }
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Create new LocalNet instance"
      onClick={() => {
        if (stage.kind === "submitting") return;
        if (stage.kind === "progress" && progress.banner.kind === "running") return;
        onClose();
      }}
      style={overlayStyle}
    >
      <div onClick={(e) => e.stopPropagation()} style={modalStyle}>
        <ModalHeader stage={stage} progress={progress} name={name} />
        <div style={{ overflow: "auto", maxHeight: "70vh" }}>
          {stage.kind === "form" && (
            <FormBody
              inputRef={inputRef}
              name={name}
              setName={setName}
              nameValid={nameValid || name.length === 0}
              version={version}
              setVersion={setVersion}
              versions={versions}
              allowUncurated={allowUncurated}
              setAllowUncurated={setAllowUncurated}
              onSubmit={submit}
            />
          )}
          {stage.kind === "submitting" && <SubmittingBody name={name} />}
          {stage.kind === "progress" && (
            <ProgressBody progress={progress} instance={stage.accepted.instance} />
          )}
          {stage.kind === "error" && (
            <ErrorBody message={stage.message} remediation={stage.remediation} />
          )}
        </div>
        <ModalFooter
          stage={stage}
          progress={progress}
          canSubmit={canSubmit}
          onSubmit={submit}
          onCancelInFlight={cancelInFlight}
          onClose={onClose}
        />
      </div>
    </div>
  );
}

// ── header / footer ───────────────────────────────────────────────

function ModalHeader({
  stage,
  progress,
  name,
}: {
  stage: Stage;
  progress: ProgressState;
  name: string;
}) {
  let title = "Create LocalNet";
  let subtitle: string = "Spin up a Canton + Splice stack.";
  if (stage.kind === "progress") {
    title = `Bringing up · ${stage.accepted.instance}`;
    if (progress.banner.kind === "done") {
      title = `${stage.accepted.instance} is ready`;
      subtitle = "Click any service URL to open it";
    } else if (progress.banner.kind === "failed") {
      title = `Failed to bring up ${stage.accepted.instance}`;
      subtitle = progress.banner.summary ?? "see step detail below";
    } else if (progress.banner.kind === "cancelled") {
      title = `Cancelled · ${stage.accepted.instance}`;
      subtitle = "the bring-up was cancelled before completion";
    } else {
      subtitle = "streaming via /api/instances/" + stage.accepted.instance + "/events";
    }
  } else if (stage.kind === "submitting") {
    title = `Starting · ${name}`;
    subtitle = "validating + queuing…";
  } else if (stage.kind === "error") {
    title = "Couldn't start";
    subtitle = stage.message;
  }
  return (
    <header
      style={{
        padding: "16px 20px",
        borderBottom: `1px solid ${W.border}`,
      }}
    >
      <div style={{ fontWeight: 600, fontSize: 15, color: W.text }}>{title}</div>
      <div style={{ color: W.dim, fontSize: 12, marginTop: 3 }}>{subtitle}</div>
    </header>
  );
}

function ModalFooter({
  stage,
  progress,
  canSubmit,
  onSubmit,
  onCancelInFlight,
  onClose,
}: {
  stage: Stage;
  progress: ProgressState;
  canSubmit: boolean;
  onSubmit: (e: React.FormEvent) => void;
  onCancelInFlight: () => void;
  onClose: () => void;
}) {
  const isRunning =
    stage.kind === "progress" && progress.banner.kind === "running";
  return (
    <footer
      style={{
        padding: "10px 16px",
        borderTop: `1px solid ${W.border}`,
        display: "flex",
        justifyContent: "flex-end",
        gap: 8,
        alignItems: "center",
      }}
    >
      {stage.kind === "progress" && progress.startedAt && (
        <span
          style={{
            color: W.dim,
            fontSize: 11.5,
            marginRight: "auto",
            fontFamily: wMono,
          }}
        >
          <Elapsed startedAt={progress.startedAt} />
          {isRunning ? " · streaming" : ""}
        </span>
      )}
      {isRunning ? (
        <>
          <button
            type="button"
            onClick={onCancelInFlight}
            style={btnStyle(W.warn, false)}
          >
            Cancel bring-up
          </button>
        </>
      ) : stage.kind === "form" ? (
        <>
          <button type="button" onClick={onClose} style={btnStyle(W.dim, false)}>
            Cancel
          </button>
          <button
            type="submit"
            onClick={onSubmit}
            disabled={!canSubmit}
            style={btnStyle(W.brand, canSubmit)}
          >
            Create LocalNet
          </button>
        </>
      ) : (
        <button
          type="button"
          onClick={onClose}
          style={btnStyle(W.brand, true)}
        >
          Close
        </button>
      )}
    </footer>
  );
}

// ── stage bodies ──────────────────────────────────────────────────

interface FormBodyProps {
  inputRef: React.RefObject<HTMLInputElement>;
  name: string;
  setName: (s: string) => void;
  nameValid: boolean;
  version: string;
  setVersion: (s: string) => void;
  versions: SpliceVersionEntry[];
  allowUncurated: boolean;
  setAllowUncurated: (b: boolean) => void;
  onSubmit: (e: React.FormEvent) => void;
}

function FormBody({
  inputRef,
  name,
  setName,
  nameValid,
  version,
  setVersion,
  versions,
  allowUncurated,
  setAllowUncurated,
  onSubmit,
}: FormBodyProps) {
  return (
    <form onSubmit={onSubmit} style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 14 }}>
      <Field
        label="name"
        hint="lowercase DNS label · 1–63 chars · can't start/end with hyphen"
        error={
          !nameValid && name.length > 0
            ? "must be lowercase a-z / 0-9 / hyphen"
            : undefined
        }
      >
        <input
          ref={inputRef}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. demo, pr-841, hubble"
          style={inputStyle}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>

      <Field label="splice version" hint="choose from the curated catalogue">
        <VersionPicker
          versions={versions}
          selected={version}
          onSelect={setVersion}
        />
      </Field>

      <details style={{ marginTop: 4 }}>
        <summary
          style={{
            cursor: "pointer",
            color: W.text2,
            fontSize: 12,
            fontWeight: 600,
            padding: "6px 0",
          }}
        >
          Advanced — uncurated versions
        </summary>
        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 8,
            padding: "8px 12px",
            background: W.surface2,
            borderRadius: 8,
          }}
        >
          <input
            type="checkbox"
            checked={allowUncurated}
            onChange={(e) => setAllowUncurated(e.target.checked)}
          />
          <div style={{ flex: 1, fontSize: 12, color: W.text2 }}>
            <strong>Allow uncurated Splice tags</strong>
            <div style={{ color: W.dim, fontSize: 11, marginTop: 2 }}>
              Resolve <code style={{ fontFamily: wMono }}>--version</code>{" "}
              upstream when not in the catalogue. DevKit hasn't reviewed
              those bits — experiments only.
            </div>
          </div>
        </label>
      </details>

      <div
        style={{
          fontSize: 11.5,
          color: W.dim,
          marginTop: 4,
          padding: "8px 10px",
          background: W.surface2,
          borderRadius: 7,
          fontFamily: wMono,
        }}
      >
        CLI equivalent:{" "}
        <span style={{ color: W.brand }}>
          dpm localnet up --name {name || "<name>"} --version{" "}
          {version || "latest"}
        </span>
      </div>
    </form>
  );
}

function SubmittingBody({ name }: { name: string }) {
  return (
    <div
      style={{
        padding: "32px 24px",
        textAlign: "center",
        color: W.dim,
        fontSize: 13,
      }}
    >
      <div style={{ marginBottom: 8 }}>
        Validating + queuing <strong style={{ color: W.text }}>{name}</strong>…
      </div>
      <div style={{ color: W.faint, fontSize: 11 }}>
        Server will hand back an events URL we'll subscribe to next.
      </div>
    </div>
  );
}

function ProgressBody({
  progress,
  instance,
}: {
  progress: ProgressState;
  instance: string;
}) {
  return (
    <div style={{ padding: "12px 18px" }}>
      <BannerStripe banner={progress.banner} />
      {progress.warnings.map((m, i) => (
        <div
          key={i}
          style={{
            background: `${W.warn}1A`,
            border: `1px solid ${W.warn}44`,
            color: W.warn,
            borderRadius: 6,
            padding: "6px 10px",
            fontSize: 11.5,
            margin: "6px 0",
          }}
        >
          ⚠ {m}
        </div>
      ))}
      <div style={{ marginTop: 8 }}>
        {STEP_ORDER.map((step) => (
          <StepRow
            key={step}
            label={STEP_LABELS[step]}
            state={progress.perStep[step]}
          />
        ))}
      </div>
      {progress.terminal.length > 0 && (
        <details open style={{ marginTop: 14 }}>
          <summary style={{ cursor: "pointer", color: W.dim, fontSize: 11.5 }}>
            Terminal output · {progress.terminal.length} line(s)
          </summary>
          <pre
            style={{
              margin: "8px 0 0",
              background: W.bg,
              border: `1px solid ${W.border}`,
              borderRadius: 7,
              padding: "10px 12px",
              fontFamily: wMono,
              fontSize: 10.5,
              color: W.text2,
              maxHeight: 180,
              overflow: "auto",
              lineHeight: 1.55,
            }}
          >
            {progress.terminal.map((l, i) => (
              <div
                key={i}
                style={{ color: l.stream === "stderr" ? W.warn : W.text2 }}
              >
                {l.text}
              </div>
            ))}
          </pre>
        </details>
      )}
      <div
        style={{
          marginTop: 10,
          fontFamily: wMono,
          fontSize: 10.5,
          color: W.faint,
        }}
      >
        instance: <span style={{ color: W.brand }}>{instance}</span>
      </div>
    </div>
  );
}

function ErrorBody({
  message,
  remediation,
}: {
  message: string;
  remediation?: string[];
}) {
  return (
    <div
      role="alert"
      style={{
        padding: "20px 22px",
        background: `${W.err}10`,
        color: W.text,
      }}
    >
      <strong style={{ color: W.err }}>{message}</strong>
      {remediation && remediation.length > 0 && (
        <ul
          style={{
            marginTop: 10,
            paddingLeft: 18,
            color: W.text2,
            fontSize: 12.5,
            lineHeight: 1.6,
          }}
        >
          {remediation.map((r, i) => (
            <li key={i}>{r}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── pieces ────────────────────────────────────────────────────────

function BannerStripe({ banner }: { banner: ProgressState["banner"] }) {
  if (banner.kind === "running") {
    return (
      <div
        style={{
          padding: "8px 12px",
          background: `${W.brand}10`,
          color: W.brand,
          border: `1px solid ${W.brand}44`,
          borderRadius: 7,
          fontSize: 12,
          fontFamily: wMono,
        }}
      >
        ● streaming step events
      </div>
    );
  }
  if (banner.kind === "done") {
    return (
      <div
        style={{
          padding: "10px 14px",
          background: `${W.ok}1A`,
          color: W.ok,
          border: `1px solid ${W.ok}`,
          borderRadius: 7,
          fontSize: 13,
          fontWeight: 600,
        }}
      >
        ✓ {banner.detail ?? "ready"}
      </div>
    );
  }
  if (banner.kind === "failed") {
    return (
      <div
        style={{
          padding: "10px 14px",
          background: `${W.err}10`,
          color: W.err,
          border: `1px solid ${W.err}`,
          borderRadius: 7,
          fontSize: 12.5,
        }}
      >
        <strong>✗ {banner.summary ?? "failed"}</strong>
        {banner.cause && (
          <div style={{ color: W.text2, marginTop: 4, fontFamily: wMono, fontSize: 11 }}>
            {banner.cause}
          </div>
        )}
      </div>
    );
  }
  // cancelled
  return (
    <div
      style={{
        padding: "10px 14px",
        background: `${W.warn}1A`,
        color: W.warn,
        border: `1px solid ${W.warn}`,
        borderRadius: 7,
        fontSize: 12.5,
      }}
    >
      ⏹ cancelled{banner.reason ? ` — ${banner.reason}` : ""}
    </div>
  );
}

function StepRow({ label, state }: { label: string; state: StepState }) {
  const icon = (() => {
    switch (state.status) {
      case "done":
        return <span style={{ color: W.ok }}>✓</span>;
      case "active":
        return <span style={{ color: W.brand }}>●</span>;
      case "fail":
        return <span style={{ color: W.err }}>✕</span>;
      default:
        return <span style={{ color: W.faint }}>○</span>;
    }
  })();
  const color =
    state.status === "fail"
      ? W.err
      : state.status === "active"
      ? W.text
      : state.status === "done"
      ? W.text
      : W.text2;
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        padding: "6px 4px",
        borderBottom: `1px dashed ${W.border}`,
        fontSize: 12.5,
      }}
    >
      <span style={{ width: 14, textAlign: "center" }}>{icon}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color }}>{label}</div>
        {(state.detail || state.summary) && (
          <div
            style={{
              color: state.status === "fail" ? W.err : W.dim,
              fontSize: 11,
              fontFamily: wMono,
              marginTop: 2,
            }}
          >
            {state.summary ?? state.detail}
          </div>
        )}
        {state.percent !== undefined && state.status === "active" && (
          <div
            style={{
              marginTop: 4,
              height: 4,
              background: W.surface2,
              borderRadius: 2,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                width: `${state.percent}%`,
                height: "100%",
                background: W.brand,
                transition: "width 0.2s",
              }}
            />
          </div>
        )}
      </div>
    </div>
  );
}

function VersionPicker({
  versions,
  selected,
  onSelect,
}: {
  versions: SpliceVersionEntry[];
  selected: string;
  onSelect: (tag: string) => void;
}) {
  if (versions.length === 0) {
    return (
      <input
        value={selected}
        onChange={(e) => onSelect(e.target.value)}
        placeholder="0.4.12"
        style={inputStyle}
        spellCheck={false}
      />
    );
  }
  return (
    <div
      style={{
        background: W.bg,
        border: `1px solid ${W.border}`,
        borderRadius: 7,
        maxHeight: 200,
        overflow: "auto",
      }}
    >
      {versions.map((v) => {
        const isSelected = v.tag === selected;
        return (
          <button
            key={v.tag}
            type="button"
            onClick={() => onSelect(v.tag)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              width: "100%",
              padding: "8px 12px",
              border: "none",
              background: isSelected ? `${W.brand}14` : "transparent",
              borderLeft: `2px solid ${isSelected ? W.brand : "transparent"}`,
              color: W.text,
              cursor: "pointer",
              fontSize: 12.5,
              textAlign: "left",
            }}
          >
            <span style={{ fontFamily: wMono, fontWeight: 600 }}>{v.tag}</span>
            {v.status === "latest" && (
              <span
                style={{
                  background: `${W.brand}1A`,
                  color: W.brand,
                  fontSize: 10,
                  fontWeight: 700,
                  padding: "1px 6px",
                  borderRadius: 999,
                }}
              >
                latest
              </span>
            )}
            <span style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>
              major {v.major}
            </span>
            <span style={{ flex: 1 }} />
            <span style={{ color: W.dim, fontFamily: wMono, fontSize: 10.5 }}>
              {v.commit.slice(0, 8)}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <label style={{ display: "block" }}>
      <div
        style={{
          color: W.text2,
          fontSize: 11,
          marginBottom: 4,
          textTransform: "uppercase",
          letterSpacing: 0.8,
        }}
      >
        {label}
      </div>
      {children}
      {error && (
        <div style={{ color: W.err, fontSize: 11, marginTop: 4 }}>{error}</div>
      )}
      {hint && !error && (
        <div style={{ color: W.dim, fontSize: 11, marginTop: 4 }}>{hint}</div>
      )}
    </label>
  );
}

function Elapsed({ startedAt }: { startedAt: number }) {
  // Tick once per second to keep the elapsed counter live. The
  // setInterval is cheap; clearing on unmount avoids leaks when
  // the modal closes mid-up.
  const [, force] = useState(0);
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  const sec = Math.floor((Date.now() - startedAt) / 1000);
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return <>{m}:{String(s).padStart(2, "0")} elapsed</>;
}

// ── styles ────────────────────────────────────────────────────────

const overlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  background: "rgba(0,0,0,0.6)",
  display: "flex",
  alignItems: "flex-start",
  justifyContent: "center",
  paddingTop: "8vh",
  zIndex: 100,
  fontFamily: wSans,
};

const modalStyle: React.CSSProperties = {
  width: "min(680px, 92vw)",
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: 12,
  boxShadow: "0 24px 64px rgba(0,0,0,0.6)",
  overflow: "hidden",
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  background: W.bg,
  color: W.text,
  border: `1px solid ${W.border}`,
  borderRadius: 6,
  padding: "7px 10px",
  fontSize: 13,
  fontFamily: wMono,
  outline: "none",
};

function btnStyle(color: string, primary: boolean): React.CSSProperties {
  return {
    background: primary ? color : "transparent",
    color: primary ? "#082018" : color,
    border: `1px solid ${color}`,
    borderRadius: 6,
    padding: "6px 14px",
    fontSize: 12,
    fontWeight: 600,
    cursor: primary ? "pointer" : "pointer",
  };
}
