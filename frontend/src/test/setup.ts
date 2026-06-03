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

// navigator.clipboard.writeText — DeveloperSetup's Copy buttons
// hit this. jsdom omits the Clipboard API entirely, so tests that
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

// Each test must start with a clean DOM. Without this, components
// from a previous test leak forwards (RTL renders into document.body
// and never tears down automatically).
afterEach(() => {
  cleanup();
});
