// Theme state for the Web UI. The design system ships both a dark
// (default) and light palette; the active one is written to
// `data-theme` on <html>, which flips every CSS variable in index.css
// and therefore every W.* token app-wide. The choice persists in
// localStorage so a reload keeps it.

import { useSyncExternalStore } from "react";

export type Theme = "dark" | "light";

const STORAGE_KEY = "cdk-theme";
const listeners = new Set<() => void>();

function read(): Theme {
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // localStorage may be unavailable (private mode / sandbox); fall
    // through to the default.
  }
  return "dark";
}

// applyTheme sets the attribute that drives the CSS variables. Called
// once at startup (before render, see main.tsx) so there is no
// light-on-dark flash, and again on every change.
export function applyTheme(t: Theme): void {
  document.documentElement.dataset.theme = t;
}

export function getTheme(): Theme {
  return (document.documentElement.dataset.theme as Theme) || read();
}

export function setTheme(t: Theme): void {
  applyTheme(t);
  try {
    window.localStorage.setItem(STORAGE_KEY, t);
  } catch {
    // Non-fatal: the theme still applies for this session.
  }
  listeners.forEach((fn) => fn());
}

export function toggleTheme(): void {
  setTheme(getTheme() === "dark" ? "light" : "dark");
}

// initTheme applies the persisted (or default) theme. Call before the
// first render.
export function initTheme(): void {
  applyTheme(read());
}

// useTheme subscribes a component to theme changes so a toggle
// re-renders with the current value.
export function useTheme(): Theme {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    getTheme,
    () => "dark",
  );
}
