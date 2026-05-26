import { useEffect, useState } from "react";
import {
  ApiError,
  type ContainersResponse,
  fetchContainers,
} from "../api";
import { W, wMono } from "../tokens";

// ContainerHealth — live per-container status panel. Polls
// /api/instances/{name}/containers every POLL_MS so the user
// sees real-time docker truth (e.g. "canton: Up 27 seconds
// (healthy)" — recently restarted) instead of the coarse
// registry status enum (running / failed / etc).
//
// Answers the user's question: "is canton in a restart loop,
// or is splice just slow on first init, or did postgres crash?"
// The registry can't tell you any of that — docker can.
//
// Renders nothing when the instance has no docker project
// (returns 503 from the backend) — the InstanceDetail card
// stays usable; the panel just doesn't show.

const POLL_MS = 3000;

export function ContainerHealth({ name }: { name: string }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: ContainersResponse }
    | { kind: "err"; message: string; status: number }
    | { kind: "absent" } // 503 — no docker project / daemon down
  >({ kind: "loading" });

  // Poll loop. Restarts when name changes; tears down on
  // unmount via the cleanup closure.
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
        borderRadius: 10,
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
            background: `${W.err}10`,
            border: `1px solid ${W.err}`,
            borderRadius: 6,
            padding: "6px 10px",
            fontSize: 12,
          }}
        >
          Docker probe failed: {state.message}
        </div>
      )}

      {state.kind === "ok" && (
        <ContainersTable containers={state.data.containers} />
      )}
    </section>
  );
}

function ContainersTable({
  containers,
}: {
  containers: ContainersResponse["containers"];
}) {
  if (containers.length === 0) {
    return (
      <div style={{ color: W.dim, fontSize: 12, padding: "8px 0" }}>
        No containers found for this compose project.
      </div>
    );
  }
  // Stable sort: unhealthy/restarting first, then starting, then
  // healthy/running. Makes the failure-mode rows pop to the top.
  const sorted = [...containers].sort((a, b) => {
    return severity(a) - severity(b);
  });
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr 1fr auto",
        gap: "4px 12px",
        fontSize: 11.5,
        fontFamily: wMono,
        alignItems: "center",
      }}
    >
      <div style={{ color: W.dim, fontSize: 10, letterSpacing: 0.5, textTransform: "uppercase" }}>
        ●
      </div>
      <div style={{ color: W.dim, fontSize: 10, letterSpacing: 0.5, textTransform: "uppercase" }}>
        service
      </div>
      <div style={{ color: W.dim, fontSize: 10, letterSpacing: 0.5, textTransform: "uppercase" }}>
        state
      </div>
      <div style={{ color: W.dim, fontSize: 10, letterSpacing: 0.5, textTransform: "uppercase" }}>
        status
      </div>
      {sorted.map((c) => {
        const { color, glyph } = signalFor(c);
        return (
          <div key={c.name} style={{ display: "contents" }}>
            <div style={{ color, fontSize: 14, textAlign: "center" }}>{glyph}</div>
            <div style={{ color: W.text, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {c.service}
            </div>
            <div style={{ color: W.text2 }}>
              <span style={{ color }}>{c.state}</span>
              {c.health && (
                <span style={{ color: W.dim }}> · {c.health}</span>
              )}
            </div>
            <div style={{ color: W.dim, fontSize: 10.5 }}>{c.status}</div>
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
              borderRadius: 999,
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

// severity orders rows so failure-mode containers come first.
// Lower number = higher priority (sorts earlier).
function severity(c: { state: string; health?: string }): number {
  if (c.state === "restarting") return 0;
  if (c.state === "dead" || c.state === "exited") return 1;
  if (c.health === "unhealthy") return 2;
  if (c.health === "starting") return 3;
  if (c.state === "paused") return 4;
  return 5; // healthy / running with no healthcheck
}

function signalFor(c: { state: string; health?: string }): {
  color: string;
  glyph: string;
} {
  if (c.state === "restarting") return { color: W.warn, glyph: "↻" };
  if (c.state === "dead" || c.state === "exited") return { color: W.err, glyph: "✕" };
  if (c.state === "paused") return { color: W.dim, glyph: "⏸" };
  if (c.health === "unhealthy") return { color: W.err, glyph: "⊗" };
  if (c.health === "starting") return { color: W.brand, glyph: "●" };
  if (c.health === "healthy") return { color: W.ok, glyph: "✓" };
  // running with no healthcheck
  if (c.state === "running") return { color: W.ok, glyph: "●" };
  return { color: W.dim, glyph: "·" };
}
