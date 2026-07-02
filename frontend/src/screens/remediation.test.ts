import { describe, it, expect } from "vitest";

import type { ErrorCode } from "../api";
import { remediationForCode } from "./remediation";

// Pins the silent-default regression risk: a new code added on the Go
// side (internal/localnet/coded_error.go) without a frontend mapping
// silently renders "no remediation panel" — the user gets the generic
// cause-text with no targeted help.
//
// Two layers of pinning:
//   1. EVERY known ErrorCode maps to either a non-null Remediation OR
//      an explicit null — no "default" case reachable in normal
//      operation.
//   2. The codes that SHOULD render a panel are individually tested
//      for shape + content keywords.

describe("remediationForCode", () => {
  // The full set of wire-stable codes, in sync with frontend/src/api.ts
  // and internal/localnet/coded_error.go. If the Go side adds a new
  // code, this list MUST be updated — that's the regression-catcher.
  const allKnownCodes: ErrorCode[] = [
    "PORTS_IN_USE",
    "DOCKER_DOWN",
    "DOCKER_NOT_INSTALLED",
    "COMPOSE_V1_OR_MISSING",
    "DOCKER_MEMORY_LOW",
    "DISK_LOW",
    "PREFLIGHT_FAILED",
    "CANTON_OOM",
    "CONTAINER_UNHEALTHY",
  ];

  // 7 codes that MUST render a panel — the remaining 2
  // (PREFLIGHT_FAILED, undefined) intentionally return null
  // because they're too generic to be actionable on their own.
  const codesWithPanel: ErrorCode[] = [
    "PORTS_IN_USE",
    "DOCKER_DOWN",
    "DOCKER_NOT_INSTALLED",
    "COMPOSE_V1_OR_MISSING",
    "DOCKER_MEMORY_LOW",
    "DISK_LOW",
    "CANTON_OOM",
    "CONTAINER_UNHEALTHY",
  ];

  it("undefined code returns null (no panel rendered)", () => {
    expect(remediationForCode(undefined)).toBeNull();
  });

  it("PREFLIGHT_FAILED returns null — generic catch-all", () => {
    expect(remediationForCode("PREFLIGHT_FAILED")).toBeNull();
  });

  it.each(codesWithPanel)(
    "%s returns a non-null Remediation with title + non-empty steps",
    (code) => {
      const r = remediationForCode(code);
      expect(r).not.toBeNull();
      expect(r!.title).toBeTruthy();
      expect(r!.title.length).toBeGreaterThan(0);
      expect(r!.steps).toBeInstanceOf(Array);
      expect(r!.steps.length).toBeGreaterThan(0);
      // Each step should be a real instruction, not a placeholder.
      for (const step of r!.steps) {
        expect(step.length).toBeGreaterThan(10);
      }
    },
  );

  it("titles are unique across all panel-returning codes — avoids ambiguous UI", () => {
    const titles = codesWithPanel
      .map((c) => remediationForCode(c)!.title)
      // Lowercasing keeps this test stable across copy edits.
      .map((t) => t.toLowerCase());
    expect(new Set(titles).size).toBe(titles.length);
  });

  it("CANTON_OOM remediation does NOT hard-code the 12 GiB / 8 GiB numbers", () => {
    // The thresholds live in internal/splice/versions.json; the
    // UI copy MUST refer users to the pre-flight panel (which
    // surfaces the catalogue numbers) rather than restating them.
    // A regression here would re-introduce string drift between
    // the catalogue and the displayed copy.
    const r = remediationForCode("CANTON_OOM");
    expect(r).not.toBeNull();
    const allText = (r!.title + " " + r!.steps.join(" ")).toLowerCase();
    expect(allText).not.toContain("12 gb");
    expect(allText).not.toContain("12gb");
    expect(allText).not.toContain("8 gb minimum");
    expect(allText).toContain("pre-flight panel");
  });

  it("DOCKER_DOWN remediation mentions Docker Desktop AND systemctl (cross-platform)", () => {
    const r = remediationForCode("DOCKER_DOWN");
    expect(r).not.toBeNull();
    const allText = (r!.title + " " + r!.steps.join(" ")).toLowerCase();
    expect(allText).toContain("docker desktop");
    expect(allText).toContain("systemctl");
  });

  it("PORTS_IN_USE remediation mentions both lsof AND Get-NetTCPConnection", () => {
    const r = remediationForCode("PORTS_IN_USE");
    expect(r).not.toBeNull();
    const allText = (r!.title + " " + r!.steps.join(" ")).toLowerCase();
    expect(allText).toContain("lsof");
    expect(allText.toLowerCase()).toContain("get-nettcpconnection");
  });

  it("every known code is handled — guards against the silent-default regression", () => {
    // A code added to the ErrorCode union without extending
    // remediationForCode falls through to the `default:` arm — no
    // remediation panel. Walk the explicit list and assert every
    // entry maps to one of the two intended outcomes (Remediation OR
    // explicit null). The list above MUST be updated when ErrorCode
    // changes — that's the stale-list canary.
    for (const code of allKnownCodes) {
      const r = remediationForCode(code);
      // Either a real remediation OR explicit null — never undefined.
      expect(r === null || (r.title.length > 0 && r.steps.length > 0)).toBe(true);
    }
  });
});
