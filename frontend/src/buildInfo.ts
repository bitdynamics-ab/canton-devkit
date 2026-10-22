import { useSyncExternalStore } from "react";
import type { VersionResponse } from "./api";

export const DEFAULT_CLI_PREFIX = "dpm localnet";

let prefix = DEFAULT_CLI_PREFIX;
const listeners = new Set<() => void>();

function notify(): void {
  listeners.forEach((fn) => fn());
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function setBuildInfo(v: Pick<VersionResponse, "cli_prefix">): void {
  prefix = v.cli_prefix?.trim() || DEFAULT_CLI_PREFIX;
  notify();
}

export function resetBuildInfo(): void {
  prefix = DEFAULT_CLI_PREFIX;
  notify();
}

export function cliPrefix(): string {
  return prefix;
}

export function useCLIPrefix(): string {
  return useSyncExternalStore(subscribe, cliPrefix, () => DEFAULT_CLI_PREFIX);
}
