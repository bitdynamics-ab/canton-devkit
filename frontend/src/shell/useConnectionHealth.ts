import { useEffect, useState } from "react";
import { SCHEMA_VERSION, fetchVersion } from "../api";

// useConnectionHealth polls /api/version every `intervalMs` ms
// after a successful initial handshake. Surfaces three states the
// topbar pill renders distinctly:
//
//   - "ok"        : last poll succeeded and schema matched
//   - "mismatch"  : server is up but speaks a different schema
//                   (a binary swap mid-session — uncommon but
//                    real; bundle stays loaded but every fetch
//                    is now unsafe to interpret)
//   - "offline"   : last poll errored (network / server gone)
//
// We deliberately keep the BootGate's hard schema-version refusal
// for first paint; this hook just colour-codes the topbar after
// the app is already running. Tearing the whole UI down on a
// transient blip would be more disruptive than the colored dot
// the user can ignore until the next deliberate reload.
//
// 10s default poll matches the SSE heartbeat cadence (#42) — same
// "is the binary alive" question, different transport. We do NOT
// use SSE here because that would mask the case where the SSE
// stream stays open but /api/version 5xx's (separate handler,
// separate failure mode worth surfacing).
export type ConnectionHealth = "ok" | "mismatch" | "offline";

export interface ConnectionState {
  health: ConnectionHealth;
  serverVersion: number | null;
}

export function useConnectionHealth(intervalMs = 10_000): ConnectionState {
  const [state, setState] = useState<ConnectionState>({
    health: "ok",
    serverVersion: SCHEMA_VERSION,
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const poll = async () => {
      try {
        const v = await fetchVersion();
        if (cancelled) return;
        setState({
          health: v.schema_version === SCHEMA_VERSION ? "ok" : "mismatch",
          serverVersion: v.schema_version,
        });
      } catch {
        if (cancelled) return;
        // Keep last-known serverVersion — useful for the tooltip
        // ("last seen v1") even while offline.
        setState((prev) => ({ ...prev, health: "offline" }));
      } finally {
        if (!cancelled) {
          timer = setTimeout(poll, intervalMs);
        }
      }
    };

    timer = setTimeout(poll, intervalMs);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [intervalMs]);

  return state;
}
