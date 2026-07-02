import { useEffect, useState } from "react";
import { SCHEMA_VERSION, fetchVersion } from "../api";

// useConnectionHealth polls /api/version every `intervalMs` ms and
// surfaces three states for the topbar pill:
//
//   - "ok"       : last poll succeeded and schema matched
//   - "mismatch" : server up but speaks a different schema (binary
//                  swapped mid-session; every fetch is now unsafe
//                  to interpret)
//   - "offline"  : last poll errored (network / server gone)
//
// The BootGate keeps its hard schema refusal for first paint; this
// hook only colour-codes the topbar afterwards — tearing the UI down
// on a transient blip would be more disruptive than a coloured dot.
// Polling (not SSE) so a 5xx from /api/version surfaces even while an
// SSE stream stays open — separate handler, separate failure mode.
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
