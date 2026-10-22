import { useCallback, useEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";
import { useLocation } from "react-router-dom";
import { W, wMono, fs } from "../tokens";

const MENTIONS = "@CantonNetwork @bitdynamics_cc";

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
type Outcome = Exclude<ShareState, null | "capturing">;

async function capturePagePng(): Promise<Blob | null> {
  const { domToBlob } = await import("modern-screenshot");
  const root = document.getElementById("root") ?? document.body;
  return domToBlob(root, {
    scale: Math.min(2, window.devicePixelRatio || 1),
    backgroundColor: getComputedStyle(document.body).backgroundColor || "#ffffff",
    filter: (node) => (node as HTMLElement).dataset?.nocapture !== "1",
    onCloneNode: replaceOpaqueIframes,
  });
}

export function replaceOpaqueIframes(cloned: Node) {
  if (!(cloned instanceof Element)) return;
  const frames = cloned.tagName === "IFRAME" ? [cloned] : Array.from(cloned.querySelectorAll("iframe"));
  if (frames.length === 0) return;
  const live = getComputedStyle(document.body);
  const resolve = (v: string) => {
    const m = /^var\((--[\w-]+)\)$/.exec(v);
    return (m && live.getPropertyValue(m[1]).trim()) || v;
  };
  for (const frame of frames) {
    const card = frame.ownerDocument.createElement("div");
    card.setAttribute("style", frame.getAttribute("style") ?? "");
    card.dataset.iframePlaceholder = "1";
    Object.assign(card.style, {
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      background: resolve(W.surface),
      color: resolve(W.dim),
      fontFamily: wMono,
      fontSize: fs.label,
    });
    card.textContent = frame.getAttribute("title") || "Embedded page";
    frame.replaceWith(card);
  }
}

export function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30000);
}

function startImageCopy(png: Promise<Blob>): Promise<void> {
  try {
    if (typeof ClipboardItem === "undefined" || !navigator.clipboard?.write) {
      throw new Error("no image clipboard");
    }
    return navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
  } catch (err) {
    return Promise.reject(err);
  }
}

async function settleShare(copy: Promise<void>, png: Promise<Blob>, name: string): Promise<Outcome> {
  try {
    await copy;
    return "copied";
  } catch {
    const blob = await png.catch(() => null);
    if (!blob) return "error";
    try {
      downloadBlob(blob, name);
      return "saved";
    } catch {
      return "error";
    }
  }
}

const PROMPT: Record<Outcome, { lead: string; tail: string | null; ok: boolean }> = {
  copied: {
    lead: "Screenshot copied to your clipboard",
    tail: "…then press ⌘/Ctrl+V to paste it into your post",
    ok: true,
  },
  saved: {
    lead: "Screenshot saved to your Downloads",
    tail: "…then drag the file into your post",
    ok: true,
  },
  error: {
    lead: "Couldn’t capture the page — attach your own screenshot",
    tail: null,
    ok: false,
  },
};

const srOnly: CSSProperties = {
  position: "absolute",
  width: 1,
  height: 1,
  padding: 0,
  margin: -1,
  overflow: "hidden",
  clip: "rect(0 0 0 0)",
  whiteSpace: "nowrap",
  border: 0,
};

export function CampaignWatermark({ code }: { code: string }) {
  const { pathname } = useLocation();
  const [state, setState] = useState<ShareState>(null);
  const busy = useRef(false);
  const timer = useRef<number | undefined>(undefined);
  const shareRef = useRef<HTMLButtonElement>(null);
  const promptRef = useRef<HTMLDivElement>(null);
  const postRef = useRef<HTMLAnchorElement>(null);

  const later = useCallback((ms: number, next: () => void) => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(next, ms);
  }, []);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const text = `${messageForPath(pathname)}\n\n${MENTIONS}\nMy run: ${code}`;
  const shareUrl = `https://x.com/intent/tweet?text=${encodeURIComponent(text)}&hashtags=CCTools`;

  const onShare = useCallback(() => {
    if (busy.current) return;
    busy.current = true;
    window.clearTimeout(timer.current);
    setState("capturing");

    const png = capturePagePng().then((blob) => {
      if (!blob) throw new Error("capture failed");
      return blob;
    });
    const copy = startImageCopy(png);
    void settleShare(copy, png, `canton-devkit-${code}.png`).then((outcome) => {
      busy.current = false;
      setState(outcome);
    });
  }, [code]);

  const close = useCallback(() => {
    window.clearTimeout(timer.current);
    if (promptRef.current?.contains(document.activeElement)) shareRef.current?.focus();
    setState(null);
  }, []);

  const prompt = state && state !== "capturing" ? PROMPT[state] : null;

  useEffect(() => {
    if (!prompt) return;
    const active = document.activeElement;
    if (active === shareRef.current || active === document.body || active === null) {
      postRef.current?.focus();
    }
  }, [prompt]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape" && prompt) close();
  };

  if (!code) return null;

  const announcement =
    state === "capturing"
      ? "Capturing the page…"
      : prompt
        ? [prompt.lead, prompt.tail].filter(Boolean).join(" ")
        : "";

  return (
    <div
      role="note"
      aria-label={`Campaign code ${code}`}
      onKeyDown={onKeyDown}
      style={{
        position: "fixed",
        right: 14,
        bottom: "calc(14px + env(safe-area-inset-bottom, 0px))",
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
            pointerEvents: "auto",
            userSelect: "text",
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
      <button
        ref={shareRef}
        type="button"
        onClick={onShare}
        aria-label="Share your run on X (captures the page and copies it to your clipboard)"
        title="Capture this page and get a pre-filled X post"
        data-nocapture="1"
        aria-busy={state === "capturing"}
        style={{
          pointerEvents: "auto",
          flex: "0 0 auto",
          display: "inline-flex",
          alignItems: "center",
          gap: 4,
          margin: 0,
          border: "none",
          padding: "4px 11px",
          borderRadius: 999,
          background: W.brand,
          color: W.onAccent,
          fontWeight: 600,
          fontFamily: "inherit",
          fontSize: fs.label,
          lineHeight: "inherit",
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
      </button>

      {prompt && (
        <div
          ref={promptRef}
          data-nocapture="1"
          style={{
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
          <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
            <span style={{ color: W.text, fontWeight: 600 }}>
              {prompt.ok && <span aria-hidden="true">✓ </span>}
              {prompt.lead}
            </span>
            <button
              type="button"
              aria-label="Dismiss"
              title="Dismiss"
              onClick={close}
              style={{
                flex: "0 0 auto",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                width: 24,
                height: 24,
                margin: "-4px -6px 0 0",
                padding: 0,
                border: "none",
                borderRadius: 999,
                background: "transparent",
                color: W.dim,
                fontFamily: "inherit",
                fontSize: fs.body,
                lineHeight: 1,
              }}
            >
              <span aria-hidden="true">×</span>
            </button>
          </div>
          <a
            ref={postRef}
            href={shareUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => later(800, close)}
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
          {prompt.tail && <span>{prompt.tail}</span>}
        </div>
      )}

      <span role="status" data-nocapture="1" style={srOnly}>
        {announcement}
      </span>
    </div>
  );
}
