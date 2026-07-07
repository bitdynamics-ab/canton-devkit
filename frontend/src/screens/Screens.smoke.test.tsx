// Screen-level smoke tests.
//
// Each screen is mounted with a stubbed `fetch` and the
// InstanceSelectionProvider primed via an /api/instances seed. The
// asserts are deliberately narrow — "did the screen render without
// throwing and surface the expected heading/empty-state" — a tripwire
// for stale references that compile fine but throw at runtime when
// their code path is hit. Interactive behaviour is covered by the
// component-level tests; this is the "won't blow up on first paint"
// guard.

import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";

// InstanceSelectionProvider uses useSearchParams, which only works
// inside a <Router>; the existing tests (Dashboard.test.tsx) wrap
// the same way.
function Providers({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter initialEntries={["/?instance=demo"]}>
      <InstanceSelectionProvider>{children}</InstanceSelectionProvider>
    </MemoryRouter>
  );
}
import { DARScreen } from "./DARScreen";
import { MetricsScreen } from "./MetricsScreen";
import { ExplorerScreen } from "./ExplorerScreen";
import { WalletScreen } from "./WalletScreen";
import { DoctorScreen } from "./DoctorScreen";

interface InstanceShape {
  name: string;
  status?: string;
  endpoints?: Array<{
    label: string;
    url: string;
    port: number;
    scheme: string;
    reachability?: "ok" | "unreachable";
    reachability_detail?: string;
  }>;
}

// stubFetch wires the minimum set of endpoints each screen probes.
// Anything not enumerated returns 404 so an accidental new call
// surfaces in failures instead of being silently mocked away.
function stubFetch(instance: InstanceShape) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((rawUrl: string) => {
      const url = typeof rawUrl === "string" ? rawUrl : (rawUrl as URL).toString();
      // List: feeds InstanceSelectionProvider so `selected` resolves.
      if (url === "/api/instances" || url.startsWith("/api/instances?")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instances: [
                {
                  name: instance.name,
                  splice_version: "0.6.4",
                  status: instance.status ?? "running",
                  created_at: "2026-05-25T10:00:00Z",
                  updated_at: "2026-05-25T10:00:00Z",
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      // Detail — for WalletScreen + others that read endpoints.
      if (url.match(/^\/api\/instances\/[^/?]+(?:\?|$)/)) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              name: instance.name,
              splice_version: "0.6.4",
              status: instance.status ?? "running",
              created_at: "2026-05-25T10:00:00Z",
              compose_project: `canton-${instance.name}`,
              docker_network: instance.name,
              container_prefix: `${instance.name}-`,
              project_dir: "/tmp/p",
              data_dir: "/tmp/d",
              endpoints: instance.endpoints ?? [],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      // Empty lists for screen-specific endpoints — enough for the
      // initial-render code path.
      if (url.includes("/contracts") || url.includes("/transactions")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ schema_version: 1, contracts: [], transactions: [], count: 0 }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/dar")) {
        return Promise.resolve(
          new Response(JSON.stringify({ schema_version: 1, dars: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url.includes("/metrics/summary")) {
        return Promise.resolve(
          new Response(JSON.stringify({ schema_version: 1, metrics: {} }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url.includes("/metrics/range")) {
        return Promise.resolve(
          new Response(JSON.stringify({ status: "success", data: { result: [] } }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      // Doctor: host-readiness report (not instance-scoped) + the
      // version list its picker reads.
      if (url.startsWith("/api/doctor")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              ok: true,
              summary: "All checks passed — host is ready for `localnet up`",
              sections: [
                {
                  title: "System",
                  checks: [{ label: "Docker daemon", result: "pass" }],
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.startsWith("/api/splice/versions")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ schema_version: 1, latest_alias: "0.6.4", versions: [] }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return Promise.resolve(new Response("not found", { status: 404 }));
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("DARScreen smoke", () => {
  it("renders the DAR Manager heading without throwing", async () => {
    stubFetch({ name: "demo" });
    render(
      <Providers>
        <DARScreen />
      </Providers>,
    );
    // Tolerant — the screen mentions "DAR" in several places; we
    // just want SOMETHING matching to confirm the body rendered.
    await waitFor(() => {
      expect(screen.getAllByText(/DAR/i).length).toBeGreaterThan(0);
    });
  });
});

describe("MetricsScreen smoke", () => {
  it("renders without throwing when observability is off (empty body)", async () => {
    stubFetch({ name: "demo" });
    render(
      <Providers>
        <MetricsScreen />
      </Providers>,
    );
    // MetricCard titles are always present once the screen mounts;
    // they don't require observability to be on.
    await waitFor(() => {
      expect(screen.getAllByText(/throughput/i).length).toBeGreaterThan(0);
    });
  });
});

describe("ExplorerScreen smoke", () => {
  it("renders without throwing on empty ACS + transactions", async () => {
    stubFetch({ name: "demo" });
    render(
      <Providers>
        <ExplorerScreen />
      </Providers>,
    );
    // The view-toggle (Contracts/Transactions/Timeline) is the
    // most stable anchor — present on every render regardless of
    // ACS-empty-vs-populated.
    await waitFor(() => {
      expect(screen.getAllByText(/Contracts|Transactions|Timeline/i).length).toBeGreaterThan(0);
    });
  });
});

describe("WalletScreen smoke", () => {
  it("renders an iframe + login hint when endpoints are present", async () => {
    stubFetch({
      name: "demo",
      endpoints: [
        {
          label: "Wallet · app-user",
          url: "http://wallet.localhost:60470",
          port: 60470,
          scheme: "http",
        },
      ],
    });
    render(
      <Providers>
        <WalletScreen />
      </Providers>,
    );
    // The login hint is the most distinctive WalletScreen artefact.
    await waitFor(() => {
      expect(screen.getByText(/Login:/i)).toBeTruthy();
    });
  });

  it("replaces the iframe with remediation when the wallet UI is unreachable", async () => {
    stubFetch({
      name: "demo",
      endpoints: [
        {
          label: "Wallet · app-user",
          url: "http://localhost:4485",
          port: 4485,
          scheme: "http",
          reachability: "unreachable",
          reachability_detail:
            "connection accepted but no HTTP response (empty reply)",
        },
      ],
    });
    render(
      <Providers>
        <WalletScreen />
      </Providers>,
    );
    await waitFor(() => {
      const alert = screen.getByRole("alert");
      expect(alert.textContent).toMatch(/not serving HTTP/i);
      expect(alert.textContent).toContain("Recreate");
      expect(alert.textContent).toContain("dpm localnet up --name demo");
    });
    expect(document.querySelector("iframe")).toBeNull();
  });
});

describe("DoctorScreen smoke", () => {
  it("renders the host-readiness report without throwing", async () => {
    stubFetch({ name: "demo" });
    render(
      <Providers>
        <DoctorScreen />
      </Providers>,
    );
    // The screen heading is the most stable anchor; it renders before
    // the async report resolves.
    await waitFor(() => {
      expect(screen.getByText("Doctor")).toBeTruthy();
    });
  });
});
