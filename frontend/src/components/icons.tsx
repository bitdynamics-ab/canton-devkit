// 16×16 stroke glyphs on currentColor. All icons are aria-hidden
// decoration; the accessible name belongs to the surrounding control.

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

export const IcCopy = (p: IconProps) => (
  <I {...p}>
    <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
    <path d="M10.5 5.5V4a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 4v5A1.5 1.5 0 0 0 4 10.5h1.5" />
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

export const IcOverview = (p: IconProps) => (
  <I {...p}>
    <rect x="2.5" y="2.5" width="4.5" height="4.5" rx="1" />
    <rect x="9" y="2.5" width="4.5" height="4.5" rx="1" />
    <rect x="2.5" y="9" width="4.5" height="4.5" rx="1" />
    <rect x="9" y="9" width="4.5" height="4.5" rx="1" />
  </I>
);

export const IcDoctor = (p: IconProps) => (
  <I {...p}>
    <path d="M1.5 8h3l1.5-4 3 8 1.5-4h3" />
  </I>
);

export const IcWallet = (p: IconProps) => (
  <I {...p}>
    <rect x="2" y="3.5" width="12" height="9" rx="1.5" />
    <path d="M2 6.5h12" />
    <circle cx="11" cy="9.5" r="0.75" fill="currentColor" stroke="none" />
  </I>
);

export const IcExplorer = (p: IconProps) => (
  <I {...p}>
    <circle cx="7" cy="7" r="4.5" />
    <path d="M10.5 10.5 14 14" />
  </I>
);

export const IcPackage = (p: IconProps) => (
  <I {...p}>
    <path d="M8 1.8 13.5 5v6L8 14.2 2.5 11V5L8 1.8Z" />
    <path d="M2.5 5 8 8.2 13.5 5M8 8.2V14.2" />
  </I>
);

// Two package nodes joined by an interaction edge, under a magnifier —
// the "cross-package interaction analysis" motif for the Analyzer tab.
export const IcAnalyzer = (p: IconProps) => (
  <I {...p}>
    <circle cx="4" cy="4.5" r="2" />
    <circle cx="11" cy="4.5" r="2" />
    <path d="M6 4.5h3" />
    <circle cx="6.5" cy="10.5" r="3" />
    <path d="m8.7 12.7 2.8 2.8" />
  </I>
);

export const IcMetrics = (p: IconProps) => (
  <I {...p}>
    <path d="M2.5 2.5v11h11" />
    <path d="M5 10.5 7.5 7l2 2 3-4.5" />
  </I>
);

export const IcTokens = (p: IconProps) => (
  <I {...p}>
    <ellipse cx="8" cy="4.5" rx="5" ry="2.2" />
    <path d="M3 4.5v6.5c0 1.2 2.24 2.2 5 2.2s5-1 5-2.2V4.5" />
    <path d="M3 8c0 1.2 2.24 2.2 5 2.2s5-1 5-2.2" />
  </I>
);

export const IcAgent = (p: IconProps) => (
  <I {...p}>
    <path d="M3 2.5h8.5A1.5 1.5 0 0 1 13 4v9.5H4.5A1.5 1.5 0 0 1 3 12V2.5Z" />
    <path d="M3 11.5h10" />
    <path d="M6 5.5h4M6 8h2.5" />
  </I>
);

export const IcSun = (p: IconProps) => (
  <I {...p}>
    <circle cx="8" cy="8" r="3" />
    <path d="M8 1v1.5M8 13.5V15M1 8h1.5M13.5 8H15M3 3l1 1M12 12l1 1M13 3l-1 1M4 12l-1 1" />
  </I>
);

export const IcMoon = (p: IconProps) => (
  <I {...p}>
    <path d="M13 9.3A5.5 5.5 0 0 1 6.7 3a5.5 5.5 0 1 0 6.3 6.3Z" />
  </I>
);

export const IcCommand = (p: IconProps) => (
  <I {...p}>
    <path d="M5.5 2.5A1.75 1.75 0 1 1 3.75 4.25H12.25A1.75 1.75 0 1 1 10.5 2.5v11a1.75 1.75 0 1 1 1.75-1.75H3.75A1.75 1.75 0 1 1 5.5 13.5v-11Z" />
  </I>
);

export const IcBook = (p: IconProps) => (
  <I {...p}>
    <path d="M8 3.5C6.8 2.7 5 2.4 3 2.5v9c2-.1 3.8.2 5 1 1.2-.8 3-1.1 5-1v-9c-2-.1-3.8.2-5 1Z" />
    <path d="M8 3.5v10" />
  </I>
);

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
