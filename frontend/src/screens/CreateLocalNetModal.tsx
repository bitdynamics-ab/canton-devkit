import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  PreflightFailedError,
  STEP_LABELS,
  STEP_ORDER,
  cancelInstanceUp,
  createInstance,
  fetchPreflight,
  fetchSpliceVersions,
  type CreateInstanceAcceptedResponse,
  type PreflightCheck,
  type PreflightReport,
  type SpliceVersionEntry,
} from "../api";
import { W, wMono, wSans, tint, R } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcAlert, IcCheck, IcStop, IcX } from "../components/icons";
import { remediationForCode } from "./remediation";
import {
  type ProgressState,
  type StepState,
  useCreateProgress,
} from "./useCreateProgress";

// Stages: form → submitting → progress (with done/failed/cancelled
// sub-states). RFC 1123 DNS-label rule from internal/registry; the
// server re-validates, so this is advisory only.
const NAME_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

interface Props {
  open: boolean;
  onClose: () => void;
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
  const [prometheus, setPrometheus] = useState(false);
  const [grafana, setGrafana] = useState(false);
  const [tokensV2, setTokensV2] = useState(false);
  const [portBase, setPortBase] = useState("");
  const [versions, setVersions] = useState<SpliceVersionEntry[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [versionsError, setVersionsError] = useState<string | null>(null);
  const [stage, setStage] = useState<Stage>({ kind: "form" });
  // "blocked" (any FAIL) disables Create; WARN-only still allows submit.
  const [preflight, setPreflight] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; report: PreflightReport }
    | { kind: "blocked"; report: PreflightReport }
    | { kind: "err"; message: string }
  >({ kind: "idle" });
  const inputRef = useRef<HTMLInputElement>(null);

  const progress = useCreateProgress(
    stage.kind === "progress" ? stage.accepted.events_url : null,
  );

