import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Dashboard } from "./Dashboard";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { CreateInstanceProvider } from "../shell/useCreateInstance";

// The Overview ("/") is detail-only: it renders exactly one instance —
// the selected one — or an empty/error state. Fleet-level table behavior
// (list, census, row-click) lives in AllInstances.test.tsx.
function mockListResponse(
  instances: Array<{ name: string; status: string }> | "error",
) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      if (url.match(/\/api\/instances\/[^/?]+(?:\?|$)/)) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              name: "demo",
              splice_version: "0.4.12",
              status: "running",
              created_at: "2026-05-25T10:00:00Z",
              compose_project: "cdk-demo",
              docker_network: "cdk-demo_default",
              container_prefix: "cdk-demo",
              project_dir: "/home/u/.canton-devkit/demo",
              data_dir: "/home/u/.canton-devkit/demo/data",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.match(/\/api\/instances\/[^/?]+\/containers/)) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instance: "demo",
              containers: [],
              healthy_count: 0,
              starting_count: 0,
              unhealthy_count: 0,
              restarting_count: 0,
              exited_count: 0,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/metrics/summary")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ code: "OBSERVABILITY_PROFILE_OFF", error: "off" }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/transactions")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instance: "demo",
              role: "app-provider",
              ledger_end: 1200,
              count: 0,
              transactions: [],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/api/instances")) {
        if (instances === "error") {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                code: "REGISTRY_READ_FAILED",
                error: "could not read registry",
                detail: "permission denied",
              }),
              { status: 500, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instances: instances.map((i) => ({
                name: i.name,
                status: i.status,
                splice_version: "0.4.12",
                ports: "5011:5011",
                started_ago: "2m",
              })),
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/jwt")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              token: "header.payload.signature",
              party: "alice::abc",
              audience: "https://canton.network.global",
              role: "app-provider",
              warning_dev_secret: "dev secret",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/app-config")) {
        return Promise.resolve(new Response("KEY=value\n", { status: 200 }));
      }
      return Promise.reject(new Error(`unmocked fetch: ${url}`));
    }),
  );
}

function renderDashboard(initialPath = "/") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <InstanceSelectionProvider>
        <CreateInstanceProvider>
          <Dashboard />
        </CreateInstanceProvider>
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("Dashboard (detail-only Overview)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows only the bring-up panel while an instance is creating", async () => {
    mockListResponse([{ name: "spinup", status: "creating" }]);
    renderDashboard("/?instance=spinup");
    await waitFor(() => {
      // Markers unique to the bring-up panel (steps + terminal + safe-to-leave).
      expect(screen.getByText(/safe to leave/i)).toBeInTheDocument();
    });
    expect(screen.getByText("docker compose output")).toBeInTheDocument();
    // The running-instance Overview's tab bar must not appear alongside it.
    expect(screen.queryByRole("button", { name: /^snapshots$/i })).not.toBeInTheDocument();
  });

  it("auto-picks the first running instance and renders its Overview", async () => {
    mockListResponse([
      { name: "hubble", status: "stopped" },
      { name: "demo", status: "running" },
    ]);
    renderDashboard();
    // InstanceOverview renders only because auto-pick selected demo; its
    // Snapshots tab is the stable marker.
    await waitFor(() => {
      expect(screen.getByText("Snapshots")).toBeInTheDocument();
    });
  });

  it("renders the EmptyState when no instances are registered", async () => {
    mockListResponse([]);
    renderDashboard();
    await waitFor(() => {
      expect(screen.getByText(/no localnet instances/i)).toBeInTheDocument();
    });
    expect(screen.getByText(/dpm localnet up/i)).toBeInTheDocument();
  });

  it("renders the ErrorPanel when /api/instances fails", async () => {
    mockListResponse("error");
    renderDashboard();
    await waitFor(() => {
      expect(screen.getByText(/failed to load instances/i)).toBeInTheDocument();
    });
  });
});
