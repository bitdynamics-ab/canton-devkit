import { describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  DEFAULT_CLI_PREFIX,
  cliPrefix,
  resetBuildInfo,
  setBuildInfo,
  useCLIPrefix,
} from "./buildInfo";

describe("buildInfo store", () => {
  it("defaults to the DPM prefix before /api/version loads", () => {
    expect(DEFAULT_CLI_PREFIX).toBe("dpm localnet");
    expect(cliPrefix()).toBe("dpm localnet");
  });

  it("takes the prefix the server reports", () => {
    setBuildInfo({ cli_prefix: "canton-devkit localnet" });
    expect(cliPrefix()).toBe("canton-devkit localnet");
  });

  it("keeps the DPM prefix when an older server omits cli_prefix", () => {
    setBuildInfo({});
    expect(cliPrefix()).toBe("dpm localnet");
    setBuildInfo({ cli_prefix: "  " });
    expect(cliPrefix()).toBe("dpm localnet");
  });

  it("resetBuildInfo restores the default", () => {
    setBuildInfo({ cli_prefix: "canton-devkit localnet" });
    resetBuildInfo();
    expect(cliPrefix()).toBe("dpm localnet");
  });

  it("re-renders hook consumers when the version payload lands", () => {
    const prefix = renderHook(() => useCLIPrefix());
    expect(prefix.result.current).toBe("dpm localnet");
    act(() => setBuildInfo({ cli_prefix: "canton-devkit localnet" }));
    expect(prefix.result.current).toBe("canton-devkit localnet");
  });
});
