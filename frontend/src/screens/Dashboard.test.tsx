import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Dashboard } from "./Dashboard";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";

function mockListResponse(
  instances: Array<{ name: string; status: string }> | "error",
  warning?: string,
  txOverride?: { status: number; body: unknown },
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
      // Empty list so ContainerHealth renders its placeholder, not the error path.
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
      // InstanceOverview probes metrics on mount; report observability
      // off so it renders the "enable monitoring" strip, no range query.
      if (url.includes("/metrics/summary")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ code: "OBSERVABILITY_PROFILE_OFF", error: "off" }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      // RecentActivity's ledger-event scan, fired only for a running instance.
      if (url.includes("/transactions")) {
        if (txOverride) {
          return Promise.resolve(
            new Response(JSON.stringify(txOverride.body), {
              status: txOverride.status,
              headers: { "Content-Type": "application/json" },
            }),
          );
        }
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instance: "demo",
              role: "app-provider",
              ledger_end: 1200,
              count: 2,
              transactions: [
                {
                  kind: "transaction", offset: 1200, update_id: "u1200",
                  record_time: "2026-05-30T15:42:14Z", event_count: 1,
                  events: [{ kind: "create", contract_id: "0x77c1aaaa", template: "abcd:Retail.Token:Token" }],
                },
                {
                  kind: "transaction", offset: 1199, update_id: "u1199",
                  record_time: "2026-05-30T15:42:12Z", event_count: 1,
                  events: [{ kind: "exercise", contract_id: "0x77c0bbbb", template: "abcd:Token:TokenProposal" }],
                },
              ],
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
              warning,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      // DeveloperSetup fires these once an instance is selected.
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
        <Dashboard />
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("Dashboard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the instance table when /api/instances returns data", async () => {
    mockListResponse([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    renderDashboard();

    // Scope to <table> so we assert the row, not the detail card's echo of "demo".
    await waitFor(() => {
      const table = screen.getByRole("table");
      expect(within(table).getByText("demo")).toBeInTheDocument();
      expect(within(table).getByText("hubble")).toBeInTheDocument();
    });
    const table = screen.getByRole("table");
    expect(within(table).getByText("Running")).toBeInTheDocument();
    expect(within(table).getByText("Stopped")).toBeInTheDocument();
  });

  it("shows only the bring-up panel while an instance is creating (not the full Overview)", async () => {
    // Regression: a creating instance rendered the bring-up panel AND the
    // running-instance Overview (throughput / tabs / KPI rail) stacked below.
    mockListResponse([{ name: "spinup", status: "creating" }]);
    renderDashboard("/?instance=spinup");
    await waitFor(() => {
      expect(screen.getByText(/bring-up in progress/i)).toBeInTheDocument();
    });
    // The Overview's tab bar must not appear alongside the progress panel.
    expect(screen.queryByRole("button", { name: /^snapshots$/i })).not.toBeInTheDocument();
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

  it("renders the warning strip when ListResponse.warning is set", async () => {
    mockListResponse(
      [{ name: "demo", status: "running" }],
      "registry has 1 unreadable entry; ignoring",
    );
    renderDashboard();
    await waitFor(() => {
      expect(
        screen.getByText(/registry has 1 unreadable entry/),
      ).toBeInTheDocument();
    });
  });

  it("clicking a row promotes that instance to the selection", async () => {
    mockListResponse([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    renderDashboard();

    // Auto-pick selects demo (first running); click hubble to override.
    const hubbleCell = await screen.findByText("hubble");
    await userEvent.click(hubbleCell);

    // InstanceOverview only renders once selection is non-null; its tab
    // bar is the stable marker (the "Snapshots" tab isn't in the table).
    await waitFor(() => {
      expect(screen.getByText("Snapshots")).toBeInTheDocument();
    });
  });

  it("auto-picks the first running instance when no row is clicked", async () => {
    mockListResponse([
      { name: "hubble", status: "stopped" },
      { name: "demo", status: "running" },
    ]);
    renderDashboard();

    // InstanceOverview renders only because auto-pick selected demo.
    await waitFor(() => {
      expect(screen.getByText("Snapshots")).toBeInTheDocument();
    });
  });
});