  // Each open is a fresh form.
  useEffect(() => {
    if (open) {
      setName("");
      setVersion("");
      setAllowUncurated(false);
      setPrometheus(false);
      setGrafana(false);
      setTokensV2(false);
      setPortBase("");
      setStage({ kind: "form" });
      requestAnimationFrame(() => inputRef.current?.focus());
      setVersionsLoading(true);
      setVersionsError(null);
      fetchSpliceVersions()
        .then((r) => {
          setVersions(r.versions);
          if (!version) {
            const latest = r.versions.find((v) => v.status === "latest");
            if (latest) setVersion(latest.tag);
          }
        })
        .catch((e) => {
          setVersions([]);
          setVersionsError(
            e instanceof ApiError ? e.message : "Couldn't load the version catalogue",
          );
        })
        .finally(() => setVersionsLoading(false));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Escape closes, but not mid-submit or while a bring-up is running.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      if (stage.kind === "submitting") return;
      if (stage.kind === "progress" && progress.banner.kind === "running") {
        return; // require explicit Cancel while running
      }
      onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, stage, progress.banner.kind, onClose]);

  // Fire onCreated at most once per accepted instance; a fresh
  // callback identity each render would otherwise re-invoke it.
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
  useEffect(() => {
    if (!open) firedRef.current = null;
  }, [open]);

  // Probe requirements on version change (form stage only). Skipped for
  // uncurated tags; the cancelled flag keeps the latest result when the
  // user flips versions quickly.
  useEffect(() => {
    if (!open || stage.kind !== "form") return;
    if (!version || allowUncurated) {
      setPreflight({ kind: "idle" });
      return;
    }
    let cancelled = false;
    setPreflight({ kind: "loading" });
    fetchPreflight(version)
      .then((report) => {
        if (cancelled) return;
        setPreflight(
          report.ok
            ? { kind: "ok", report }
            : { kind: "blocked", report },
        );
      })
      .catch((e) => {
        if (cancelled) return;
        setPreflight({
          kind: "err",
          message:
            e instanceof ApiError
              ? e.message
              : "could not check system requirements",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [open, stage.kind, version, allowUncurated]);

  if (!open) return null;

  const nameValid = NAME_RE.test(name);
  const preflightBlocks = preflight.kind === "blocked";
  const canSubmit =
    nameValid && stage.kind === "form" && !preflightBlocks;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStage({ kind: "submitting" });
    try {
      const accepted = await createInstance({
        name,
        ...(version ? { version } : {}),
        ...(allowUncurated ? { allow_uncurated: true } : {}),
        ...(() => {
          const profiles = [
            ...(prometheus ? ["prometheus"] : []),
            ...(grafana ? ["grafana"] : []),
            ...(tokensV2 ? ["tokens-v2"] : []),
          ];
          return profiles.length ? { profiles } : {};
        })(),
        ...(portBase.trim() ? { port_base: Number(portBase.trim()) } : {}),
      });
      setStage({ kind: "progress", accepted });
    } catch (e) {
      if (e instanceof PreflightFailedError) {
        // Server gate caught what the inline probe missed; show findings.
        setPreflight({ kind: "blocked", report: e.report });
        setStage({ kind: "form" });
        return;
      }
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
      // SSE delivers kind=cancelled; the reducer flips the banner.
    } catch {
      // Cancel-after-finish 404s; not worth surfacing.
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
              versionsLoading={versionsLoading}
              versionsError={versionsError}
              allowUncurated={allowUncurated}
              setAllowUncurated={setAllowUncurated}
              prometheus={prometheus}
              setPrometheus={setPrometheus}
              grafana={grafana}
              setGrafana={setGrafana}
              tokensV2={tokensV2}
              setTokensV2={setTokensV2}
              portBase={portBase}
              setPortBase={setPortBase}
              preflight={preflight}
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
        <Button variant="secondary" size="md" onClick={onCancelInFlight}>
          Cancel bring-up
        </Button>
      ) : stage.kind === "form" ? (
        <>
          <Button variant="ghost" size="md" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="md"
            type="submit"
            onClick={onSubmit}
            disabled={!canSubmit}
          >
            Create LocalNet
          </Button>
        </>
      ) : (
        <Button variant="primary" size="md" onClick={onClose}>
          Close
        </Button>
      )}
    </footer>
  );
}

type PreflightState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ok"; report: PreflightReport }
  | { kind: "blocked"; report: PreflightReport }
  | { kind: "err"; message: string };

interface FormBodyProps {
  inputRef: React.RefObject<HTMLInputElement>;
  name: string;
  setName: (s: string) => void;
  nameValid: boolean;
  version: string;
  setVersion: (s: string) => void;
  versions: SpliceVersionEntry[];
  versionsLoading: boolean;
  versionsError: string | null;
  allowUncurated: boolean;
  setAllowUncurated: (b: boolean) => void;
  prometheus: boolean;
  setPrometheus: (b: boolean) => void;
  grafana: boolean;
  setGrafana: (b: boolean) => void;
  tokensV2: boolean;
  setTokensV2: (b: boolean) => void;
  portBase: string;
  setPortBase: (s: string) => void;
  preflight: PreflightState;
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
  versionsLoading,
  versionsError,
  allowUncurated,
  setAllowUncurated,
  prometheus,
  setPrometheus,
  grafana,
  setGrafana,
  tokensV2,
  setTokensV2,
  portBase,
  setPortBase,
  preflight,
  onSubmit,
}: FormBodyProps) {
  return (
    <form onSubmit={onSubmit} style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 14 }}>
      <Field
        label="Name"
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

      <Field label="Splice version" hint="choose from the curated catalogue">
        <VersionPicker
          versions={versions}
          selected={version}
          onSelect={setVersion}
          loading={versionsLoading}
          error={versionsError}
        />
      </Field>

      <PreflightPanel state={preflight} />

      <label
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 10,
          padding: "10px 12px",
          background: W.surface2,
          borderRadius: 4,
          border: `1px solid ${prometheus ? W.brand : W.border}`,
          cursor: "pointer",
        }}
      >
        <input
          type="checkbox"
          checked={prometheus}
          onChange={(e) => setPrometheus(e.target.checked)}
          style={{ marginTop: 2 }}
          aria-label="Enable Prometheus"
        />
        <div style={{ flex: 1, fontSize: 12.5, color: W.text2 }}>
          <strong style={{ color: W.text }}>Enable Prometheus</strong>
          <span
            style={{
              marginLeft: 8,
              fontSize: 10.5,
              padding: "1px 6px",
              borderRadius: 2,
              background: `${tint(W.brand, 10)}`,
              color: W.brand,
              fontFamily: wMono,
            }}
          >
            metrics scrape
          </span>
          <div style={{ color: W.dim, fontSize: 11.5, marginTop: 3, lineHeight: 1.5 }}>
            Scrapes Canton + Splice JMX / admin metrics on a loopback-
            only host port. Adds ~150 MB image + ~100 MB RAM. Required
            for the Metrics screen's live charts (throughput, latency,
            ACS, errors). Equivalent to{" "}
            <code style={{ fontFamily: wMono }}>--profile prometheus</code>{" "}
            on the CLI.
          </div>
        </div>
      </label>

      <label
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 10,
          padding: "10px 12px",
          background: W.surface2,
          borderRadius: 4,
          border: `1px solid ${grafana ? W.brand : W.border}`,
          cursor: "pointer",
        }}
      >
        <input
          type="checkbox"
          checked={grafana}
          onChange={(e) => setGrafana(e.target.checked)}
          style={{ marginTop: 2 }}
          aria-label="Enable Grafana"
        />
        <div style={{ flex: 1, fontSize: 12.5, color: W.text2 }}>
          <strong style={{ color: W.text }}>Enable Grafana</strong>
          <span
            style={{
              marginLeft: 8,
              fontSize: 10.5,
              padding: "1px 6px",
              borderRadius: 2,
              background: `${tint(W.brand, 10)}`,
              color: W.brand,
              fontFamily: wMono,
            }}
          >
            dashboards
          </span>
          <div style={{ color: W.dim, fontSize: 11.5, marginTop: 3, lineHeight: 1.5 }}>
            Provisioned with the canton-localnet dashboard on a loopback-
            only host port. Adds ~250 MB image + ~200 MB RAM. Equivalent
            to{" "}
            <code style={{ fontFamily: wMono }}>--profile grafana</code>{" "}
            on the CLI.
          </div>
          {grafana && !prometheus && (
            <div
              role="status"
              style={{
                marginTop: 8,
                padding: "6px 8px",
                background: `${tint(W.warn, 8)}`,
                border: `1px solid ${W.warn}`,
                borderRadius: 2,
                fontSize: 11.5,
                color: W.warn,
                lineHeight: 1.5,
              }}
            >
              Grafana without Prometheus: the bundled dashboards will
              have no data source. Enable Prometheus too, or wire
              Grafana to an external scrape source manually after
              bring-up.
            </div>
          )}
        </div>
      </label>

      <label
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 10,
          padding: "10px 12px",
          background: W.surface2,
          borderRadius: 4,
          border: `1px solid ${tokensV2 ? W.brand : W.border}`,
          cursor: "pointer",
        }}
      >
        <input
          type="checkbox"
          checked={tokensV2}
          onChange={(e) => setTokensV2(e.target.checked)}
          style={{ marginTop: 2 }}
        />
        <div style={{ flex: 1, fontSize: 12.5, color: W.text2 }}>
          <strong style={{ color: W.text }}>Token Standard V2 (alpha)</strong>
          <span
            style={{
              marginLeft: 8,
              fontSize: 10.5,
              padding: "1px 6px",
              borderRadius: 2,
              background: `${tint(W.brand, 10)}`,
              color: W.brand,
              fontFamily: wMono,
            }}
          >
            protocol 35
          </span>
          <div style={{ color: W.dim, fontSize: 11.5, marginTop: 3, lineHeight: 1.5 }}>
            Injects the alpha-protocol Canton config for CIP-0112 token
            flows. Requires a V2-capable Splice version (e.g.{" "}
            <code style={{ fontFamily: wMono }}>token-standard-v2</code>). The
            instance settles at status <strong>partial</strong>. The V2
            splice healthcheck never reports healthy, but token flows work.
            Equivalent to{" "}
            <code style={{ fontFamily: wMono, marginLeft: 4 }}>
              --profile tokens-v2
            </code>{" "}
            on the CLI.
          </div>
        </div>
      </label>

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
          Advanced · uncurated versions
        </summary>
        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 8,
            padding: "8px 12px",
            background: W.surface2,
            borderRadius: 4,
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
              those bits. Experiments only.
            </div>
          </div>
        </label>
        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 8,
            padding: "8px 12px",
            background: W.surface2,
            borderRadius: 4,
          }}
        >
          <input
            type="number"
            min={1024}
            placeholder="auto"
            value={portBase}
            onChange={(e) => setPortBase(e.target.value)}
            style={{
              width: 88,
              fontFamily: wMono,
              fontVariantNumeric: "tabular-nums",
              fontSize: 12,
              padding: "4px 6px",
              background: W.surface,
              color: W.text,
              border: `1px solid ${W.border}`,
              borderRadius: R.control,
            }}
          />
          <div style={{ flex: 1, fontSize: 12, color: W.text2 }}>
            <strong>Fixed port base</strong>
            <div style={{ color: W.dim, fontSize: 11, marginTop: 2 }}>
              Pin deterministic host ports from this base (
              <code style={{ fontFamily: wMono }}>--port-base</code>) for
              reproducible multi-instance / CI layouts. Empty = auto-allocate.
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
          borderRadius: 2,
          fontFamily: wMono,
        }}
      >
        CLI equivalent:{" "}
        <span style={{ color: W.brand }}>
          dpm localnet up --name {name || "<name>"} --version{" "}
          {version || "latest"}
          {prometheus ? " --profile prometheus" : ""}
          {grafana ? " --profile grafana" : ""}
          {tokensV2 ? " --profile tokens-v2" : ""}
          {portBase.trim() ? ` --port-base ${portBase.trim()}` : ""}
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
            background: `${tint(W.warn, 10)}`,
            border: `1px solid ${tint(W.warn, 27)}`,
            color: W.warn,
            borderRadius: 2,
            padding: "6px 10px",
            fontSize: 11.5,
            margin: "6px 0",
          }}
        >
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <IcAlert size={12} /> {m}
          </span>
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
              borderRadius: 2,
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
        background: `${tint(W.err, 6)}`,
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

