// Click-to-copy affordance for a full party id (alias::fingerprint).
// Party ids are long and must be pasted verbatim into mint/transfer
// targets, so we copy the WHOLE id (never a truncation) and flash brief
// "Copied!" feedback that reverts after ~1.5s. Built on the shared
// Button so it inherits the dark design-token styling.

import { useEffect, useRef, useState } from "react";
import { Button } from "./Button";
import { IcCheck, IcCopy } from "./icons";

export function CopyPartyId({
  partyId,
  label = "Copy party id",
}: {
  /** The full party id to copy — always copied in full, never truncated. */
  partyId: string;
  /** Accessible label; defaults to "Copy party id". */
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timer.current), []);

  function copy() {
    // clipboard may be unavailable (non-localhost http) or denied; a
    // failed copy is a silent no-op rather than a thrown error.
    try {
      navigator.clipboard?.writeText(partyId).catch(() => {});
    } catch {
      /* no clipboard API */
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  }

  return (
    <Button
      variant="ghost"
      size="sm"
      icon={copied ? <IcCheck /> : <IcCopy />}
      onClick={copy}
      aria-label={label}
      title={copied ? "Copied!" : `${partyId}\n(click to copy full id)`}
    >
      {copied ? "Copied!" : "Copy id"}
    </Button>
  );
}
