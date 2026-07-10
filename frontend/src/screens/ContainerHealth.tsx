import { useEffect, useState } from "react";
import {
  ApiError,
  type ContainersResponse,
  fetchContainers,
  restartContainer,
} from "../api";
import { W, wMono, tableCaps, tint } from "../tokens";
import { Button } from "../components/Button";
import { Dot, IcRefresh } from "../components/icons";
import { ContainerLogsModal } from "./ContainerLogsModal";

// ContainerHealth — live per-container status panel. Polls
// /api/instances/{name}/containers every POLL_MS so the user sees
// real-time docker truth ("is canton in a restart loop, or did
// postgres crash?") instead of the coarse registry status enum.
//
// Renders nothing when the instance has no docker project (backend
// returns 503) — the InstanceDetail card stays usable.

const POLL_MS = 3000;

export function ContainerHealth({ name }: { name: string }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: ContainersResponse }
    | { kind: "err"; message: string; status: number }
    | { kind: "absent" } // 503 — no docker project / daemon down
  >({ kind: "loading" });
  // Selected container for the logs modal. Null = closed.
  const [logsOpen, setLogsOpen] = useState<string | null>(null);
  // Containers with a restart in flight; a Set so rapid clicks on
  // different rows each show their own pending state.
  const [restarting, setRestarting] = useState<Set<string>>(new Set());
  const [restartErr, setRestartErr] = useState<string | null>(null);

  async function onRestart(container: string) {
    if (!confirm(`Restart ${container}? Container will be stopped + started; in-flight requests may drop.`)) {
      return;
    }
    setRestarting((s) => new Set([...s, container]));
    setRestartErr(null);
    try {
      await restartContainer(name, container);
      // The poll loop picks up the new status; no manual refresh needed.
    } catch (e) {
      setRestartErr(
        `Restart ${container} failed: ` +
          (e instanceof ApiError ? e.message : "unknown error"),
      );
    } finally {
      setRestarting((s) => {
        const next = new Set(s);
        next.delete(container);
        return next;
      });
    }
  }

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    async function poll() {
      try {
        const r = await fetchContainers(name);
        if (!cancelled) setState({ kind: "ok", data: r });
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError) {
          if (e.status === 503) {
            setState({ kind: "absent" });
          } else {
            setState({ kind: "err", message: e.message, status: e.status });
          }
        } else {
          setState({ kind: "err", message: "failed to probe docker", status: 0 });
        }
      } finally {
        if (!cancelled) {
          timer = setTimeout(poll, POLL_MS);
        }
      }
    }

    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [name]);

  if (state.kind === "absent") return null;

  return (
    <section
      style={{
        marginTop: 16,
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        padding: 14,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: 12,
          marginBottom: 10,
        }}
      >
        <div style={{ fontWeight: 600, fontSize: 13, color: W.text }}>
          Container health
        </div>
        <code style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>
          live · polled every {POLL_MS / 1000}s
        </code>
        <span style={{ marginLeft: "auto" }} />
        {state.kind === "ok" && (
          <SummaryPills counts={state.data} />
        )}
      </header>

      {state.kind === "loading" && (
        <div style={{ color: W.dim, fontSize: 12 }}>Querying docker…</div>
      )}

      {state.kind === "err" && (
        <div
          role="alert"
          style={{
            color: W.err,
            background: `${tint(W.err, 6)}`,
            border: `1px solid ${W.err}`,
            borderRadius: 2,
            padding: "6px 10px",
            fontSize: 12,
          }}
        >
          Docker probe failed: {state.message}
        </div>
      )}

      {restartErr && (
        <div
          role="alert"
          style={{
            color: W.err,
            background: `${tint(W.err, 6)}`,
            border: `1px solid ${W.err}`,
            borderRadius: 2,
            padding: "6px 10px",
            fontSize: 12,
            marginBottom: 8,
          }}
        >
          {restartErr}
        </div>
      )}

      {state.kind === "ok" && (
        <ContainersTable
          containers={state.data.containers}
          onPickLogs={(c) => setLogsOpen(c)}
          onRestart={onRestart}
          restarting={restarting}
        />
      )}

      <ContainerLogsModal
        open={logsOpen !== null}
        instance={name}
        container={logsOpen ?? ""}
        onClose={() => setLogsOpen(null)}
      />
    </section>
  );
}

// Dense-panel micro-labels: sentence case, muted — wide caps are
// reserved for real table/card headers.
// Table column headers use the quiet caps cut — match every other
// table in the app.
const colHeader: React.CSSProperties = {
  ...tableCaps,
  color: W.dim,
  fontSize: 11,
};

