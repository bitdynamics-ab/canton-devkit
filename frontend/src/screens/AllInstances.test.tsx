import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useSearchParams } from "react-router-dom";
import { AllInstances } from "./AllInstances";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { CreateInstanceProvider } from "../shell/useCreateInstance";

// Fleet-management surface (route /instances): the census + table + New
// instance + row-to-Overview navigation that used to live on the Dashboard.
function mockListResponse(
  instances: Array<{ name: string; status: string }> | "error",
  warning?: string,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      // Detail/containers/etc. only hit once an instance renders on "/";
      // the table view itself needs only the list endpoint.
      if (url.match(/\/api\/instances\/[^/?]+/)) {
        return Promise.resolve(new Response(JSON.stringify({}), { status: 200 }));
      }
      if (url.includes("/api/instances")) {
        if (instances === "error") {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                code: "REGISTRY_READ_FAILED",
                error: "could not read registry",
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
      return Promise.reject(new Error(`unmocked fetch: ${url}`));
    }),
  );
}

// Overview stand-in that echoes ?instance= so we can assert row-click landed
// on the detail view with the right selection.
function OverviewProbe() {
  const [params] = useSearchParams();
  return <div>overview for {params.get("instance") ?? "none"}</div>;
}

function renderAll() {
  return render(
    <MemoryRouter initialEntries={["/instances"]}>
      <InstanceSelectionProvider>
        <CreateInstanceProvider>
          <Routes>
            <Route path="/" element={<OverviewProbe />} />
            <Route path="/instances" element={<AllInstances />} />
          </Routes>
        </CreateInstanceProvider>
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("AllInstances", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the instance table and census when the list loads", async () => {
    mockListResponse([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    renderAll();
    await waitFor(() => {
      const table = screen.getByRole("table");
      expect(within(table).getByText("demo")).toBeInTheDocument();
      expect(within(table).getByText("hubble")).toBeInTheDocument();
    });
    expect(screen.getByText(/2 registered · 1 running · 1 stopped/)).toBeInTheDocument();
  });

  it("renders the warning strip when ListResponse.warning is set", async () => {
    mockListResponse(
      [{ name: "demo", status: "running" }],
      "registry has 1 unreadable entry; ignoring",
    );
    renderAll();
    await waitFor(() => {
      expect(
        screen.getByText(/registry has 1 unreadable entry/),
      ).toBeInTheDocument();
    });
  });

  it("clicking a row opens that instance's Overview with the selection", async () => {
    mockListResponse([
      { name: "demo", status: "running" },
      { name: "hubble", status: "stopped" },
    ]);
    renderAll();
    const hubble = await screen.findByText("hubble");
    await userEvent.click(hubble);
    await waitFor(() => {
      expect(screen.getByText("overview for hubble")).toBeInTheDocument();
    });
  });

  it("renders the EmptyState when no instances are registered", async () => {
    mockListResponse([]);
    renderAll();
    await waitFor(() => {
      expect(screen.getByText(/no localnet instances/i)).toBeInTheDocument();
    });
  });
});
