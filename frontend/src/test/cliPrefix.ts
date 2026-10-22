import { expect, onTestFinished } from "vitest";
import { resetBuildInfo, setBuildInfo } from "../buildInfo";

export const STANDALONE_CLI = "canton-devkit localnet";

export function setStandaloneCLI(): void {
  setBuildInfo({ cli_prefix: STANDALONE_CLI });
  onTestFinished(() => resetBuildInfo());
}

export function expectNoDpmHint(): void {
  expect(document.body.textContent).not.toContain("dpm localnet");
}
