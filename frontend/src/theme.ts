// Theme state. Palettes live in index.css as CSS variables keyed off
// `data-theme` on <html>; setting it re-themes every W.* token.

import { useSyncExternalStore } from "react";

export type Theme = "dark" | "light";

const STORAGE_KEY = "cdk-theme";
const listeners = new Set<() => void>();

function read(): Theme {
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // localStorage may be unavailable (private mode / sandbox).
  }
  return "light";
}

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

export function useTheme(): Theme {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    getTheme,
    () => "light",
  );
}
