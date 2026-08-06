// Pairs a colored dot with a text label so color is never the only cue.

import type { CSSProperties } from "react";
import { W, tint, R, fs } from "../tokens";
import { Dot } from "./icons";

type Tone = "ok" | "warn" | "danger" | "muted" | "busy";

const MAP: Record<string, { label: string; tone: Tone }> = {
  running: { label: "Running", tone: "ok" },
  // Deliberate transient operations (brand-toned so they read as "in
  // progress", not a fault). `snapshotting` overrides the paused-container
  // "partial" the reconciler would otherwise show mid-backup.
  snapshotting: { label: "Snapshotting…", tone: "busy" },
  healthy: { label: "Healthy", tone: "ok" },
  ready: { label: "Ready", tone: "ok" },
  stopped: { label: "Stopped", tone: "muted" },
  exited: { label: "Exited", tone: "muted" },
  creating: { label: "Creating", tone: "warn" },
  starting: { label: "Starting", tone: "warn" },
  stopping: { label: "Stopping", tone: "warn" },
  restarting: { label: "Restarting", tone: "warn" },
  partial: { label: "Partial", tone: "warn" },
  paused: { label: "Paused", tone: "warn" },
  stalled: { label: "Stalled", tone: "warn" },
  failed: { label: "Failed", tone: "danger" },
  error: { label: "Error", tone: "danger" },
  dead: { label: "Dead", tone: "danger" },
  live: { label: "Live", tone: "ok" },
  reconnecting: { label: "Reconnecting", tone: "warn" },
  truncated: { label: "Truncated", tone: "warn" },
  idle: { label: "Idle", tone: "muted" },
};

function toneColor(tone: Tone): string {
  return tone === "ok"
    ? W.ok
    : tone === "warn"
      ? W.warn
      : tone === "danger"
        ? W.err
        : tone === "busy"
          ? W.brand
          : W.dim;
}

function resolve(status: string): { label: string; color: string } {
  const hit = MAP[status.toLowerCase()];
  if (hit) return { label: hit.label, color: toneColor(hit.tone) };
  const label = status.charAt(0).toUpperCase() + status.slice(1);
  return { label, color: W.dim };
}

interface StatusBadgeProps {
  status: string;
  /** "text" = dot + label; "pill" = bordered tinted chip. */
  variant?: "text" | "pill";
  pulse?: boolean;
  style?: CSSProperties;
}

export function StatusBadge({
  status,
  variant = "text",
  pulse = false,
  style,
}: StatusBadgeProps) {
  const { label, color } = resolve(status);
  if (variant === "pill") {
    return (
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          height: 20,
          padding: "0 8px",
          borderRadius: R.control,
          border: `1px solid ${tint(color, 34)}`,
          background: tint(color, 13),
          color,
          fontSize: fs.label,
          fontWeight: 500,
          ...style,
        }}
      >
        <Dot color={color} size={6} pulse={pulse} />
        {label}
      </span>
    );
  }
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        color,
        fontSize: fs.meta,
        ...style,
      }}
    >
      <Dot color={color} size={6} pulse={pulse} />
      {label}
    </span>
  );
}
