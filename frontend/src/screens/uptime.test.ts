import { describe, expect, it } from "vitest";
import { formatUptime } from "./uptime";

const CREATED = "2026-07-27T08:46:00Z";
const at = (offsetMs: number) => Date.parse(CREATED) + offsetMs;
const MIN = 60_000;
const HOUR = 60 * MIN;

describe("formatUptime", () => {
  it("matches the backend's minute / hour / day buckets", () => {
    expect(formatUptime(CREATED, at(0))).toBe("0m");
    expect(formatUptime(CREATED, at(59 * MIN + 59_000))).toBe("59m");
    expect(formatUptime(CREATED, at(HOUR))).toBe("1h 0m");
    expect(formatUptime(CREATED, at(2 * HOUR + 5 * MIN + 30_000))).toBe("2h 5m");
    expect(formatUptime(CREATED, at(24 * HOUR - 1))).toBe("23h 59m");
    expect(formatUptime(CREATED, at(24 * HOUR))).toBe("1d");
    expect(formatUptime(CREATED, at(71 * HOUR))).toBe("2d");
  });

  it("returns empty for missing, unparseable, or future timestamps", () => {
    expect(formatUptime(undefined, at(HOUR))).toBe("");
    expect(formatUptime("", at(HOUR))).toBe("");
    expect(formatUptime("not-a-time", at(HOUR))).toBe("");
    expect(formatUptime(CREATED, at(-1))).toBe("");
  });

  it("accepts RFC3339 offsets", () => {
    expect(formatUptime("2026-07-27T10:46:00+02:00", at(90 * MIN))).toBe("1h 30m");
  });
});
