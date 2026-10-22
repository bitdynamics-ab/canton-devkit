import type { ContainersResponse } from "../api";

export const SNAPSHOT_MIN_HOLD_MS = 3_000;
export const SNAPSHOT_NO_PAUSE_MS = 8_000;
export const SNAPSHOT_MAX_HOLD_MS = 10 * 60_000;

export interface SnapshotHold {
  startedAt: number;
  sawPause: boolean;
  done: boolean;
}

export function startSnapshotHold(now: number): SnapshotHold {
  return { startedAt: now, sawPause: false, done: false };
}

export function anyContainerPaused(r: ContainersResponse | null): boolean {
  return r?.containers.some((c) => c.state === "paused") ?? false;
}

export function observeSnapshot(
  hold: SnapshotHold,
  paused: boolean,
  sampledAt: number,
): SnapshotHold {
  if (hold.done || sampledAt < hold.startedAt) return hold;
  if (paused) return hold.sawPause ? hold : { ...hold, sawPause: true };
  if (hold.sawPause || sampledAt - hold.startedAt >= SNAPSHOT_NO_PAUSE_MS) {
    return { ...hold, done: true };
  }
  return hold;
}

export function snapshotHoldExpiresAt(hold: SnapshotHold): number {
  return (
    hold.startedAt + (hold.done ? SNAPSHOT_MIN_HOLD_MS : SNAPSHOT_MAX_HOLD_MS)
  );
}
