// Shared types for the chart primitives in this directory.
//
// Production charts consume Prometheus's range-query response
// shape verbatim — see backend handleMetricsRange. We translate
// `result[].values[][unix-seconds, string-value]` into the typed
// `Point` shape exactly once at the API boundary so chart
// components don't have to know about the wire format.

export interface Point {
  /** Unix timestamp in milliseconds. */
  t: number;
  /** Numeric value at that instant. NaN is treated as "no sample". */
  v: number;
}

export interface Series {
  /** Human-facing label rendered in the legend + tooltip. */
  label: string;
  /** Stroke colour (hex or CSS colour token). */
  color: string;
  /** Time-ordered points; chart components assume ascending t. */
  points: Point[];
}

/**
 * Decode Prometheus's `/api/v1/query_range` response into a list of
 * Series. Skips empty results, coerces string values to numbers, and
 * drops non-finite samples (Prometheus can emit "NaN" for quantiles
 * when the source histogram lacks finite buckets).
 *
 * Shape we accept (subset Prometheus emits):
 * ```
 * { status: "success",
 *   data: {
 *     resultType: "matrix",
 *     result: [
 *       { metric: { __name__, labels… }, values: [[t, "v"], …] }
 *     ]
 *   }
 * }
 * ```
 *
 * Used by every chart component instead of letting each one
 * re-derive the projection.
 */
export interface PrometheusRangeResponse {
  status: string;
  data?: {
    resultType?: string;
    result?: Array<{
      metric?: Record<string, string>;
      values?: Array<[number, string]>;
    }>;
  };
}

export function decodePrometheusRange(
  resp: PrometheusRangeResponse,
  labelFn: (m: Record<string, string>) => string = (m) =>
    m.__name__ ?? m.job ?? "value",
  colorFn: (i: number) => string = (i) => CHART_PALETTE[i % CHART_PALETTE.length],
): Series[] {
  const result = resp?.data?.result ?? [];
  return result.map((row, i) => ({
    label: labelFn(row.metric ?? {}),
    color: colorFn(i),
    points: (row.values ?? [])
      .map(([t, v]) => ({
        t: t * 1000, // Prometheus returns seconds; chart wants ms.
        v: Number(v),
      }))
      .filter((p) => Number.isFinite(p.v)),
  }));
}

// Curated chart palette. Keeps a chart with 6+ series readable —
// neighbouring lines never share the same hue family. Picked to be
// accessible on the project's dark surface tokens.
export const CHART_PALETTE = [
  "#7CB5F7", // blue
  "#5BD7C5", // teal
  "#C4A8F5", // purple
  "#F5BF55", // amber
  "#F08FB5", // pink
  "#62E2A0", // green
  "#E8A14E", // orange
];
