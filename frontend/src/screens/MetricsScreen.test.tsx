import { describe, expect, it } from "vitest";
import { Q } from "./MetricsScreen";

describe("Q.cmdLatency", () => {
  // Guards the command-latency panel that replaced the (never-populating on
  // 0.6.x) submit-to-commit heatmap: it must be a real mean (sum/count) in
  // ms per node — NOT a histogram_quantile, which is NaN on 0.6.x's
  // +Inf-only histogram.
  it("is a per-node sum/count average in ms, not a percentile", () => {
    const q = Q.cmdLatency;
    const base = "daml_participant_api_commands_submissions_duration_seconds";
    expect(q).toContain(`${base}_sum`);
    expect(q).toContain(`${base}_count`);
    expect(q).toContain("by (node)");
    expect(q).toContain("/"); // a ratio
    expect(q.startsWith("1000")).toBe(true); // seconds -> ms
    expect(q).not.toContain("histogram_quantile");
  });
});
