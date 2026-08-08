import { describe, expect, it } from "vitest";
import {
  buildDerivedTargets,
  buildSeedTargets,
  rewriteInstanceName,
  validateSchema,
} from "./seed-utils.ts";

describe("seed-utils", () => {
  const baseOpts = {
    baseUrl: "http://127.0.0.1:7777",
    instance: "mynet",
    role: "app-user",
    dryRun: false,
  };

  it("buildSeedTargets includes core endpoints", () => {
    const targets = buildSeedTargets(baseOpts);
    expect(targets.find((t) => t.file === "version.json")?.path).toBe("/api/version");
    expect(targets.find((t) => t.file === "instance-mynet.json")?.path).toBe(
      "/api/instances/mynet",
    );
    expect(targets.find((t) => t.file === "contracts.json")?.path).toContain("role=app-user");
    expect(targets.find((t) => t.file === "token-identity.json")?.path).toBe(
      "/api/tokens/identity?instance=mynet&role=app-user",
    );
    expect(targets.find((t) => t.file === "token-allocations.json")?.path).toBe(
      "/api/tokens/allocations?instance=mynet",
    );
    expect(targets.find((t) => t.file === "token-transfers.json")?.path).toBe(
      "/api/tokens/transfers?instance=mynet",
    );
  });

  it("buildDerivedTargets adds contract and token drill-down", () => {
    const derived = buildDerivedTargets(
      baseOpts,
      { contracts: [{ contract_id: "00abc" }] },
      { transactions: [{ update_id: "u1" }] },
      { dars: [{ id: "dar-1" }] },
      { instruments: [{ symbol: "RTK" }] },
    );
    expect(derived.some((t) => t.file === "contract-detail.json")).toBe(true);
    expect(derived.some((t) => t.file === "tx-replay.json")).toBe(true);
    expect(derived.some((t) => t.file === "dar-inspect.json")).toBe(true);
    expect(derived.some((t) => t.file === "token-summary.json")).toBe(true);
  });

  it("rewriteInstanceName renames instance fields", () => {
    const input = {
      name: "mynet",
      instance: "mynet",
      instances: [{ name: "mynet" }],
      compose_project: "canton-mynet",
      container_prefix: "mynet-",
    };
    const out = rewriteInstanceName(input, "mynet", "demo") as typeof input;
    expect(out.name).toBe("demo");
    expect(out.instance).toBe("demo");
    expect(out.instances[0].name).toBe("demo");
    expect(out.compose_project).toBe("canton-demo");
    expect(out.container_prefix).toBe("demo-");
  });

  it("validateSchema accepts schema_version 1", () => {
    expect(validateSchema({ schema_version: 1 })).toBe(true);
    expect(validateSchema({ schema_version: 2 })).toBe(false);
    expect(validateSchema(null)).toBe(false);
  });
});
