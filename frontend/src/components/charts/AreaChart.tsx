import { useMemo, useState } from "react";
import { W, wMono } from "../../tokens";
import type { Point, Series } from "./types";
import { extent, linearScale, niceTicks } from "./scale";

// AreaChart — single-series filled area + line, with axes,
// gridlines, hover tooltip, and an empty state.
//
// Design constraints:
//   - Pure SVG (no chart library; this whole component compresses
//     to <2 KB after build).
//   - Accessible: role="img" + aria-label summarising the data so
//     screen readers don't read out the underlying SVG points.
//   - Hover via a single transparent rect listener; we map mouse
//     X back to the nearest point's index in O(log n) (bisect).
//   - Empty state ("No data in this window") rendered inside the
//     chart's own viewport so layout never reflows when data
//     toggles between empty and non-empty.

interface Props {
  series: Series;
  /** Width in px; charts inside a flex parent should pass 100% via outer style and a real px width here for the SVG viewBox. */
  width?: number;
  height?: number;
  /** Optional value formatter for the tooltip (default: 2 decimals). */
  format?: (v: number) => string;
  /** Optional Y-axis label (e.g. "tx/s", "ms"). */
  yLabel?: string;
}

const PADDING = { top: 8, right: 12, bottom: 22, left: 38 };

