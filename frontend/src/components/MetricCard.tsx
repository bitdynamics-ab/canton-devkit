import type { Point } from "./charts/types";
import { Sparkline } from "./charts/Sparkline";
import { W, wMono } from "../tokens";

// MetricCard — the 4-up strip at the top of the Metrics screen.
// One headline number + a delta vs the prior window + an inline
// sparkline so the value reads against its trend.
//
// Loading and error states are first-class — when the upstream
// PromQL fetch is in flight the card shows a skeleton; when it
// fails the card shows the error without taking down the whole
// grid.

export interface MetricCardProps {
  title: string;
  unit?: string;
  /** Current value (the big number). undefined → loading. */
  value: number | undefined;
  /** Delta vs prior window. undefined hides the badge. */
  delta?: number;
  /** "up arrow good" or "down arrow good" — affects delta colour. */
  deltaPolarity?: "up-is-good" | "down-is-good" | "neutral";
  /** Tiny chart embedded in the card. */
  sparkline?: Point[];
  sparklineColor?: string;
  /** When set, replaces the value + sparkline with the error message. */
  error?: string;
  format?: (v: number) => string;
}

export function MetricCard({
  title,
  unit,
  value,
  delta,
  deltaPolarity = "up-is-good",
  sparkline,
  sparklineColor = "#7CB5F7",
  error,
  format = defaultFormat,
}: MetricCardProps) {
  const deltaSign = delta === undefined ? 0 : Math.sign(delta);
  let deltaColor: string = W.dim;
  if (deltaSign !== 0 && deltaPolarity !== "neutral") {
    const good =
      (deltaSign > 0 && deltaPolarity === "up-is-good") ||
      (deltaSign < 0 && deltaPolarity === "down-is-good");
    deltaColor = good ? "#62E2A0" : "#F08FB5";
  }

  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        padding: 14,
        display: "flex",
        flexDirection: "column",
        gap: 6,
        minWidth: 0,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
        }}
      >
        <span
          style={{
            color: W.dim,
            fontSize: 11,
            letterSpacing: 0.4,
            textTransform: "uppercase",
            fontWeight: 600,
          }}
        >
          {title}
        </span>
        {delta !== undefined && !error && (
          <span
            style={{
              fontFamily: wMono,
              fontSize: 11,
              color: deltaColor,
              fontWeight: 600,
            }}
          >
            {deltaSign > 0 ? "▲" : deltaSign < 0 ? "▼" : "—"}{" "}
            {format(Math.abs(delta))}
            {unit && <span style={{ color: W.dim }}>{" " + unit}</span>}
          </span>
        )}
      </div>

      {error ? (
        <div style={{ color: "#F08FB5", fontSize: 12 }} role="alert">
          {error}
        </div>
      ) : (
        <>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: 6,
              minHeight: 28,
            }}
          >
            {value === undefined ? (
              <Skeleton width={70} height={22} />
            ) : (
              <>
                <span
                  style={{
                    color: W.text,
                    fontSize: 26,
                    fontWeight: 600,
                    fontFamily: wMono,
                    lineHeight: 1,
                  }}
                >
                  {format(value)}
                </span>
                {unit && (
                  <span style={{ color: W.dim, fontSize: 12 }}>{unit}</span>
                )}
              </>
            )}
          </div>
          <div style={{ marginTop: 4, height: 30 }}>
            {sparkline ? (
              <Sparkline points={sparkline} color={sparklineColor} />
            ) : (
              <Skeleton width="100%" height={30} />
            )}
          </div>
        </>
      )}
    </div>
  );
}

function Skeleton({
  width,
  height,
}: {
  width: number | string;
  height: number;
}) {
  return (
    <div
      aria-hidden="true"
      style={{
        width,
        height,
        background: W.border,
        borderRadius: 4,
        opacity: 0.4,
      }}
    />
  );
}

function defaultFormat(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const abs = Math.abs(v);
  if (abs >= 1000) return v.toFixed(0);
  if (abs >= 10) return v.toFixed(1);
  return v.toFixed(2);
}
