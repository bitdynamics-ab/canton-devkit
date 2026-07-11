import { useMemo } from "react";
import { W, wMono } from "../../tokens";
import { extent, niceTicks } from "./scale";

// BarChart — categorical bar chart. Used for per-template throughput,
// "top error sources", and other ranked lists. Bars are coloured per
// category so the chart reads as a heat-ranked horizontal scan.

export interface Bar {
  label: string;
  value: number;
  color?: string;
}

interface Props {
  bars: Bar[];
  width?: number;
  height?: number;
  defaultColor?: string;
  format?: (v: number) => string;
}

const PADDING = { top: 8, right: 12, bottom: 28, left: 100 };

export function BarChart({
  bars,
  width = 320,
  height,
  defaultColor = "#8FA3EE",
  format = (v) => (Math.abs(v) >= 1000 ? v.toFixed(0) : v.toFixed(1)),
}: Props) {
  // Default height grows with bar count so dense lists don't squish.
  const computedHeight = height ?? Math.max(120, bars.length * 22 + 36);
  const innerW = Math.max(1, width - PADDING.left - PADDING.right);
  const innerH = Math.max(1, computedHeight - PADDING.top - PADDING.bottom);

  const { x, yStep, yTicks } = useMemo(() => {
    const vDomain = extent(bars.map((b) => b.value));
    // Bars start at 0 by convention — make sure the X axis includes 0
    // even when all values are positive.
    const xDomain = {
      min: Math.min(0, vDomain.min),
      max: Math.max(0, vDomain.max),
    };
    const xSpan = xDomain.max - xDomain.min || 1;
    const xx = (v: number) => ((v - xDomain.min) / xSpan) * innerW;
    return {
      x: xx,
      yStep: bars.length > 0 ? innerH / bars.length : 0,
      yTicks: niceTicks(xDomain, 4),
    };
  }, [bars, innerW, innerH]);

  if (bars.length === 0) {
    return (
      <svg
        viewBox={`0 0 ${width} ${computedHeight}`}
        width="100%"
        height={computedHeight}
        role="img"
        aria-label="no bar data"
        style={{ display: "block" }}
      >
        <text
          x={width / 2}
          y={computedHeight / 2}
          textAnchor="middle"
          fontSize={11}
          fill={W.dim}
          fontFamily={wMono}
        >
          No data in this window
        </text>
      </svg>
    );
  }

  return (
    <svg
      role="img"
      aria-label={`${bars.length} bars · max ${format(Math.max(...bars.map((b) => b.value)))}`}
      viewBox={`0 0 ${width} ${computedHeight}`}
      width="100%"
      height={computedHeight}
      preserveAspectRatio="none"
      style={{ display: "block" }}
    >
      <g transform={`translate(${PADDING.left},${PADDING.top})`}>
        {/* X-axis tick lines + labels */}
        {yTicks.map((t, i) => (
          <g key={`x-${i}`}>
            <line
              x1={x(t)}
              x2={x(t)}
              y1={0}
              y2={innerH}
              stroke={W.border}
              strokeDasharray="2 3"
            />
            <text
              x={x(t)}
              y={innerH + 14}
              textAnchor="middle"
              fontSize={11}
              fill={W.dim}
              fontFamily={wMono}
            >
              {format(t)}
            </text>
          </g>
        ))}
        {bars.map((b, i) => {
          const w = Math.max(1, x(b.value) - x(0));
          const yy = i * yStep;
          const barH = Math.max(4, yStep - 4);
          return (
            <g key={b.label}>
              <text
                x={-8}
                y={yy + barH / 2 + 3}
                textAnchor="end"
                fontSize={11}
                fill={W.text2}
              >
                {b.label}
              </text>
              <rect
                x={x(0)}
                y={yy + 2}
                width={w}
                height={barH}
                fill={b.color ?? defaultColor}
                rx={2}
              />
              <text
                x={x(b.value) + 5}
                y={yy + barH / 2 + 3}
                fontSize={11}
                fill={W.text}
                fontFamily={wMono}
              >
                {format(b.value)}
              </text>
            </g>
          );
        })}
      </g>
    </svg>
  );
}
