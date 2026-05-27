import { useMemo } from "react";
import type { Point } from "./types";
import { extent } from "./scale";

// Sparkline — the tiny line+area chart embedded inside each
// MetricCard. No axes, no tooltips, no legend; just shape +
// trend. Renders nothing when there's no data so the card layout
// holds even when the metric hasn't populated yet.

interface Props {
  points: Point[];
  color: string;
  width?: number;
  height?: number;
}

export function Sparkline({ points, color, width = 120, height = 30 }: Props) {
  const { path, area } = useMemo(() => {
    if (points.length < 2) return { path: "", area: "" };
    const ts = points.map((p) => p.t);
    const vs = points.map((p) => p.v);
    const xDomain = extent(ts);
    const yDomain = extent(vs);
    const xSpan = xDomain.max - xDomain.min || 1;
    const ySpan = yDomain.max - yDomain.min || 1;
    const xx = (t: number) => ((t - xDomain.min) / xSpan) * (width - 2) + 1;
    const yy = (v: number) =>
      height - 1 - ((v - yDomain.min) / ySpan) * (height - 2);
    const line = points
      .map(
        (p, i) =>
          `${i === 0 ? "M" : "L"}${xx(p.t).toFixed(1)},${yy(p.v).toFixed(1)}`,
      )
      .join(" ");
    const fill = `${line} L${xx(points[points.length - 1].t).toFixed(1)},${height - 1} L${xx(points[0].t).toFixed(1)},${height - 1} Z`;
    return { path: line, area: fill };
  }, [points, width, height]);

  if (!path) {
    return <svg width={width} height={height} aria-hidden="true" />;
  }
  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      style={{ display: "block" }}
    >
      <path d={area} fill={color} fillOpacity={0.18} />
      <path
        d={path}
        fill="none"
        stroke={color}
        strokeWidth={1.25}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
