// StatusBadge — the one renderer for instance / container / connection
// status across the console. Before this, the same status datum showed
// up four different ways (a lowercase dot+enum in the table, plain mono
// text in the detail grid, a Title-Case pill in the topbar, a bare
// color-only dot in the ACS). One renderer fixes the inconsistency and
// guarantees color is never the ONLY cue — the label carries the
// meaning for the colorblind / auditor audience.

import type { CSSProperties } from "react";
import { W, tint, R } from "../tokens";
import { Dot } from "./icons";

type Tone = "ok" | "warn" | "danger" | "muted";

// Canonical status vocabulary. Terse Title-Case labels; unknown values
// fall through to a muted, capitalized rendering rather than breaking.
const MAP: Record<string, { label: string; tone: Tone }> = {
  running: { label: "Running", tone: "ok" },
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
  // Explorer stream states — the ACS/tx snapshot-vs-live stream.
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
  /** "text" = dot + colored label (tables, detail rows);
   *  "pill" = bordered tinted chip (topbar, cards). */
  variant?: "text" | "pill";
  /** Pulse the dot (in-flight states). */
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
          fontSize: 11,
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
        fontSize: 12.5,
        ...style,
      }}
    >
      <Dot color={color} size={6} pulse={pulse} />
      {label}
    </span>
  );
}
