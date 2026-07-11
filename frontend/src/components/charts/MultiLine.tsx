import { useMemo, useState } from "react";
import { W, wMono, fs } from "../../tokens";
import type { Series } from "./types";
import { extent, linearScale, niceTicks } from "./scale";

// MultiLine — N series sharing one Y axis. Hover shows a vertical
// guide + a tooltip listing every series's value at that timestamp.
// Used for "median vs p99" latency, per-template throughput, etc.

interface Props {
  series: Series[];
  width?: number;
  height?: number;
  format?: (v: number) => string;
  yLabel?: string;
  showLegend?: boolean;
}

const PADDING = { top: 8, right: 12, bottom: 28, left: 38 };

export function MultiLine({
  series,
  width = 320,
  height = 160,
  format = defaultFormat,
  yLabel,
  showLegend = true,
}: Props) {
  const legendH = showLegend ? 20 : 0;
  const chartH = height - legendH;
  const innerW = Math.max(1, width - PADDING.left - PADDING.right);
  const innerH = Math.max(1, chartH - PADDING.top - PADDING.bottom);
  const allPoints = series.flatMap((s) => s.points);
  const hasData = allPoints.length > 0;

  const { x, y, xTicks, yTicks } = useMemo(() => {
    if (!hasData) {
      return {
        x: (_: number) => 0,
        y: (_: number) => 0,
        xTicks: [] as number[],
        yTicks: [] as number[],
      };
    }
    const xDomain = extent(allPoints.map((p) => p.t));
    const vDomain = extent(allPoints.map((p) => p.v));
    const yDomain = {
      min: Math.min(0, vDomain.min),
      max: vDomain.max + (vDomain.max - vDomain.min) * 0.1,
    };
    return {
      x: linearScaleFn(xDomain.min, xDomain.max, innerW),
      y: (v: number) => innerH - linearScale(yDomain, innerH)(v),
      xTicks: [xDomain.min, (xDomain.min + xDomain.max) / 2, xDomain.max],
      yTicks: niceTicks(yDomain, 4),
    };
  }, [series, allPoints, hasData, innerW, innerH]);

  const [hoverT, setHoverT] = useState<number | null>(null);

  const onMove = (e: React.MouseEvent<SVGRectElement>) => {
    if (!hasData) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    // Map mx back to a t-domain value; pick the timestamp closest to that.
    const tsAll = series.flatMap((s) => s.points.map((p) => p.t));
    const tMin = Math.min(...tsAll);
    const tMax = Math.max(...tsAll);
    const targetT = tMin + (mx / innerW) * (tMax - tMin);
    setHoverT(targetT);
  };

  const summary = hasData
    ? `${series.length} series · ${series.map((s) => s.label).join(", ")}`
    : "no data";

  return (
    <div>
      <svg
        role="img"
        aria-label={summary}
        viewBox={`0 0 ${width} ${chartH}`}
        width="100%"
        height={chartH}
        preserveAspectRatio="none"
        style={{ display: "block" }}
      >
        <g transform={`translate(${PADDING.left},${PADDING.top})`}>
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
                  strokeDasharray="2 3"
                />
                <text
                  x={-6}
                  y={yy + 3}
                  textAnchor="end"
                  fontSize={fs.caption}
                  fill={W.dim}
                  fontFamily={wMono}
                >
                  {format(t)}
                </text>
              </g>
            );
          })}
          {xTicks.map((t, i) => (
            <text
              key={`x-${i}`}
              x={x(t)}
              y={innerH + 14}
              textAnchor={
                i === 0 ? "start" : i === xTicks.length - 1 ? "end" : "middle"
              }
              fontSize={fs.caption}
              fill={W.dim}
              fontFamily={wMono}
            >
              {fmtTime(t)}
            </text>
          ))}

          {hasData &&
            series.map((s) => {
              const path = s.points
                .map(
                  (p, i) =>
                    `${i === 0 ? "M" : "L"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`,
                )
                .join(" ");
              return (
                <path
                  key={s.label}
                  d={path}
                  fill="none"
                  stroke={s.color}
                  strokeWidth={1.5}
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
              );
            })}

          {hoverT !== null && hasData && (
            <line
              x1={x(hoverT)}
              x2={x(hoverT)}
              y1={0}
              y2={innerH}
              stroke={W.dim}
              strokeDasharray="2 3"
            />
          )}

          {!hasData && (
            <text
              x={innerW / 2}
              y={innerH / 2}
              textAnchor="middle"
              fontSize={fs.caption}
              fill={W.dim}
              fontFamily={wMono}
            >
              No data in this window
            </text>
          )}

          <rect
            x={0}
            y={0}
            width={innerW}
            height={innerH}
            fill="transparent"
            onMouseMove={onMove}
            onMouseLeave={() => setHoverT(null)}
          />
        </g>
      </svg>

      {showLegend && (
        <div
          style={{
            display: "flex",
            gap: 14,
            flexWrap: "wrap",
            padding: "4px 12px 0",
            fontSize: fs.small,
            fontFamily: wMono,
            color: W.text2,
          }}
        >
          {series.map((s) => {
            const last = s.points[s.points.length - 1]?.v;
            return (
              <span
                key={s.label}
                style={{ display: "flex", alignItems: "center", gap: 5 }}
              >
                <span
                  style={{
                    width: 9,
                    height: 2,
                    background: s.color,
                    display: "inline-block",
                  }}
                />
                <span style={{ color: W.dim }}>{s.label}</span>
                {last !== undefined && (
                  <span style={{ color: W.text }}>
                    {format(last)}
                    {yLabel ? " " + yLabel : ""}
                  </span>
                )}
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
}

function linearScaleFn(min: number, max: number, range: number) {
  if (max === min) return () => range / 2;
  const k = range / (max - min);
  return (v: number) => (v - min) * k;
}

function defaultFormat(v: number): string {
  if (!Number.isFinite(v)) return "—";
  if (Math.abs(v) >= 1000) return v.toFixed(0);
  if (Math.abs(v) >= 10) return v.toFixed(1);
  return v.toFixed(2);
}

function fmtTime(ms: number): string {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
