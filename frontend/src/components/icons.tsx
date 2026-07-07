// The single icon system for the Web UI — 16×16 stroke glyphs drawn
// with currentColor so they inherit the text color of whatever they
// sit in. Replaces the mixed emoji/unicode controls (⚡ 🔥 ⏸ ↻ …)
// that read as prototype polish.
//
// Usage: <IcPause /> inside a Button icon slot, or standalone with
// size/style overrides. All icons are aria-hidden decoration; the
// accessible name belongs to the surrounding control.

import type { CSSProperties, ReactNode } from "react";

export interface IconProps {
  /** Rendered box in px. Defaults to 14 (button-slot size). */
  size?: number;
  style?: CSSProperties;
}

function I({ size = 14, style, children }: IconProps & { children: ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      style={{ display: "block", flex: "none", ...style }}
    >
      {children}
    </svg>
  );
}

export const IcPlay = (p: IconProps) => (
  <I {...p}>
    <path d="M5.5 3.5v9l7-4.5-7-4.5Z" />
  </I>
);

export const IcPause = (p: IconProps) => (
  <I {...p}>
    <path d="M6 3.5v9M10 3.5v9" />
  </I>
);

export const IcStop = (p: IconProps) => (
  <I {...p}>
    <rect x="4" y="4" width="8" height="8" />
  </I>
);

export const IcEject = (p: IconProps) => (
  <I {...p}>
    <path d="M8 3.5 12.5 9h-9L8 3.5Z" />
    <path d="M3.5 12.5h9" />
  </I>
);

export const IcRefresh = (p: IconProps) => (
  <I {...p}>
    <path d="M13.5 2.5v4h-4" />
    <path d="M13.2 9.3a5.4 5.4 0 1 1-1-5.2l1.3 1.9" />
  </I>
);

export const IcCheck = (p: IconProps) => (
  <I {...p}>
    <path d="m3 8.5 3.5 3.5L13 4.5" />
  </I>
);

export const IcX = (p: IconProps) => (
  <I {...p}>
    <path d="m4 4 8 8M12 4l-8 8" />
  </I>
);

export const IcAlert = (p: IconProps) => (
  <I {...p}>
    <path d="M8 2.5 14.5 13H1.5L8 2.5Z" />
    <path d="M8 6.5V9.5" />
    <path d="M8 11.6v.01" />
  </I>
);

export const IcDownload = (p: IconProps) => (
  <I {...p}>
    <path d="M8 2.5v7.5M4.5 6.5 8 10l3.5-3.5" />
    <path d="M3 13.5h10" />
  </I>
);

export const IcUpload = (p: IconProps) => (
  <I {...p}>
    <path d="M8 10V2.5M4.5 6 8 2.5 11.5 6" />
    <path d="M3 13.5h10" />
  </I>
);

export const IcArrowUp = (p: IconProps) => (
  <I {...p}>
    <path d="M8 13.5V3M3.5 7.5 8 3l4.5 4.5" />
  </I>
);

export const IcArrowRight = (p: IconProps) => (
  <I {...p}>
    <path d="M2.5 8H13M8.5 3.5 13 8l-4.5 4.5" />
  </I>
);

export const IcChevronDown = (p: IconProps) => (
  <I {...p}>
    <path d="m4 6 4 4 4-4" />
  </I>
);

export const IcChevronRight = (p: IconProps) => (
  <I {...p}>
    <path d="m6 4 4 4-4 4" />
  </I>
);

export const IcPlus = (p: IconProps) => (
  <I {...p}>
    <path d="M8 3v10M3 8h10" />
  </I>
);

export const IcBolt = (p: IconProps) => (
  <I {...p}>
    <path d="M9 2 3.5 9H7l-1 5L11.5 7H8l1-5Z" />
  </I>
);

export const IcFlame = (p: IconProps) => (
  <I {...p}>
    <path d="M8 2c.4 2.7-3.5 4-3.5 7.3a3.5 3.5 0 0 0 7 0C11.5 6 9.3 4.3 8 2Z" />
  </I>
);

export const IcDroplet = (p: IconProps) => (
  <I {...p}>
    <path d="M8 2.5c2.5 3 4 5 4 7a4 4 0 0 1-8 0c0-2 1.5-4 4-7Z" />
  </I>
);

/** Status dot — the only full-radius element in the system. */
export function Dot({
  color,
  size = 6,
  pulse = false,
  style,
}: {
  color: string;
  size?: number;
  pulse?: boolean;
  style?: CSSProperties;
}) {
  return (
    <span
      aria-hidden
      style={{
        display: "inline-block",
        width: size,
        height: size,
        borderRadius: "50%",
        background: color,
        flexShrink: 0,
        animation: pulse ? "pulse 1.6s ease-in-out infinite" : undefined,
        ...style,
      }}
    />
  );
}
