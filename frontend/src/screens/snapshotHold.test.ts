import { describe, expect, it } from "vitest";
import type { ContainersResponse } from "../api";
import {
  SNAPSHOT_MAX_HOLD_MS,
  SNAPSHOT_MIN_HOLD_MS,
  SNAPSHOT_NO_PAUSE_MS,
  anyContainerPaused,
  observeSnapshot,
  snapshotHoldExpiresAt,
  startSnapshotHold,
} from "./snapshotHold";

function containers(...states: string[]): ContainersResponse {
  return {
    schema_version: 1,
    instance: "demo",
    containers: states.map((state, i) => ({
      name: `c${i}`,
      service: `s${i}`,
      state,
      status: state,
    })),
    healthy_count: 0,
    starting_count: 0,
    unhealthy_count: 0,
    restarting_count: 0,
    exited_count: 0,
  };
}

describe("snapshot hold", () => {
  it("detects a paused container in the poll", () => {
    expect(anyContainerPaused(null)).toBe(false);
    expect(anyContainerPaused(containers("running", "running"))).toBe(false);
    expect(anyContainerPaused(containers("running", "paused"))).toBe(true);
  });

  it("stays up while paused and finishes once the pause has ended", () => {
    let h = startSnapshotHold(1_000);
    h = observeSnapshot(h, false, 1_500);
    expect(h.done).toBe(false);
    h = observeSnapshot(h, true, 4_000);
    expect(h).toMatchObject({ sawPause: true, done: false });
    h = observeSnapshot(h, true, 60_000);
    expect(h.done).toBe(false);
    h = observeSnapshot(h, false, 63_000);
    expect(h.done).toBe(true);
  });

  it("ignores samples taken before the snapshot started", () => {
    const h = startSnapshotHold(10_000);
    expect(observeSnapshot(h, true, 9_999)).toBe(h);
    expect(observeSnapshot(h, false, 9_999 + SNAPSHOT_NO_PAUSE_MS * 2)).not.toBe(h);
  });

  it("waits out the no-pause window before giving up on an unseen pause", () => {
    const h = startSnapshotHold(0);
    expect(observeSnapshot(h, false, SNAPSHOT_NO_PAUSE_MS - 1).done).toBe(false);
    expect(observeSnapshot(h, false, SNAPSHOT_NO_PAUSE_MS).done).toBe(true);
  });

  it("returns the same object when nothing changed", () => {
    const h = observeSnapshot(startSnapshotHold(0), true, 100);
    expect(observeSnapshot(h, true, 200)).toBe(h);
    const done = observeSnapshot(h, false, 300);
    expect(observeSnapshot(done, true, 400)).toBe(done);
  });

  it("caps an unfinished hold and keeps a finished one up for the minimum", () => {
    const h = startSnapshotHold(5_000);
    expect(snapshotHoldExpiresAt(h)).toBe(5_000 + SNAPSHOT_MAX_HOLD_MS);
    const done = observeSnapshot(h, false, 5_000 + SNAPSHOT_NO_PAUSE_MS);
    expect(snapshotHoldExpiresAt(done)).toBe(5_000 + SNAPSHOT_MIN_HOLD_MS);
  });
});
