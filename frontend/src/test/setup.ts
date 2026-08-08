// Vitest setup — loaded once before any test file runs.
//
// Two responsibilities:
//
//   1. Wire @testing-library/jest-dom matchers into expect() so
//      tests can use toBeInTheDocument / toHaveTextContent / etc.
//      without per-file imports.
//
//   2. Polyfills for browser APIs jsdom doesn't ship. Add narrowly
//      and only when a test hits one; over-polyfilling masks
//      real "this won't work in some browsers" bugs.
import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

// jsdom doesn't implement matchMedia; the HealthPill animation
// rule reads prefers-reduced-motion via the same mechanism in
// CSS but not from JS, so this stub is precautionary — components
// that ever ask `window.matchMedia(...)` get a sensible default.
if (!window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

// navigator.clipboard.writeText — the Overview's JWT / App-config Copy
// buttons hit this. jsdom omits the Clipboard API entirely, so tests that
// click Copy would otherwise throw. Tests can spy on this via
// vi.spyOn(navigator.clipboard, "writeText").
//
// configurable: true is critical — userEvent.setup() reassigns
// navigator.clipboard internally for paste handling, and a
// non-configurable property throws "Cannot redefine property:
// clipboard" the moment any test calls userEvent.setup().
if (!navigator.clipboard) {
  Object.defineProperty(navigator, "clipboard", {
    writable: true,
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
}

// jsdom also omits EventSource. Several screens subscribe to SSE
// progress or watch streams on mount, and the default "undefined"
// primitive turns otherwise-passing tests into unhandled runtime
// errors. Keep the shim intentionally small; tests that care about
// streamed data can still replace it with a richer fake.
if (!globalThis.EventSource) {
  class FakeEventSource {
    url: string;
    readyState = 0;
    withCredentials = false;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent<string>) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;

    constructor(url: string | URL) {
      this.url = String(url);
    }

    close() {
      this.readyState = 2;
    }

    addEventListener() {}
    removeEventListener() {}
    dispatchEvent() {
      return true;
    }
  }

  Object.defineProperty(globalThis, "EventSource", {
    writable: true,
    configurable: true,
    value: FakeEventSource,
  });
}

// Each test must start with a clean DOM. Without this, components
// from a previous test leak forwards (RTL renders into document.body
// and never tears down automatically).
afterEach(() => {
  cleanup();
});
