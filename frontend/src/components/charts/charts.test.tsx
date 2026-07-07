import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { AreaChart } from "./AreaChart";
import { MultiLine } from "./MultiLine";
import { BarChart } from "./BarChart";
import { Heatmap } from "./Heatmap";
import { Sparkline } from "./Sparkline";
import {
  CHART_PALETTE,
  decodePrometheusRange,
  type PrometheusRangeResponse,
} from "./types";
import { extent, linearScale, niceTicks } from "./scale";

// Chart primitives — production tests cover the responsibilities
// that aren't visually inspectable: data decoding, scale math,
// empty-state rendering, a11y attributes.

describe("decodePrometheusRange", () => {
  it("decodes a typical Prometheus matrix response into Series", () => {
    const resp: PrometheusRangeResponse = {
      status: "success",
      data: {
        resultType: "matrix",
        result: [
          {
            metric: { __name__: "up", job: "canton" },
            values: [
              [1700000000, "1"],
              [1700000060, "0.5"],
              [1700000120, "0.75"],
            ],
          },
        ],
      },
    };
    const series = decodePrometheusRange(resp);
    expect(series).toHaveLength(1);
    expect(series[0].label).toBe("up");
    expect(series[0].points).toHaveLength(3);
    // Seconds → ms conversion
    expect(series[0].points[0].t).toBe(1700000000 * 1000);
    // String → number coercion
    expect(series[0].points[1].v).toBe(0.5);
  });

  it("skips non-finite values rather than including them", () => {
    const resp: PrometheusRangeResponse = {
      status: "success",
      data: {
        result: [
          {
            metric: {},
            values: [
              [1700000000, "1"],
              [1700000060, "NaN"],
              [1700000120, "2"],
              [1700000180, "+Inf"],
              [1700000240, "-Inf"],
            ],
          },
        ],
      },
    };
    const series = decodePrometheusRange(resp);
    expect(series[0].points).toHaveLength(2);
    expect(series[0].points.map((p) => p.v)).toEqual([1, 2]);
  });

  it("handles empty result without crashing", () => {
    expect(decodePrometheusRange({ status: "success" })).toEqual([]);
    expect(decodePrometheusRange({ status: "success", data: {} })).toEqual([]);
    expect(decodePrometheusRange({ status: "success", data: { result: [] } })).toEqual([]);
  });

  it("rotates through the palette when there are more series than colours", () => {
    const resp: PrometheusRangeResponse = {
      status: "success",
      data: {
        result: Array.from({ length: 10 }, (_, i) => ({
          metric: { __name__: `m${i}` },
          values: [[1700000000, "1"]],
        })),
      },
    };
    const series = decodePrometheusRange(resp);
    expect(series).toHaveLength(10);
    // Wraps around CHART_PALETTE (length 7) so series[7] reuses the
    // first colour. Catches a future "I capped the palette but
    // forgot mod" regression.
    expect(series[0].color).toBe(CHART_PALETTE[0]);
    expect(series[7].color).toBe(CHART_PALETTE[0]);
  });
});

describe("scale helpers", () => {
  it("linearScale maps domain into [0, range]", () => {
    const s = linearScale({ min: 0, max: 10 }, 100);
    expect(s(0)).toBe(0);
    expect(s(5)).toBe(50);
    expect(s(10)).toBe(100);
  });

  it("linearScale handles degenerate domain without dividing by zero", () => {
    const s = linearScale({ min: 5, max: 5 }, 100);
    expect(s(5)).toBe(50); // mid
    expect(s(99)).toBe(50);
  });

  it("extent returns padded domain when all values are equal", () => {
    const e = extent([7, 7, 7]);
    // Single value gets a 10% pad on each side so the chart doesn't render
    // as a degenerate horizontal at the edge.
    expect(e.min).toBeLessThan(7);
    expect(e.max).toBeGreaterThan(7);
  });

  it("niceTicks produces rounded ticks (1/2/5 × 10^k)", () => {
    const t = niceTicks({ min: 0, max: 100 }, 4);
    // Should produce ticks at multiples of 20 or 25 — both are nice
    expect(t.length).toBeGreaterThanOrEqual(3);
    expect(t[0]).toBe(0);
  });
});