function BannerStripe({ banner }: { banner: ProgressState["banner"] }) {
  if (banner.kind === "running") {
    return (
      <div
        style={{
          padding: "8px 12px",
          background: `${tint(W.brand, 6)}`,
          color: W.brand,
          border: `1px solid ${tint(W.brand, 27)}`,
          borderRadius: 2,
          fontSize: 12,
          fontFamily: wMono,
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          <Dot color={W.brand} pulse /> streaming step events
        </span>
      </div>
    );
  }
  if (banner.kind === "done") {
    return (
      <div
        style={{
          padding: "10px 14px",
          background: `${tint(W.ok, 10)}`,
          color: W.ok,
          border: `1px solid ${W.ok}`,
          borderRadius: 2,
          fontSize: 13,
          fontWeight: 600,
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          <IcCheck size={12} /> {banner.detail || "ready"}
        </span>
      </div>
    );
  }
  if (banner.kind === "failed") {
    const remediation = remediationForCode(banner.errorCode);
    return (
      <div
        style={{
          padding: "10px 14px",
          background: `${tint(W.err, 6)}`,
          color: W.err,
          border: `1px solid ${W.err}`,
          borderRadius: 2,
          fontSize: 12.5,
        }}
      >
        <strong
          style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
        >
          <IcX size={12} /> {banner.summary ?? "failed"}
        </strong>
        {banner.cause && (
          <div style={{ color: W.text2, marginTop: 4, fontFamily: wMono, fontSize: 11 }}>
            {banner.cause}
          </div>
        )}
        {remediation && (
          <div
            style={{
              marginTop: 8,
              padding: "8px 10px",
              background: tint(W.warn, 8),
              borderRadius: R.control,
              color: W.text2,
              fontSize: 11.5,
            }}
          >
            <strong style={{ color: W.warn }}>{remediation.title}</strong>
            <ul style={{ margin: "4px 0 0", paddingLeft: 18, lineHeight: 1.55 }}>
              {remediation.steps.map((s, i) => (
                <li key={i}>{s}</li>
              ))}
            </ul>
          </div>
        )}
      </div>
    );
  }
  return (
    <div
      style={{
        padding: "10px 14px",
        background: `${tint(W.warn, 10)}`,
        color: W.warn,
        border: `1px solid ${W.warn}`,
        borderRadius: 2,
        fontSize: 12.5,
      }}
    >
      <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
        <IcStop size={12} /> Cancelled{banner.reason ? `. ${banner.reason}` : ""}
      </span>
    </div>
  );
}

function StepRow({ label, state }: { label: string; state: StepState }) {
  const icon = (() => {
    switch (state.status) {
      case "done":
        return <IcCheck size={12} style={{ color: W.ok }} />;
      case "active":
        return <Dot color={W.brand} pulse />;
      case "fail":
        return <IcX size={12} style={{ color: W.err }} />;
      default:
        return <Dot color={W.faint} />;
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
        borderBottom: `1px solid ${W.border}`,
        fontSize: 12.5,
      }}
    >
      <span
        style={{
          width: 14,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          height: 18,
          flexShrink: 0,
        }}
      >
        {icon}
      </span>
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

// Always a <select>, never free text: a textbox would route arbitrary
// tags to the upstream-resolution path the curated dropdown prevents.
// Sorted "latest" first, then descending semver.
export function VersionPicker({
  versions,
  selected,
  onSelect,
  loading = false,
  error = null,
}: {
  versions: SpliceVersionEntry[];
  selected: string;
  onSelect: (tag: string) => void;
  loading?: boolean;
  error?: string | null;
}) {
  if (versions.length === 0) {
    let placeholder = "No curated versions available";
    if (loading) placeholder = "Loading curated versions…";
    else if (error) placeholder = `Couldn't load versions: ${error}`;
    return (
      <select
        disabled
        value=""
        style={selectStyle}
        aria-label="Splice version"
        aria-busy={loading || undefined}
      >
        <option value="">{placeholder}</option>
      </select>
    );
  }

  const sorted = [...versions].sort((a, b) => {
    if (a.status === "latest" && b.status !== "latest") return -1;
    if (b.status === "latest" && a.status !== "latest") return 1;
    return compareSpliceTags(b.tag, a.tag);
  });

  return (
    <select
      value={selected}
      onChange={(e) => onSelect(e.target.value)}
      style={selectStyle}
      aria-label="Splice version"
    >
      {sorted.map((v) => (
        <option key={v.tag} value={v.tag}>
          {v.tag}
          {v.status === "latest" ? " (latest)" : ""}
          {v.major ? ` · major ${v.major}` : ""}
        </option>
      ))}
    </select>
  );
}

// Semver-aware ordering (negative ⇒ a older than b) so a release
// outranks its own pre-release; plain localeCompare sorts "0.6.4-rc.1"
// after "0.6.4". Non-semver tags fall back to numeric localeCompare.
export function compareSpliceTags(a: string, b: string): number {
  const pa = parseSemverTag(a);
  const pb = parseSemverTag(b);
  if (!pa || !pb) {
    return a.localeCompare(b, undefined, { numeric: true });
  }
  for (let i = 0; i < 3; i++) {
    if (pa.core[i] !== pb.core[i]) return pa.core[i] - pb.core[i];
  }
  // Same x.y.z: a release is newer than any pre-release.
  if (pa.pre === null && pb.pre === null) return 0;
  if (pa.pre === null) return 1;
  if (pb.pre === null) return -1;
  return comparePrerelease(pa.pre, pb.pre);
}

function parseSemverTag(tag: string): { core: [number, number, number]; pre: string | null } | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-(.+))?$/.exec(tag);
  if (!m) return null;
  return { core: [Number(m[1]), Number(m[2]), Number(m[3])], pre: m[4] ?? null };
}

// Semver pre-release precedence: dot-separated identifiers left-to-right,
// numeric ranked below alphanumeric, shorter run loses when otherwise equal.
function comparePrerelease(a: string, b: string): number {
  const as = a.split(".");
  const bs = b.split(".");
  const n = Math.max(as.length, bs.length);
  for (let i = 0; i < n; i++) {
    if (as[i] === undefined) return -1;
    if (bs[i] === undefined) return 1;
    const aNum = /^\d+$/.test(as[i]);
    const bNum = /^\d+$/.test(bs[i]);
    if (aNum && bNum) {
      const d = Number(as[i]) - Number(bs[i]);
      if (d !== 0) return d;
    } else if (aNum !== bNum) {
      return aNum ? -1 : 1; // numeric identifiers have lower precedence
    } else {
      const c = as[i].localeCompare(bs[i]);
      if (c !== 0) return c;
    }
  }
  return 0;
}

// Inlined, not spread from inputStyle (declared below): referencing it
// here would hit the ES-module TDZ. appearance:"auto" keeps the native
// dropdown caret some browsers drop with a custom border/background.
const selectStyle: React.CSSProperties = {
  width: "100%",
  background: W.bg,
  color: W.text,
  border: `1px solid ${W.border}`,
  borderRadius: R.control,
  padding: "7px 10px",
  fontSize: 13,
  fontFamily: wMono,
  fontVariantNumeric: "tabular-nums",
  outline: "none",
  cursor: "pointer",
  appearance: "auto",
};

function PreflightPanel({ state }: { state: PreflightState }) {
  if (state.kind === "idle") return null;
  if (state.kind === "loading") {
    return (
      <div
        style={{
          padding: "8px 12px",
          background: W.surface2,
          color: W.dim,
          border: `1px solid ${W.border}`,
          borderRadius: 2,
          fontSize: 11.5,
          fontFamily: wMono,
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          <Dot color={W.dim} pulse /> checking system requirements (docker
          memory · disk · daemon)…
        </span>
      </div>
    );
  }
  if (state.kind === "err") {
    return (
      <div
        style={{
          padding: "8px 12px",
          background: W.surface2,
          color: W.dim,
          border: `1px solid ${W.border}`,
          borderRadius: 2,
          fontSize: 11.5,
        }}
      >
        Pre-flight probe couldn't reach the server ({state.message}). The
        server-side gate still runs on submit.
      </div>
    );
  }
  const allChecks: { section: string; check: PreflightCheck }[] = [];
  for (const sec of state.report.sections) {
    for (const c of sec.checks) {
      allChecks.push({ section: sec.title, check: c });
    }
  }
  const fails = allChecks.filter((c) => c.check.result === "fail");
  const warns = allChecks.filter((c) => c.check.result === "warn");
  const blocked = state.kind === "blocked";
  const accent = blocked ? W.err : warns.length > 0 ? W.warn : W.ok;
  const headingIcon = blocked ? (
    <IcX size={12} />
  ) : warns.length > 0 ? (
    <IcAlert size={12} />
  ) : (
    <IcCheck size={12} />
  );
  const heading = blocked
    ? "Host doesn't meet this version's requirements"
    : warns.length > 0
    ? "Host meets minimums. Raise resources for headroom."
    : "Host is ready for this version";
  if (!blocked && warns.length === 0) {
    return (
      <div
        style={{
          padding: "6px 10px",
          background: `${tint(W.ok, 8)}`,
          color: W.ok,
          border: `1px solid ${tint(W.ok, 27)}`,
          borderRadius: 2,
          fontSize: 11.5,
          fontFamily: wMono,
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          {headingIcon} {heading} · {state.report.summary}
        </span>
      </div>
    );
  }
  return (
    <div
      role={blocked ? "alert" : undefined}
      style={{
        padding: "10px 12px",
        background: tint(accent, 6),
        border: `1px solid ${accent}`,
        borderRadius: R.control,
        fontSize: 12,
      }}
    >
      <div
        style={{
          color: accent,
          fontWeight: 600,
          marginBottom: 6,
          display: "flex",
          alignItems: "center",
          gap: 6,
        }}
      >
        {headingIcon} {heading}
      </div>
      {state.report.summary && (
        <div style={{ color: W.text2, marginBottom: 8, fontSize: 11.5 }}>
          {state.report.summary}
        </div>
      )}
      {[...fails, ...warns].map(({ section, check }, i) => (
        <div
          key={`${section}-${check.label}-${i}`}
          style={{
            marginTop: 6,
            padding: "6px 8px",
            background: W.bg,
            border: `1px solid ${W.border}`,
            borderRadius: 2,
          }}
        >
          <div style={{ fontFamily: wMono, fontSize: 11.5 }}>
            <span
              style={{
                color: check.result === "fail" ? W.err : W.warn,
                fontWeight: 600,
                marginRight: 6,
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
              }}
            >
              {check.result === "fail" ? (
                <IcX size={12} />
              ) : (
                <IcAlert size={12} />
              )}{" "}
              {check.label}
            </span>
            <span style={{ color: W.dim }}>· {section}</span>
          </div>
          {check.detail && (
            <div
              style={{
                color: W.text2,
                fontSize: 11,
                marginTop: 3,
                fontFamily: wMono,
              }}
            >
              {check.detail}
            </div>
          )}
          {check.remediation && check.remediation.length > 0 && (
            <ul
              style={{
                margin: "4px 0 0 0",
                paddingLeft: 18,
                color: W.text2,
                fontSize: 11,
                lineHeight: 1.5,
              }}
            >
              {check.remediation.map((r: string, j: number) => (
                <li key={j}>{r}</li>
              ))}
            </ul>
          )}
        </div>
      ))}
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
          color: W.dim,
          fontSize: 12,
          fontWeight: 500,
          marginBottom: 4,
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
  borderRadius: R.dialog,
  boxShadow: "0 10px 32px rgba(0,0,0,0.24)",
  overflow: "hidden",
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  background: W.bg,
  color: W.text,
  border: `1px solid ${W.border}`,
  borderRadius: R.control,
  padding: "7px 10px",
  fontSize: 13,
  fontFamily: wMono,
  fontVariantNumeric: "tabular-nums",
  outline: "none",
};
