// MonoId — the one way to render a ledger identifier (contract id,
// party id, package id, hash, offset) in this console.
//
// Ledger ids are long and their *suffix* is the discriminating part,
// so tail-only truncation ("00ce960f…") hides exactly what tells two
// ids apart. MonoId middle-truncates (head…tail), keeps the full value
// in the title for hover, and copies it on click — the discipline an
// auditor comparing ids relies on.

import { useState, type CSSProperties } from "react";
import { W, wMono } from "../tokens";

function truncateMid(s: string, head: number, tail: number): string {
  if (s.length <= head + tail + 1) return s;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
}

interface MonoIdProps {
  value: string;
  /** Leading chars kept. Default 8. */
  head?: number;
  /** Trailing chars kept — the discriminating suffix. Default 6. */
  tail?: number;
  /** Render in full (no truncation) — for short ids like symbols. */
  full?: boolean;
  size?: number;
  color?: string;
  style?: CSSProperties;
}

export function MonoId({
  value,
  head = 8,
  tail = 6,
  full = false,
  size = 12,
  color = W.text2,
  style,
}: MonoIdProps) {
  const [copied, setCopied] = useState(false);
  const shown = full ? value : truncateMid(value, head, tail);
  const copy = () => {
    // clipboard may be unavailable (http on non-localhost) or denied;
    // swallow both the throw and the promise rejection so a failed
    // copy is a silent no-op, not an unhandled rejection.
    try {
      const p = navigator.clipboard?.writeText(value);
      if (p) p.catch(() => {});
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1100);
    } catch {
      // no clipboard API at all
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
