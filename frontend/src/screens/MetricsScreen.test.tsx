import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { MetricsScreen, Q } from "./MetricsScreen";
import { STANDALONE_CLI, expectNoDpmHint, setStandaloneCLI } from "../test/cliPrefix";

describe("Q.cmdLatency", () => {
  // Guards the command-latency panel that replaced the (never-populating on
  // 0.6.x) submit-to-commit heatmap: it must be a real mean (sum/count) in
  // ms per node — NOT a histogram_quantile, which is NaN on 0.6.x's
  // +Inf-only histogram.
  it("is a per-node sum/count average in ms, not a percentile", () => {
    const q = Q.cmdLatency;
    const base = "daml_participant_api_commands_submissions_duration_seconds";
    expect(q).toContain(`${base}_sum`);
    expect(q).toContain(`${base}_count`);
    expect(q).toContain("by (node)");
    expect(q).toContain("/"); // a ratio
    expect(q.startsWith("1000")).toBe(true); // seconds -> ms
    expect(q).not.toContain("histogram_quantile");
  });
});

describe("MetricsScreen CLI prefix", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("names the standalone binary in the observability-off hint", async () => {
    setStandaloneCLI();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string) => {
        const json = (body: unknown, status = 200) =>
          Promise.resolve(
            new Response(JSON.stringify(body), {
              status,
              headers: { "Content-Type": "application/json" },
            }),
          );
        if (url === "/api/instances" || url.startsWith("/api/instances?")) {
          return json({ schema_version: 1, instances: [{ name: "demo", status: "running" }] });
        }
        if (url.includes("/metrics/summary")) {
          return json({ code: "OBSERVABILITY_PROFILE_OFF", error: "observability profile off" }, 412);
        }
        return json({ code: "NOT_FOUND", error: "not stubbed: " + url }, 404);
      }),
    );
    render(
      <MemoryRouter initialEntries={["/?instance=demo"]}>
        <InstanceSelectionProvider>
          <MetricsScreen />
        </InstanceSelectionProvider>
      </MemoryRouter>,
    );
    expect(
      await screen.findByText(`${STANDALONE_CLI} observability enable --name demo`),
    ).toBeInTheDocument();
    expectNoDpmHint();
  });
});
