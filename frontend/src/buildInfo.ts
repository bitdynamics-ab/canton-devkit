import { useSyncExternalStore } from "react";
import type { VersionResponse } from "./api";

export const DEFAULT_CLI_PREFIX = "dpm localnet";

let prefix = DEFAULT_CLI_PREFIX;
let version: string | null = null;
const listeners = new Set<() => void>();

function notify(): void {
  listeners.forEach((fn) => fn());
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function setBuildInfo(v: Pick<VersionResponse, "cli_prefix" | "version">): void {
  prefix = v.cli_prefix?.trim() || DEFAULT_CLI_PREFIX;
  version = v.version?.trim() || null;
  notify();
}

export function resetBuildInfo(): void {
  prefix = DEFAULT_CLI_PREFIX;
  version = null;
  notify();
}

export function cliPrefix(): string {
  return prefix;
}

export function buildVersion(): string | null {
  return version;
}

export function useCLIPrefix(): string {
  return useSyncExternalStore(subscribe, cliPrefix, () => DEFAULT_CLI_PREFIX);
}

export function useBuildVersion(): string | null {
  return useSyncExternalStore(subscribe, buildVersion, () => null);
}
