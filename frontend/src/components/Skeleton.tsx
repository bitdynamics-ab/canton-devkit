// Loading placeholders shaped like the real table to avoid layout shift.

import { useEffect, useState, type CSSProperties } from "react";
import { W, R } from "../tokens";

// Delays true until `ms` so a fast fetch never flashes a skeleton.
export function useLoadingDelay(active: boolean, ms = 160): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!active) {
      setShown(false);
      return;
    }
    const t = window.setTimeout(() => setShown(true), ms);
    return () => window.clearTimeout(t);
  }, [active, ms]);
  return shown;
}

export function SkeletonBar({
  width = "100%",
  height = 12,
  style,
}: {
  width?: number | string;
  height?: number;
  style?: CSSProperties;
}) {
  return (
    <span
      aria-hidden
      style={{
        display: "inline-block",
        width,
        height,
        borderRadius: R.control,
        background: `linear-gradient(90deg, ${W.surface2} 25%, ${W.rowHover} 50%, ${W.surface2} 75%)`,
        backgroundSize: "220% 100%",
        animation: "cdk-shimmer 1.4s ease-in-out infinite",
        ...style,
      }}
    />
  );
}

// Pass the real table's column widths so the skeleton lines up.
export function SkeletonTable({
  columns,
  rows = 4,
  rowHeight = 38,
  label = "Loading",
}: {
  columns: (number | string)[];
  rows?: number;
  rowHeight?: number;
  label?: string;
}) {
  return (
    <div role="status" aria-label={label} style={{ width: "100%" }}>
      {Array.from({ length: rows }).map((_, r) => (
        <div
          key={r}
          style={{
            display: "flex",
            gap: 16,
            alignItems: "center",
            height: rowHeight,
            padding: "0 12px",
            borderBottom: r < rows - 1 ? `1px solid ${W.border}` : undefined,
          }}
        >
          {columns.map((w, c) => (
            <div key={c} style={{ flex: typeof w === "number" ? w : `0 0 ${w}` }}>
              <SkeletonBar
                width={c === 0 ? "62%" : "48%"}
                height={11}
                style={{ opacity: 1 - r * 0.12 }}
              />
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
