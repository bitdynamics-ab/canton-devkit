// Middle-truncates a ledger id (head…tail) so the discriminating suffix
// stays visible; full value on hover, copies on click.

import { useState, type CSSProperties } from "react";
import { W, wMono, fs } from "../tokens";

function truncateMid(s: string, head: number, tail: number): string {
  if (s.length <= head + tail + 1) return s;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
}

interface MonoIdProps {
  value: string;
  head?: number;
  tail?: number;
  full?: boolean;
  size?: string;
  color?: string;
  style?: CSSProperties;
}

export function MonoId({
  value,
  head = 8,
  tail = 6,
  full = false,
  size = fs.small,
  color = W.text2,
  style,
}: MonoIdProps) {
  const [copied, setCopied] = useState(false);
  const shown = full ? value : truncateMid(value, head, tail);
  const copy = () => {
    // clipboard may be unavailable (non-localhost http) or denied; failed copy is a no-op.
    try {
      navigator.clipboard?.writeText(value).catch(() => {});
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1100);
    } catch {
      /* no clipboard API */
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      title={copied ? "Copied" : `${value}\n(click to copy)`}
      style={{
        appearance: "none",
        background: "transparent",
        border: 0,
        padding: 0,
        margin: 0,
        cursor: "pointer",
        fontFamily: wMono,
        fontSize: size,
        fontVariantLigatures: "none",
        fontVariantNumeric: "tabular-nums",
        color: copied ? W.ok : color,
        textAlign: "left",
        ...style,
      }}
    >
      {copied ? "copied" : shown}
    </button>
  );
}
