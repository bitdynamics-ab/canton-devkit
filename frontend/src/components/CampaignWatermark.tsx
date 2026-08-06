// Corner watermark for the campaign build: this install's code on every screen,
// plus a Share that captures a page-only PNG, copies it to the clipboard, and
// shows a "Post on X" button with a paste prompt. The intent can't attach media
// (no OAuth) and we can't instruct the user once they're on x.com, so the prompt
// rides with the button. Rendered only when the backend sends a campaign_code.

import { useCallback, useState, type MouseEvent } from "react";
import { useLocation } from "react-router-dom";
import { W, wMono, fs } from "../tokens";

// Handles + hashtag seeded into every share (hashtag via the intent param).
const MENTIONS = "@CantonNetwork @bitdynamics_cc";

// Screen-aware share copy — the post reflects what the participant was actually
// doing. First matching route prefix wins; "/" (Overview) falls to DEFAULT.
const SCREEN_MESSAGES: Array<[RegExp, string]> = [
  [/^\/tokens/, "I just minted a token on a live Canton network with the Canton DevKit \u{1FA99}"],
  [/^\/wallet/, "Running Canton wallets on a local network with the Canton DevKit \u{1F45B}"],
  [/^\/explorer/, "Exploring live Canton ledger contracts with the Canton DevKit \u{1F50D}"],
  [/^\/dar/, "Deployed a Daml app to my local Canton network with the Canton DevKit \u{1F4E6}"],
  [/^\/metrics/, "Watching my local Canton network’s live metrics with the Canton DevKit \u{1F4CA}"],
  [/^\/doctor/, "Health-checked my local Canton network with the Canton DevKit \u{1FA7A}"],
  [/^\/agent/, "Driving a Canton network with agent skills via the Canton DevKit \u{1F916}"],
];
const DEFAULT_MESSAGE =
  "I just spun up a real Canton network in minutes with the Canton DevKit \u{1FA99}";

function messageForPath(pathname: string): string {
  for (const [re, msg] of SCREEN_MESSAGES) {
    if (re.test(pathname)) return msg;
  }
  return DEFAULT_MESSAGE;
}

type ShareState = null | "capturing" | "copied" | "saved" | "error";

// Captures the app (not the whole screen) to a PNG, excluding the interactive
// bits of the watermark (marked data-nocapture) so the shot is clean but still
// carries the code. Uses modern-screenshot (foreignObject) so the app's modern
// CSS — color-mix(), CSS vars, the inline SVG chart — renders faithfully.
async function capturePagePng(): Promise<Blob | null> {
  const { domToBlob } = await import("modern-screenshot");
  const root = document.getElementById("root") ?? document.body;
  return domToBlob(root, {
    scale: Math.min(2, window.devicePixelRatio || 1),
    backgroundColor: getComputedStyle(document.body).backgroundColor || "#ffffff",
    filter: (node) => (node as HTMLElement).dataset?.nocapture !== "1",
  });
}