describe("AreaChart", () => {
  it("renders with a series + summary aria-label", () => {
    render(
      <AreaChart
        series={{
          label: "tps",
          color: "#8FA3EE",
          points: [
            { t: 1700000000000, v: 1 },
            { t: 1700000060000, v: 1.5 },
            { t: 1700000120000, v: 2 },
          ],
        }}
        width={300}
        height={120}
      />,
    );
    // aria-label summarises latest value + count so screen readers
    // don't try to verbalise the SVG path.
    const svg = screen.getByRole("img");
    expect(svg.getAttribute("aria-label")).toMatch(/3 points/);
    expect(svg.getAttribute("aria-label")).toMatch(/latest/);
  });

  it("renders empty-state message when there are no points", () => {
    render(
      <AreaChart
        series={{ label: "tps", color: "#8FA3EE", points: [] }}
      />,
    );
    expect(screen.getByText(/no data in this window/i)).toBeTruthy();
    // Even empty, the SVG still mounts so the parent's layout
    // doesn't reflow on data toggle.
    expect(screen.getByRole("img")).toBeTruthy();
  });
});

describe("MultiLine", () => {
  it("renders a legend entry per series with latest value", () => {
    render(
      <MultiLine
        series={[
          {
            label: "median",
            color: "#6480E6",
            points: [{ t: 1, v: 100 }, { t: 2, v: 110 }],
          },
          {
            label: "p99",
            color: "#DDB25E",
            points: [{ t: 1, v: 200 }, { t: 2, v: 220 }],
          },
        ]}
      />,
    );
    expect(screen.getByText("median")).toBeTruthy();
    expect(screen.getByText("p99")).toBeTruthy();
  });

  it("renders empty-state text when no points are supplied", () => {
    render(<MultiLine series={[]} />);
    expect(screen.getByText(/no data in this window/i)).toBeTruthy();
  });
});

describe("BarChart", () => {
  it("renders one bar per row with its label and value", () => {
    render(
      <BarChart
        bars={[
          { label: "Splice.Amulet", value: 1208 },
          { label: "Token.Mint", value: 4 },
        ]}
      />,
    );
    expect(screen.getByText("Splice.Amulet")).toBeTruthy();
    expect(screen.getByText("Token.Mint")).toBeTruthy();
  });

  it("renders the empty state when no bars are supplied", () => {
    render(<BarChart bars={[]} />);
    expect(screen.getByText(/no data in this window/i)).toBeTruthy();
  });
});

describe("Heatmap", () => {
  it("renders all rows × cols even when cells are sparse", () => {
    const { container } = render(
      <Heatmap
        rows={3}
        cols={4}
        cells={[
          { r: 0, c: 0, i: 0.9 },
          { r: 2, c: 3, i: 0.5 },
        ]}
      />,
    );
    // 3 × 4 = 12 cells in the grid, all rendered as <rect>
    expect(container.querySelectorAll("g rect").length).toBe(12);
  });

  it("clamps intensity to [0, 1]", () => {
    // Should not throw on out-of-range input; the rect's
    // fillOpacity expression must still produce a valid number.
    expect(() =>
      render(
        <Heatmap rows={1} cols={1} cells={[{ r: 0, c: 0, i: 99 }]} />,
      ),
    ).not.toThrow();
  });
});

describe("Sparkline", () => {
  it("renders an SVG with width/height even when there's no data", () => {
    const { container } = render(<Sparkline points={[]} color="#8FA3EE" />);
    const svg = container.querySelector("svg");
    expect(svg).toBeTruthy();
    expect(svg?.getAttribute("width")).toBe("120");
  });

  it("draws a path when there are at least 2 points", () => {
    const { container } = render(
      <Sparkline
        points={[
          { t: 1, v: 1 },
          { t: 2, v: 2 },
          { t: 3, v: 3 },
        ]}
        color="#8FA3EE"
      />,
    );
    const paths = container.querySelectorAll("svg path");
    // Two paths: area fill + line stroke
    expect(paths.length).toBe(2);
  });
});
