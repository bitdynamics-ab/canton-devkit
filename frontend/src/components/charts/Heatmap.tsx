import { useMemo } from "react";
import { W, wMono } from "../../tokens";

// Heatmap — 2D density grid. Rows are e.g. latency buckets;
// columns are time buckets. Cells coloured by intensity 0..1.
// Used for "submit-to-commit heatmap" in the Metrics screen.

export interface Cell {
  /** Row index (0..rows-1). */
  r: number;
  /** Column index (0..cols-1). */
  c: number;
  /** Intensity in [0, 1]; values outside are clamped. */
  i: number;
}

interface Props {
  rows: number;
  cols: number;
  cells: Cell[];
  rowLabels?: string[];
  colLabels?: string[];
  width?: number;
  height?: number;
  color?: string;
}

const PADDING = { top: 8, right: 12, bottom: 22, left: 60 };

export function Heatmap({
  rows,
  cols,
  cells,
  rowLabels,
  colLabels,
  width = 320,
  height = 160,
  color = "#8FA3EE",
}: Props) {
  const innerW = Math.max(1, width - PADDING.left - PADDING.right);
  const innerH = Math.max(1, height - PADDING.top - PADDING.bottom);
  const cellW = innerW / cols;
  const cellH = innerH / rows;
  const cellMap = useMemo(() => {
    const m = new Map<string, number>();
    for (const c of cells) {
      m.set(`${c.r}:${c.c}`, Math.max(0, Math.min(1, c.i)));
    }
    return m;
  }, [cells]);

  const hasData = cells.length > 0;
  if (!hasData) {
    return (
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        role="img"
        aria-label="no heatmap data"
        style={{ display: "block" }}
      >
        <text
          x={width / 2}
          y={height / 2}
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
      aria-label={`heatmap · ${rows}×${cols}`}
      viewBox={`0 0 ${width} ${height}`}
      width="100%"
      height={height}
      preserveAspectRatio="none"
      style={{ display: "block" }}
    >
      <g transform={`translate(${PADDING.left},${PADDING.top})`}>
        {Array.from({ length: rows }).map((_, r) =>
          Array.from({ length: cols }).map((_, c) => {
            const intensity = cellMap.get(`${r}:${c}`) ?? 0;
            return (
              <rect
                key={`${r}-${c}`}
                x={c * cellW}
                y={r * cellH}
                width={Math.max(1, cellW - 1)}
                height={Math.max(1, cellH - 1)}
                fill={color}
                fillOpacity={0.06 + intensity * 0.84}
                rx={1}
              />
            );
          }),
        )}
        {rowLabels?.map((lbl, r) => (
          <text
            key={`rl-${r}`}
            x={-6}
            y={r * cellH + cellH / 2 + 3}
            textAnchor="end"
            fontSize={11}
            fill={W.dim}
            fontFamily={wMono}
          >
            {lbl}
          </text>
        ))}
        {colLabels?.map((lbl, c) => {
          if (c !== 0 && c !== cols - 1 && c !== Math.floor(cols / 2)) return null;
          return (
            <text
              key={`cl-${c}`}
              x={c * cellW + cellW / 2}
              y={innerH + 14}
              textAnchor={c === 0 ? "start" : c === cols - 1 ? "end" : "middle"}
              fontSize={11}
              fill={W.dim}
              fontFamily={wMono}
            >
              {lbl}
            </text>
          );
        })}
      </g>
    </svg>
  );
}
