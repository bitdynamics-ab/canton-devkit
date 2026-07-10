// Theme state. Dark (default) and light palettes live in index.css as
// CSS variables keyed off `data-theme` on <html>; setting it re-themes
// every W.* token. Persisted in localStorage.

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

// Sets the attribute that drives the CSS variables. Called before the
// first render (main.tsx) to avoid a flash, and on every change.
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

// Apply the persisted (or default) theme before the first render.
export function initTheme(): void {
  applyTheme(read());
}

// Subscribe a component to theme changes so a toggle re-renders it.
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
