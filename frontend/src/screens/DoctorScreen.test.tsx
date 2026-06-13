import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { PreflightReport } from "../api";
import { DoctorScreen } from "./DoctorScreen";

// DoctorScreen tests — the Web UI surface for `dpm localnet doctor`.
//
// What matters:
//   1. mount calls GET /api/doctor and renders each section + check
//      with the right pass/warn/fail glyph + remediation steps.
//   2. the doctor-only advisories (Platform support, Ephemeral
//      loopback ports) render — they are the reason this screen
//      exists over the create-modal preflight panel.
//   3. an error from the endpoint surfaces inline, not as a crash.
//   4. changing the version selector re-fetches with ?version=.
//
// The visual chrome (colors, exact glyph chars) is not asserted
// beyond the result word; that's covered by the typecheck step.

interface FetchCall {
  url: string;
}

function recordingFetch(handler: (call: FetchCall) => Response): {
  calls: FetchCall[];
} {
  const calls: FetchCall[] = [];
  const spy = vi.fn().mockImplementation(async (rawUrl: string) => {
    const url = typeof rawUrl === "string" ? rawUrl : (rawUrl as URL).toString();
    calls.push({ url });
    return handler({ url });
  });
  vi.stubGlobal("fetch", spy);
  return { calls };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const PASS_REPORT: PreflightReport = {
  schema_version: 1,
  ok: true,
  summary: "All checks passed — host is ready for `localnet up`",
  sections: [
    {
      title: "System",
      checks: [
        { label: "Docker daemon", result: "pass", detail: "reachable" },
        {
          label: "Platform support",
          result: "pass",
          detail: "darwin/arm64 is a supported platform",
        },
      ],
    },
    {
      title: "Network",
      checks: [
        { label: "Ephemeral loopback ports", result: "pass" },
      ],
    },
  ],
};

const VERSIONS_RESP = {
  schema_version: 1,
  latest_alias: "0.6.4",
  versions: [
    { tag: "0.6.4", status: "latest", major: "0.6", commit: "abc" },
    { tag: "0.5.9", status: "supported", major: "0.5", commit: "def" },
  ],
};

afterEach(() => vi.unstubAllGlobals());

describe("DoctorScreen", () => {
  it("calls GET /api/doctor on mount and renders sections + checks", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.startsWith("/api/doctor")) return json(PASS_REPORT);
      if (url.startsWith("/api/splice/versions")) return json(VERSIONS_RESP);
      return new Response("not found", { status: 404 });
    });

    render(<DoctorScreen />);

    await waitFor(() => {
      expect(screen.getByText("Docker daemon")).toBeTruthy();
    });
    // The two doctor-only advisories must render — they're the
    // reason this screen exists over /api/preflight.
    expect(screen.getByText("Platform support")).toBeTruthy();
    expect(screen.getByText("Ephemeral loopback ports")).toBeTruthy();
    // Section headers render.
    expect(screen.getByText("System")).toBeTruthy();
    expect(screen.getByText("Network")).toBeTruthy();
    // Summary banner.
    expect(
      screen.getByText(/All checks passed — host is ready/),
    ).toBeTruthy();

    expect(calls.some((c) => c.url.startsWith("/api/doctor"))).toBe(true);
  });

  it("renders remediation steps and the FAIL word for a failing check", async () => {
    const failing: PreflightReport = {
      schema_version: 1,
      ok: false,
      error_code: "DOCKER_MEMORY_LOW",
      summary: "1 issue · 0 warnings — host is NOT ready",
      sections: [
        {
          title: "Resources",
          checks: [
            {
              label: "Docker memory",
              result: "fail",
              detail: "4.0 GiB available, 8.0 GiB required",
              remediation: [
                "Raise Docker Desktop memory to 8 GiB",
                "Restart the daemon",
              ],
            },
          ],
        },
      ],
    };
    recordingFetch(({ url }) => {
      if (url.startsWith("/api/doctor")) return json(failing);
      if (url.startsWith("/api/splice/versions")) return json(VERSIONS_RESP);
      return new Response("not found", { status: 404 });
    });

    render(<DoctorScreen />);

    await waitFor(() => {
      expect(screen.getByText("Docker memory")).toBeTruthy();
    });
    expect(screen.getByText("FAIL")).toBeTruthy();
    expect(
      screen.getByText("Raise Docker Desktop memory to 8 GiB"),
    ).toBeTruthy();
    expect(screen.getByText("Restart the daemon")).toBeTruthy();
    expect(
      screen.getByText(/host is NOT ready/),
    ).toBeTruthy();
  });

  it("surfaces an endpoint error inline instead of crashing", async () => {
    recordingFetch(({ url }) => {
      if (url.startsWith("/api/doctor")) {
        return json({ code: "INTERNAL", error: "run doctor checks" }, 500);
      }
      if (url.startsWith("/api/splice/versions")) return json(VERSIONS_RESP);
      return new Response("not found", { status: 404 });
    });

    render(<DoctorScreen />);

    await waitFor(() => {
      expect(screen.getByText(/run doctor checks/)).toBeTruthy();
    });
  });

  it("re-fetches with ?version= when the version selector changes", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.startsWith("/api/doctor")) return json(PASS_REPORT);
      if (url.startsWith("/api/splice/versions")) return json(VERSIONS_RESP);
      return new Response("not found", { status: 404 });
    });

    render(<DoctorScreen />);

    // Wait for the version picker (populated from /api/splice/versions).
    const select = await screen.findByLabelText(
      "Splice version for resource thresholds",
    );
    await userEvent.selectOptions(select, "0.5.9");

    await waitFor(() => {
      expect(
        calls.some((c) => c.url.includes("/api/doctor?version=0.5.9")),
      ).toBe(true);
    });
  });
});