function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function CampaignWatermark({ code }: { code: string }) {
  const { pathname } = useLocation();
  const [state, setState] = useState<ShareState>(null);

  const text = `${messageForPath(pathname)}\n\n${MENTIONS}\nMy run: ${code}`;
  const shareUrl = `https://x.com/intent/tweet?text=${encodeURIComponent(text)}&hashtags=CCTools`;

  const onShare = useCallback(
    async (e: MouseEvent<HTMLAnchorElement>) => {
      e.preventDefault();
      setState("capturing");

      const blob = await capturePagePng().catch(() => null);
      let outcome: Exclude<ShareState, null | "capturing"> = "error";

      if (blob) {
        // Copy while THIS tab is focused (image clipboard needs a focused,
        // user-activated tab). Falls back to a download where the browser has
        // no image clipboard (e.g. Firefox).
        try {
          if (typeof ClipboardItem !== "undefined" && navigator.clipboard?.write) {
            await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
            outcome = "copied";
          } else {
            throw new Error("no image clipboard");
          }
        } catch {
          downloadBlob(blob, `canton-devkit-${code}.png`);
          outcome = "saved";
        }
      }

      // Deliberately do NOT auto-open X: once the user is on x.com we can't tell
      // them to paste. Instead we show the paste instruction next to a "Post on
      // X" button they click themselves — so the prompt is read before they
      // leave (and a user-gesture open is never popup-blocked).
      setState(outcome);
      window.setTimeout(() => setState((s) => (s === outcome ? null : s)), 30000);
    },
    [code],
  );

  const dismissSoon = () => window.setTimeout(() => setState(null), 800);

  // Defense-in-depth: render nothing without a code, so a normal build (or a
  // dropped parent guard) never shows an empty pill or a code-less share link.
  if (!code) return null;

  const lead =
    state === "copied"
      ? "✓ Screenshot copied to your clipboard"
      : state === "saved"
        ? "✓ Screenshot saved to your Downloads"
        : state === "error"
          ? "Couldn’t capture the page — attach your own screenshot"
          : null;
  const tail =
    state === "copied"
      ? "…then press ⌘/Ctrl+V to paste it into your post"
      : state === "saved"
        ? "…then drag the file into your post"
        : null;

  return (
    <div
      role="note"
      aria-label={`Campaign code ${code}`}
      style={{
        position: "fixed",
        right: 14,
        bottom: "calc(14px + env(safe-area-inset-bottom, 0px))",
        // Sit below the overlay band (drawers/modals are z 40–200) so any
        // dialog paints over it, and never intercept clicks — pointer-events is
        // re-enabled only on the Share link below.
        zIndex: 30,
        pointerEvents: "none",
        display: "flex",
        alignItems: "center",
        gap: 10,
        maxWidth: "calc(100vw - 28px)",
        padding: "5px 6px 5px 12px",
        borderRadius: 999,
        background: W.surface,
        border: `1px solid ${W.border}`,
        boxShadow: "0 4px 16px rgba(0,0,0,0.18)",
        fontFamily: wMono,
        fontSize: fs.label,
        color: W.text,
        userSelect: "text",
      }}
    >
      {lead && (
        <div
          data-nocapture="1"
          role="status"
          style={{
            // The pill container is pointer-events:none; re-enable it here so
            // the "Post on X" link inside the prompt is actually clickable.
            pointerEvents: "auto",
            position: "absolute",
            right: 0,
            bottom: "calc(100% + 8px)",
            maxWidth: "min(340px, calc(100vw - 28px))",
            display: "flex",
            flexDirection: "column",
            alignItems: "flex-end",
            gap: 7,
            padding: "10px 12px",
            borderRadius: 10,
            background: W.surface,
            border: `1px solid ${W.border}`,
            boxShadow: "0 6px 22px rgba(0,0,0,0.22)",
            color: W.text2,
            fontSize: fs.label,
            lineHeight: 1.45,
            textAlign: "right",
          }}
        >
          <span style={{ color: W.text, fontWeight: 600 }}>{lead}</span>
          <a
            href={shareUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={dismissSoon}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              textDecoration: "none",
              padding: "5px 12px",
              borderRadius: 999,
              background: W.brand,
              color: W.onAccent,
              fontWeight: 600,
            }}
          >
            Post on X <span aria-hidden="true">↗</span>
          </a>
          {tail && <span>{tail}</span>}
        </div>
      )}

      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          minWidth: 0,
          overflow: "hidden",
        }}
      >
        <span style={{ width: 7, height: 7, borderRadius: 999, background: W.brand, flex: "0 0 auto" }} />
        <span style={{ color: W.dim }}>CantonDevKit</span>
        <span
          style={{
            color: W.brandText,
            fontWeight: 600,
            letterSpacing: 0.3,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {code}
        </span>
      </span>
      <a
        href={shareUrl}
        onClick={onShare}
        target="_blank"
        rel="noopener noreferrer"
        aria-label="Share your run on X (captures the page and copies it to your clipboard)"
        title="Capture this page, copy it to your clipboard, and open a pre-filled X post"
        data-nocapture="1"
        aria-busy={state === "capturing"}
        style={{
          pointerEvents: "auto",
          flex: "0 0 auto",
          display: "inline-flex",
          alignItems: "center",
          gap: 4,
          textDecoration: "none",
          padding: "4px 11px",
          borderRadius: 999,
          background: W.brand,
          color: W.onAccent,
          fontWeight: 600,
          fontFamily: "inherit",
          fontSize: fs.label,
          cursor: state === "capturing" ? "progress" : "pointer",
          whiteSpace: "nowrap",
        }}
      >
        {state === "capturing" ? (
          "Capturing…"
        ) : (
          <>
            Share <span aria-hidden="true">↗</span>
          </>
        )}
      </a>
    </div>
  );
}