export function AreaChart({
  series,
  width = 320,
  height = 120,
  format = defaultFormat,
  yLabel,
}: Props) {
  const innerW = Math.max(1, width - PADDING.left - PADDING.right);
  const innerH = Math.max(1, height - PADDING.top - PADDING.bottom);
  const hasData = series.points.length > 0;

  const { x, y, xTicks, yTicks } = useMemo(() => {
    if (!hasData) {
      return {
        x: (_: number) => 0,
        y: (_: number) => 0,
        xTicks: [] as number[],
        yTicks: [] as number[],
      };
    }
    const ts = series.points.map((p) => p.t);
    const vs = series.points.map((p) => p.v);
    const xDomain = extent(ts);
    const vDomain = extent(vs);
    // Pad Y top by 10% so the line never touches the chart's top
    // edge — the curated chart palette reads as cramped without it.
    const yDomain = {
      min: Math.min(0, vDomain.min),
      max: vDomain.max + (vDomain.max - vDomain.min) * 0.1,
    };
    const xScale = linearScale(xDomain, innerW);
    const yScale = linearScale(yDomain, innerH);
    return {
      x: (t: number) => xScale(t),
      y: (v: number) => innerH - yScale(v),
      xTicks: [xDomain.min, (xDomain.min + xDomain.max) / 2, xDomain.max],
      yTicks: niceTicks(yDomain, 4),
    };
  }, [series, innerW, innerH, hasData]);

  const linePath = useMemo(
    () =>
      series.points
        .map((p, i) => `${i === 0 ? "M" : "L"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`)
        .join(" "),
    [series.points, x, y],
  );
  const areaPath = useMemo(
    () =>
      hasData
        ? `${linePath} L${x(series.points[series.points.length - 1].t).toFixed(1)},${innerH} L${x(series.points[0].t).toFixed(1)},${innerH} Z`
        : "",
    [linePath, series.points, x, innerH, hasData],
  );

  const [hover, setHover] = useState<{ p: Point; sx: number; sy: number } | null>(
    null,
  );

  const onMove = (e: React.MouseEvent<SVGRectElement>) => {
    if (!hasData) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const i = nearestIndex(series.points, x, mx);
    const p = series.points[i];
    setHover({ p, sx: x(p.t), sy: y(p.v) });
  };

  const summary = hasData
    ? `${series.label} — ${series.points.length} points; latest ${format(series.points[series.points.length - 1].v)}${yLabel ? " " + yLabel : ""}`
    : `${series.label} — no data`;

  return (
    <svg
      role="img"
      aria-label={summary}
      viewBox={`0 0 ${width} ${height}`}
      width="100%"
      height={height}
      preserveAspectRatio="none"
      style={{ display: "block" }}
    >
      <defs>
        <linearGradient id={`area-${series.label}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={series.color} stopOpacity={0.32} />
          <stop offset="100%" stopColor={series.color} stopOpacity={0} />
        </linearGradient>
      </defs>

      <g transform={`translate(${PADDING.left},${PADDING.top})`}>
        {/* Y gridlines + tick labels */}
        {yTicks.map((t, i) => {
          const yy = y(t);
          return (
            <g key={`y-${i}`}>
              <line
                x1={0}
                x2={innerW}
                y1={yy}
                y2={yy}
                stroke={W.border}
                strokeWidth={1}
                strokeDasharray="2 3"
              />
              <text
                x={-6}
                y={yy + 3}
                textAnchor="end"
                fontSize={9}
                fill={W.dim}
                fontFamily={wMono}
              >
                {format(t)}
              </text>
            </g>
          );
        })}
        {/* X tick labels — time HH:MM:SS */}
        {xTicks.map((t, i) => (
          <text
            key={`x-${i}`}
            x={x(t)}
            y={innerH + 14}
            textAnchor={i === 0 ? "start" : i === xTicks.length - 1 ? "end" : "middle"}
            fontSize={9}
            fill={W.dim}
            fontFamily={wMono}
          >
            {fmtTime(t)}
          </text>
        ))}

        {hasData ? (
          <>
            <path d={areaPath} fill={`url(#area-${series.label})`} />
            <path
              d={linePath}
              fill="none"
              stroke={series.color}
              strokeWidth={1.5}
              strokeLinejoin="round"
              strokeLinecap="round"
            />
            {hover && (
              <>
                <line
                  x1={hover.sx}
                  x2={hover.sx}
                  y1={0}
                  y2={innerH}
                  stroke={W.dim}
                  strokeWidth={1}
                  strokeDasharray="2 3"
                />
                <circle cx={hover.sx} cy={hover.sy} r={3.5} fill={series.color} />
              </>
            )}
            {/* Transparent rect captures every pixel for hover */}
            <rect
              x={0}
              y={0}
              width={innerW}
              height={innerH}
              fill="transparent"
              onMouseMove={onMove}
              onMouseLeave={() => setHover(null)}
            />
          </>
        ) : (
          <text
            x={innerW / 2}
            y={innerH / 2}
            textAnchor="middle"
            fontSize={11}
            fill={W.dim}
            fontFamily={wMono}
          >
            No data in this window
          </text>
        )}
      </g>

      {hover && (
        <foreignObject
          x={Math.min(width - 130, Math.max(0, hover.sx + PADDING.left - 60))}
          y={Math.max(0, hover.sy + PADDING.top - 38)}
          width={130}
          height={32}
          style={{ pointerEvents: "none" }}
        >
          <div
            style={{
              background: W.surface,
              border: `1px solid ${W.border}`,
              borderRadius: 2,
              padding: "3px 7px",
              fontFamily: wMono,
              fontSize: 10.5,
              color: W.text,
              textAlign: "center",
            }}
          >
            <div>
              {format(hover.p.v)}
              {yLabel ? " " + yLabel : ""}
            </div>
            <div style={{ color: W.dim, fontSize: 9 }}>{fmtTime(hover.p.t)}</div>
          </div>
        </foreignObject>
      )}
    </svg>
  );
}

function defaultFormat(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const abs = Math.abs(v);
  if (abs >= 1000) return v.toFixed(0);
  if (abs >= 10) return v.toFixed(1);
  return v.toFixed(2);
}

function fmtTime(ms: number): string {
  const d = new Date(ms);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

/**
 * Binary search for the point whose mapped X is closest to `targetX`.
 * Assumes `points` is t-ascending (always true for Prometheus range
 * results).
 */
function nearestIndex(
  points: Point[],
  x: (t: number) => number,
  targetX: number,
): number {
  let lo = 0;
  let hi = points.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (x(points[mid].t) < targetX) lo = mid + 1;
    else hi = mid;
  }
  if (lo > 0) {
    const a = Math.abs(x(points[lo - 1].t) - targetX);
    const b = Math.abs(x(points[lo].t) - targetX);
    if (a < b) lo = lo - 1;
  }
  return lo;
}
