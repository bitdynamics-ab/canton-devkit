import { describe, expect, it } from "vitest";
import type { Series } from "../components/charts/types";
import {
  HEATMAP_NO_FINITE_BUCKETS_NOTE,
  heatmapCellsOrNote,
} from "./MetricsScreen";

const series = (label: string, values: number[]): Series => ({
  label,
  color: "#000",
  points: values.map((v, i) => ({ t: i * 60_000, v })),
});

describe("heatmapCellsOrNote", () => {
  it("returns a note when the histogram exports only the +Inf bucket (Splice 0.6.4)", () => {
    // Regression: with only le="+Inf", the old code mapped every sample to
    // the >2s row, implying all latency is >2s. Now we refuse to render a
    // misleading grid and explain why instead.
    const out = heatmapCellsOrNote([series("+Inf", [10, 20, 30])]);
    expect(out).toEqual({ note: HEATMAP_NO_FINITE_BUCKETS_NOTE });
    expect("cells" in out).toBe(false);
  });

  it("returns the note when there are no buckets at all", () => {
    expect(heatmapCellsOrNote([])).toEqual({
      note: HEATMAP_NO_FINITE_BUCKETS_NOTE,
    });
  });

  it("builds cells on the correct rows once finite buckets are present", () => {
    const out = heatmapCellsOrNote([
      series("0.005", [1]), // -> row 0 (<5ms)
      series("0.1", [2]), //   -> row 2 (<100ms)
      series("+Inf", [4]), //  -> row 5 (>2s)
    ]);
    expect("cells" in out).toBe(true);
    if ("cells" in out) {
      expect(out.cells.map((c) => c.r).sort((a, b) => a - b)).toEqual([0, 2, 5]);
      // intensity is normalised against the max sample (4)
      expect(out.cells.find((c) => c.r === 5)?.i).toBeCloseTo(1);
      expect(out.cells.find((c) => c.r === 0)?.i).toBeCloseTo(0.25);
    }
  });
});
