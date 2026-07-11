import type { Point } from "./charts/types";
import { Sparkline } from "./charts/Sparkline";
import { IcArrowUp } from "./icons";
import { W, wMono, wideCaps, R, fs } from "../tokens";

// Headline number + delta vs prior window + inline sparkline.

export interface MetricCardProps {
  title: string;
  unit?: string;
  /** undefined → loading. */
  value: number | undefined;
  /** Delta vs prior window. undefined hides the badge. */
  delta?: number;
  deltaPolarity?: "up-is-good" | "down-is-good" | "neutral";
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
  sparklineColor = "#8FA3EE",
  error,
  format = defaultFormat,
}: MetricCardProps) {
  const hasValue = value !== undefined;
  const valueAvailable = hasValue && Number.isFinite(value);
  const deltaSign = delta === undefined ? 0 : Math.sign(delta);
  let deltaColor: string = W.dim;
  if (deltaSign !== 0 && deltaPolarity !== "neutral") {
    const good =
      (deltaSign > 0 && deltaPolarity === "up-is-good") ||
      (deltaSign < 0 && deltaPolarity === "down-is-good");
    deltaColor = good ? W.ok : W.err;
  }

  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 14,
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
          marginBottom: 6,
        }}
      >
        <span
          style={{
            ...wideCaps,
            color: W.dim,
            fontSize: fs.label,
          }}
        >
          {title}
        </span>
        {delta !== undefined && !error && (
          <span
            style={{
              fontFamily: wMono,
              fontSize: fs.label,
              fontVariantNumeric: "tabular-nums",
              color: deltaColor,
              fontWeight: 600,
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
            }}
          >
            {deltaSign > 0 ? (
              <IcArrowUp size={10} />
            ) : deltaSign < 0 ? (
              <IcArrowUp size={10} style={{ transform: "rotate(180deg)" }} />
            ) : (
              "—"
            )}
            {format(Math.abs(delta))}
            {unit && <span style={{ color: W.dim }}>{" " + unit}</span>}
          </span>
        )}
      </div>

      {error ? (
        <div style={{ color: W.err, fontSize: fs.meta }} role="alert">
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
            {!hasValue ? (
              <Skeleton width={70} height={22} />
            ) : !valueAvailable ? (
              <span
                style={{
                  color: W.text,
                  fontSize: fs.stat,
                  fontWeight: 600,
                  lineHeight: 1.1,
                  fontFamily: wMono,
                  fontVariantNumeric: "tabular-nums",
                }}
              >
                —
              </span>
            ) : (
              <>
                <span
                  style={{
                    color: W.text,
                    fontSize: fs.stat,
                    fontWeight: 600,
                    fontFamily: wMono,
                    fontVariantNumeric: "tabular-nums",
                    lineHeight: 1,
                  }}
                >
                  {format(value)}
                </span>
                {unit && (
                  <span style={{ color: W.dim, fontSize: fs.meta }}>{unit}</span>
                )}
              </>
            )}
          </div>
          <div style={{ marginTop: 8, height: 30 }}>
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
        borderRadius: R.control,
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