function ContainersTable({
  containers,
  onPickLogs,
  onRestart,
  restarting,
}: {
  containers: ContainersResponse["containers"];
  onPickLogs: (name: string) => void;
  onRestart: (name: string) => void;
  restarting: Set<string>;
}) {
  if (containers.length === 0) {
    return (
      <div style={{ color: W.dim, fontSize: 12, padding: "8px 0" }}>
        No containers found for this compose project.
      </div>
    );
  }
  // Failure-mode rows sort to the top.
  const sorted = [...containers].sort((a, b) => severity(a) - severity(b));
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr 1fr auto auto",
        gap: "4px 12px",
        fontSize: 11.5,
        fontFamily: wMono,
        alignItems: "center",
      }}
    >
      <div style={colHeader} aria-hidden />
      <div style={colHeader}>Service</div>
      <div style={colHeader}>State</div>
      <div style={colHeader}>Status</div>
      <div style={{ ...colHeader, textAlign: "right" }}>Actions</div>
      {sorted.map((c) => {
        const color = signalFor(c);
        const onLogs = (e: React.MouseEvent) => {
          // Don't let the opening click double as a backdrop click on
          // the modal overlay (which would close it immediately).
          e.stopPropagation();
          onPickLogs(c.name);
        };
        const onRestartClick = (e: React.MouseEvent) => {
          e.stopPropagation();
          onRestart(c.name);
        };
        const isRestarting = restarting.has(c.name);
        // display:contents rows can't carry click handlers, so each
        // cell gets its own onClick; the restart cell stops propagation
        // so the button doesn't also open the logs modal.
        const cellBase: React.CSSProperties = {
          cursor: "pointer",
          padding: "2px 0",
        };
        return (
          <div
            key={c.name}
            style={{ display: "contents" }}
            title={`Click columns to view logs for ${c.name}`}
          >
            <div
              onClick={onLogs}
              style={{
                ...cellBase,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              <Dot color={color} />
            </div>
            <div onClick={onLogs} style={{ ...cellBase, color: W.text, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", textDecoration: "underline", textDecorationColor: W.faint, textDecorationStyle: "dotted", textUnderlineOffset: 2 }}>
              {c.service}
            </div>
            <div onClick={onLogs} style={{ ...cellBase, color: W.text2 }}>
              <span style={{ color }}>{c.state}</span>
              {c.health && (
                <span style={{ color: W.dim }}> · {c.health}</span>
              )}
            </div>
            <div onClick={onLogs} style={{ ...cellBase, color: W.dim, fontSize: 10.5 }}>{c.status}</div>
            <div style={{ display: "flex", justifyContent: "flex-end" }}>
              <Button
                variant="ghost"
                size="sm"
                icon={<IcRefresh />}
                onClick={onRestartClick}
                disabled={isRestarting}
                title={`Restart ${c.name} (docker restart)`}
              >
                {isRestarting ? "restarting…" : "restart"}
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function SummaryPills({ counts }: { counts: ContainersResponse }) {
  const pills: Array<[number, string, string]> = [
    [counts.healthy_count, "healthy", W.ok],
    [counts.starting_count, "starting", W.brand],
    [counts.unhealthy_count, "unhealthy", W.err],
    [counts.restarting_count, "restarting", W.warn],
    [counts.exited_count, "exited", W.dim],
  ];
  return (
    <div style={{ display: "flex", gap: 6 }}>
      {pills
        .filter(([n]) => n > 0)
        .map(([n, label, color]) => (
          <span
            key={label}
            style={{
              padding: "2px 8px",
              borderRadius: 2,
              border: `1px solid ${color}`,
              background: `${color}1A`,
              color,
              fontSize: 10.5,
              fontFamily: wMono,
            }}
          >
            {n} {label}
          </span>
        ))}
    </div>
  );
}

// severity orders rows so failure-mode containers come first
// (lower sorts earlier).
function severity(c: { state: string; health?: string }): number {
  if (c.state === "restarting") return 0;
  if (c.state === "dead" || c.state === "exited") return 1;
  if (c.health === "unhealthy") return 2;
  if (c.health === "starting") return 3;
  if (c.state === "paused") return 4;
  return 5; // healthy / running with no healthcheck
}

// signalFor maps a container's docker state/health to its status-dot
// color (the state word next to it carries the same color).
function signalFor(c: { state: string; health?: string }): string {
  if (c.state === "restarting") return W.warn;
  if (c.state === "dead" || c.state === "exited") return W.err;
  if (c.state === "paused") return W.dim;
  if (c.health === "unhealthy") return W.err;
  if (c.health === "starting") return W.brand;
  if (c.health === "healthy") return W.ok;
  // running with no healthcheck
  if (c.state === "running") return W.ok;
  return W.dim;
}
