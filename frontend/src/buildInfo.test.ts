import { describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  DEFAULT_CLI_PREFIX,
  buildVersion,
  cliPrefix,
  resetBuildInfo,
  setBuildInfo,
  useBuildVersion,
  useCLIPrefix,
} from "./buildInfo";

describe("buildInfo store", () => {
  it("defaults to the DPM prefix and no version before /api/version loads", () => {
    expect(DEFAULT_CLI_PREFIX).toBe("dpm localnet");
    expect(cliPrefix()).toBe("dpm localnet");
    expect(buildVersion()).toBeNull();
  });

  it("takes the prefix and version the server reports", () => {
    setBuildInfo({ cli_prefix: "canton-devkit localnet", version: "v0.18.1-cctools.1 (f78650e)" });
    expect(cliPrefix()).toBe("canton-devkit localnet");
    expect(buildVersion()).toBe("v0.18.1-cctools.1 (f78650e)");
  });

  it("keeps the DPM prefix when an older server omits cli_prefix", () => {
    setBuildInfo({});
    expect(cliPrefix()).toBe("dpm localnet");
    expect(buildVersion()).toBeNull();
    setBuildInfo({ cli_prefix: "  ", version: "" });
    expect(cliPrefix()).toBe("dpm localnet");
    expect(buildVersion()).toBeNull();
  });

  it("resetBuildInfo restores the defaults", () => {
    setBuildInfo({ cli_prefix: "canton-devkit localnet", version: "v1" });
    resetBuildInfo();
    expect(cliPrefix()).toBe("dpm localnet");
    expect(buildVersion()).toBeNull();
  });

  it("re-renders hook consumers when the version payload lands", () => {
    const prefix = renderHook(() => useCLIPrefix());
    const version = renderHook(() => useBuildVersion());
    expect(prefix.result.current).toBe("dpm localnet");
    expect(version.result.current).toBeNull();
    act(() => setBuildInfo({ cli_prefix: "canton-devkit localnet", version: "v2" }));
    expect(prefix.result.current).toBe("canton-devkit localnet");
    expect(version.result.current).toBe("v2");
  });
});
