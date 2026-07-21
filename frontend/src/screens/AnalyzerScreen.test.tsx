import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { InstanceSelectionProvider } from "../shell/useInstanceSelection";
import { AnalyzerScreen } from "./AnalyzerScreen";

afterEach(() => vi.unstubAllGlobals());

const REPORT = {
  analyzed_package: { name: "pkg-app", version: "1.0.0", package_id: "aa11", lf_version: "2.2" },
  dependencies: [{ name: "pkg-registry", version: "1.0.0", package_id: "bb22" }],
  summary: { total_interactions: 1, by_type: { Exercise: 1 }, by_target_package: { "pkg-registry": 1 } },
  interactions: [
    {
      type: "Exercise",
      caller: { package: "pkg-app", version: "1.0.0", package_id: "aa11", module: "App", choice: "TransferAsset" },
      target: { package: "pkg-registry", version: "1.0.0", package_id: "bb22", module: "Registry", choice: "UpdateOwner", consuming: true },
    },
  ],
};

function stubFetch(status: { available: boolean; docker_found?: boolean; image_present?: boolean; detail?: string }) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      const json = (body: unknown) =>
        Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));
      if (url.startsWith("/api/version")) return json({ name: "canton-devkit", schema_version: 1 });
      if (url.startsWith("/api/instances/demo/analyzer/")) return json({ schema_version: 1, instance: "demo", package_id: "aa11", dar_name: "pkg-app-1.0.0.dar", report: REPORT });
      if (url.startsWith("/api/instances/demo/dar")) return json({ schema_version: 1, instance: "demo", role: "app-user", dars: [{ main: "aa11", name: "pkg-app", version: "1.0.0" }] });
      if (url.startsWith("/api/instances")) return json({ schema_version: 1, instances: [{ name: "demo", status: "running" }] });
      if (url.startsWith("/api/analyzer/status"))
        return json({ schema_version: 1, available: status.available, docker_found: status.docker_found ?? status.available, image_present: status.image_present ?? status.available, detail: status.detail ?? "" });
      return Promise.resolve(new Response(null, { status: 204 }));
    }),
  );
}

function renderScreen() {
  return render(
    <MemoryRouter initialEntries={["/analyzer?instance=demo"]}>
      <InstanceSelectionProvider>
        <AnalyzerScreen />
      </InstanceSelectionProvider>
    </MemoryRouter>,
  );
}

describe("AnalyzerScreen", () => {
  it("renders a report when a deployed DAR is analyzed", async () => {
    stubFetch({ available: true });
    renderScreen();
    // the deployed DAR appears as a button; clicking it renders the report
    const darBtn = await screen.findByRole("button", { name: /pkg-app 1\.0\.0/ }, { timeout: 4000 });
    await userEvent.click(darBtn);
    expect(await screen.findByText(/TransferAsset/)).toBeInTheDocument();
    expect(screen.getByText(/UpdateOwner/)).toBeInTheDocument();
    expect(screen.getByText(/consuming/)).toBeInTheDocument();
  });

  it("shows a not-configured notice when the analyzer is unavailable", async () => {
    stubFetch({ available: false, docker_found: false, image_present: false, detail: "install Docker to run the analyzer image" });
    renderScreen();
    expect(await screen.findByText("Analyzer not configured")).toBeInTheDocument();
    expect(screen.getByText(/install Docker/)).toBeInTheDocument();
  });
});
