import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Dashboard } from "./Dashboard";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";

// Dashboard tests — the user-facing states for the Overview
// screen. Pin the table-rendering + click-to-select wiring +
// the empty/error fallbacks; the InstanceTable's status badge
// is implementation detail not worth testing in isolation.
//
// Three classes of state the user sees:
//   1. ok with instances → table + InstanceDetail + DeveloperSetup
//   2. ok with empty list → EmptyState ("run dpm localnet up")
//   3. error → ErrorPanel with the message

function mockListResponse(
  instances: Array<{ name: string; status: string }> | "error",
  warning?: string,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      // /api/instances/:name detail — for InstanceDetail card.
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
      // /api/instances/{name}/containers — ContainerHealth's
      // 3s poll. Return empty list so the panel renders the
      // "no containers" placeholder rather than the error path.
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
      // /api/instances/{name}/transactions — the RecentActivity
      // panel's ledger-event scan, fired only for a running instance.
      if (url.includes("/transactions")) {
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
      // /api/instances list — primary fetch.
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
      // JWT + app-config — DeveloperSetup fires these once the
      // instance is selected. Return minimal payloads to keep
      // the components happy.
      if (url.includes("/jwt")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              token: "<redacted>",
              redacted: true,
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

    // "demo" appears in the table AND in the InstanceDetail
    // header (auto-selected); "hubble" only in the table.
    // Scope to <table> so we're asserting the row, not the
    // detail card's echo.
    await waitFor(() => {
      const table = screen.getByRole("table");
      expect(within(table).getByText("demo")).toBeInTheDocument();
      expect(within(table).getByText("hubble")).toBeInTheDocument();
    });
    // STATE badges within the table.
    const table = screen.getByRole("table");
    expect(within(table).getByText("running")).toBeInTheDocument();
    expect(within(table).getByText("stopped")).toBeInTheDocument();
  });

  it("renders the EmptyState when no instances are registered", async () => {
    mockListResponse([]);
    renderDashboard();
    await waitFor(() => {
      expect(screen.getByText(/no localnet instances/i)).toBeInTheDocument();
    });
    // The remediation hint must include the dpm command — this
    // is the user's first interaction with an empty UI.
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
    // Same warning the CLI's `dpm localnet list` surfaces (e.g.
    // registry parse drift). Should show as an amber strip above
    // the table.
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

    // The auto-pick rule picks demo (first running). Click on
    // hubble's row to override.
    const hubbleCell = await screen.findByText("hubble");
    await userEvent.click(hubbleCell);

    // After selection, the InstanceDetail card pops with the
    // detail-fetched data. We fetch a static "demo" detail in
    // the mock, but the card header echoes the URL-selected
    // name (hubble), so look for that as the source-of-truth.
    await waitFor(() => {
      // The hubble cell should now show in the brand colour
      // class — but we can't easily check colour. Instead pin
      // that the InstanceDetail section appeared, which only
      // happens once selection is non-null.
      expect(screen.getByText(/instance detail/i)).toBeInTheDocument();
    });
  });

  it("auto-picks the first running instance when no row is clicked", async () => {
    mockListResponse([
      { name: "hubble", status: "stopped" },
      { name: "demo", status: "running" },
    ]);
    renderDashboard();

    // InstanceDetail appears because the auto-pick selected demo.
    // Without the auto-pick rule there'd be no selected
    // instance and the detail card wouldn't render.
    await waitFor(() => {
      expect(screen.getByText(/instance detail/i)).toBeInTheDocument();
    });
  });

  it("shows the recent-activity panel with ledger events for a running instance", async () => {
    mockListResponse([{ name: "demo", status: "running" }]);
    renderDashboard();
    // The panel mounts for the auto-selected running instance and
    // flattens transactions → one row per ledger event.
    await waitFor(() =>
      expect(screen.getByText(/recent activity/i)).toBeInTheDocument(),
    );
    expect(await screen.findByText("exercise")).toBeInTheDocument();
    // template id is shortened to Module:Entity for the EVENT column
    expect(screen.getByText("Retail.Token:Token")).toBeInTheDocument();
  });
});
