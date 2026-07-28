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

    // InstanceDetail only renders once selection is non-null.
    await waitFor(() => {
      expect(screen.getByText(/instance detail/i)).toBeInTheDocument();
    });
  });

  it("auto-picks the first running instance when no row is clicked", async () => {
    mockListResponse([
      { name: "hubble", status: "stopped" },
      { name: "demo", status: "running" },
    ]);
    renderDashboard();

    // InstanceDetail renders only because auto-pick selected demo.
    await waitFor(() => {
      expect(screen.getByText(/instance detail/i)).toBeInTheDocument();
    });
  });

  it("shows the recent-activity panel with ledger events for a running instance", async () => {
    mockListResponse([{ name: "demo", status: "running" }]);
    renderDashboard();
    await waitFor(() =>
      expect(screen.getByText(/recent activity/i)).toBeInTheDocument(),
    );
    expect(await screen.findByText("exercise")).toBeInTheDocument();
    // template id is shortened to Module:Entity for the EVENT column
    expect(screen.getByText("Retail.Token:Token")).toBeInTheDocument();
  });

  it("recent-activity shows the party-JWT hint when the ledger needs a party JWT", async () => {
    mockListResponse([{ name: "demo", status: "running" }], undefined, {
      status: 503,
      body: { code: "EXPLORER_NEEDS_PARTY_JWT", error: "jwt lacks party rights" },
    });
    renderDashboard();
    await waitFor(() =>
      expect(screen.getByText(/party-rights JWT/i)).toBeInTheDocument(),
    );
  });

  it("recent-activity shows the restart-to-capture hint for the no-JWT-recorded 500", async () => {
    // Instances predating JWT capture return a generic 500 distinguished by message, not code.
    mockListResponse([{ name: "demo", status: "running" }], undefined, {
      status: 500,
      body: { code: "INTERNAL", error: "no JWT recorded for role app-provider" },
    });
    renderDashboard();
    await waitFor(() =>
      expect(screen.getByText(/restart the instance to capture/i)).toBeInTheDocument(),
    );
  });

  it("recent-activity shows the empty hint when there are no ledger events", async () => {
    mockListResponse([{ name: "demo", status: "running" }], undefined, {
      status: 200,
      body: { schema_version: 1, instance: "demo", role: "app-provider", ledger_end: 0, count: 0, transactions: [] },
    });
    renderDashboard();
    await waitFor(() =>
      expect(screen.getByText(/No recent ledger activity/i)).toBeInTheDocument(),
    );
  });
});
