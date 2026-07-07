import { useEffect, useRef, useState } from "react";
import { ApiError, fetchContainerLogs } from "../api";
import { W, wMono, wSans } from "../tokens";
import { Button } from "../components/Button";
import { IcX } from "../components/icons";

// ContainerLogsModal — opens when the user clicks a row in
// ContainerHealth. Polls docker logs for the selected container at
// LOG_POLL_MS and renders them in a terminal-styled <pre>. Tail size
// and since duration are toolbar-tunable; auto-scroll-to-bottom is on
// by default but disabled once the user scrolls up.

const LOG_POLL_MS = 3000;

interface Props {
  open: boolean;
  instance: string;
  container: string;
  onClose: () => void;
}

export function ContainerLogsModal({ open, instance, container, onClose }: Props) {
  const [tail, setTail] = useState(200);
  const [since, setSince] = useState("");
  const [body, setBody] = useState<string>("");
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const preRef = useRef<HTMLPreElement>(null);
  const autoScrollRef = useRef(true);
  // Only close when both mousedown AND click originated on the overlay;
  // otherwise the click that opened the modal (mousedown on a row cell,
  // mouseup after the modal mounted) would immediately close it.
  const downOnOverlayRef = useRef(false);

  // Esc closes.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    async function poll() {
      setLoading(true);
      try {
        const text = await fetchContainerLogs(instance, container, {
          tail,
          since: since || undefined,
        });
        if (cancelled) return;
        setBody(text);
        setErr(null);
      } catch (e) {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : "failed to fetch logs");
      } finally {
        if (cancelled) return;
        setLoading(false);
        timer = setTimeout(poll, LOG_POLL_MS);
      }
    }
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [open, instance, container, tail, since]);

  // Auto-scroll to bottom on new content unless the user scrolled up;
  // tracked via a ref to avoid a state update per scroll event.
  useEffect(() => {
    if (!preRef.current || !autoScrollRef.current) return;
    preRef.current.scrollTop = preRef.current.scrollHeight;
  }, [body]);

  function onScroll() {
    if (!preRef.current) return;
    const el = preRef.current;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 20;
    autoScrollRef.current = atBottom;
  }

  if (!open) return null;
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`Logs for ${container}`}
      onMouseDown={(e) => {
        downOnOverlayRef.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget && downOnOverlayRef.current) {
          onClose();
        }
      }}
      style={overlayStyle}
    >
      <div onMouseDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()} style={modalStyle}>
        <header style={headerStyle}>
          <div>
            <div style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
              Logs · <code style={{ color: W.brand, fontFamily: wMono }}>{container}</code>
            </div>
            <div style={{ color: W.dim, fontSize: 11.5, marginTop: 2, fontFamily: wMono }}>
              {instance} · polled every {LOG_POLL_MS / 1000}s {loading && "· refreshing"}
            </div>
          </div>
          <span style={{ flex: 1 }} />
          <Toolbar
            tail={tail}
            setTail={setTail}
            since={since}
            setSince={setSince}
          />
          <Button
            variant="ghost"
            icon={<IcX />}
            onClick={onClose}
            title="Close (Esc)"
            aria-label="Close logs"
          />
        </header>

        {err && (
          <div
            role="alert"
            style={{
              color: W.err,
              background: `${W.err}10`,
              borderBottom: `1px solid ${W.err}`,
              padding: "8px 16px",
              fontSize: 12,
            }}
          >
            {err}
          </div>
        )}

        <pre
          ref={preRef}
          onScroll={onScroll}
          style={{
            margin: 0,
            padding: "10px 14px",
            background: W.bg,
            color: W.text2,
            fontFamily: wMono,
            fontSize: 11,
            lineHeight: 1.55,
            whiteSpace: "pre-wrap",
            wordBreak: "break-all",
            overflow: "auto",
            flex: 1,
            minHeight: 0,
          }}
        >
          {body || (loading ? "Loading…" : "(no log output yet)")}
        </pre>
      </div>
    </div>
  );
}

function Toolbar({
  tail,
  setTail,
  since,
  setSince,
}: {
  tail: number;
  setTail: (n: number) => void;
  since: string;
  setSince: (s: string) => void;
}) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <label style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>
        tail
        <select
          value={tail}
          onChange={(e) => setTail(parseInt(e.target.value, 10))}
          style={selectStyle}
        >
          {[50, 100, 200, 500, 1000, 2000].map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <label style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>
        since
        <select
          value={since}
          onChange={(e) => setSince(e.target.value)}
          style={selectStyle}
        >
          <option value="">all</option>
          <option value="30s">30s</option>
          <option value="1m">1m</option>
          <option value="5m">5m</option>
          <option value="15m">15m</option>
          <option value="1h">1h</option>
        </select>
      </label>
    </div>
  );
}

const overlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  background: "rgba(0,0,0,0.6)",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  zIndex: 200,
  fontFamily: wSans,
};

const modalStyle: React.CSSProperties = {
  width: "min(900px, 95vw)",
  height: "min(700px, 88vh)",
  background: W.surface,
  border: `1px solid ${W.border}`,
  borderRadius: 4,
  boxShadow: "0 24px 64px rgba(0,0,0,0.6)",
  display: "flex",
  flexDirection: "column",
  overflow: "hidden",
};

const headerStyle: React.CSSProperties = {
  padding: "12px 16px",
  borderBottom: `1px solid ${W.border}`,
  display: "flex",
  alignItems: "center",
  gap: 12,
};

const selectStyle: React.CSSProperties = {
  background: W.bg,
  color: W.text,
  border: `1px solid ${W.border}`,
  borderRadius: 2,
  padding: "2px 6px",
  fontSize: 11,
  fontFamily: wMono,
  marginLeft: 4,
};
