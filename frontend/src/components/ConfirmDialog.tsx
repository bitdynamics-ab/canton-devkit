// Promise-based confirm dialog:
//   if (!(await confirmDialog({ title, body, confirmLabel, danger }))) return;
// A single ConfirmHost (mounted in App) handles the dispatched event.

import { useEffect, useState } from "react";
import { W, wMono, wSans, R, EASE, FAST } from "../tokens";
import { Button } from "./Button";

export interface ConfirmOptions {
  title: string;
  body: string;
  /** Optional monospace detail line (the exact command / effect). */
  detail?: string;
  confirmLabel?: string;
  danger?: boolean;
}

interface Pending extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

const EVENT = "cdk-confirm";

export function confirmDialog(opts: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    window.dispatchEvent(
      new CustomEvent<Pending>(EVENT, { detail: { ...opts, resolve } }),
    );
  });
}

export function ConfirmHost() {
  const [p, setP] = useState<Pending | null>(null);

  useEffect(() => {
    function onReq(e: Event) {
      setP((e as CustomEvent<Pending>).detail);
    }
    window.addEventListener(EVENT, onReq);
    return () => window.removeEventListener(EVENT, onReq);
  }, []);

  useEffect(() => {
    if (!p) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") settle(false);
      else if (e.key === "Enter") settle(true);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p]);

  if (!p) return null;
  function settle(ok: boolean) {
    p?.resolve(ok);
    setP(null);
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={p.title}
      onClick={() => settle(false)}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 200,
        background: "color-mix(in srgb, #000 44%, transparent)",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: "18vh",
        fontFamily: wSans,
        animation: `cdk-fade ${FAST} ${EASE}`,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(440px, 92vw)",
          background: W.surface,
          border: `1px solid ${W.borderHi}`,
          borderRadius: R.dialog,
          overflow: "hidden",
        }}
      >
        <div style={{ padding: "16px 18px 14px" }}>
          <h2 style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600, color: W.text }}>
            {p.title}
          </h2>
          <p style={{ margin: 0, fontSize: 16, lineHeight: 1.5, color: W.text2 }}>
            {p.body}
          </p>
          {p.detail && (
            <div
              style={{
                marginTop: 10,
                fontFamily: wMono,
                fontSize: 13,
                color: W.dim,
                background: W.inset,
                border: `1px solid ${W.border}`,
                borderRadius: R.control,
                padding: "7px 10px",
              }}
            >
              {p.detail}
            </div>
          )}
        </div>
        <div
          style={{
            display: "flex",
            justifyContent: "flex-end",
            gap: 8,
            padding: "12px 18px",
            borderTop: `1px solid ${W.border}`,
            background: W.sunken,
          }}
        >
          <Button variant="ghost" size="md" onClick={() => settle(false)}>
            Cancel
          </Button>
          <Button
            variant={p.danger ? "danger" : "primary"}
            size="md"
            onClick={() => settle(true)}
          >
            {p.confirmLabel ?? "Confirm"}
          </Button>
        </div>
      </div>
    </div>
  );
}
