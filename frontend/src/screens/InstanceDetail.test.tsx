import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { InstanceDetail } from "./InstanceDetail";

// InstanceDetail tests — surfaces every field the /api/instances/:name
// endpoint returns beyond the summary. Three states:
//
//   1. ok with full payload → grid populated
//   2. ok with live_probe_failed=true → warning pill in header
//   3. fetch error → red error line

function mockInstanceFetch(
  body: object | { status: number; error: string },
  status = 200,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

describe("InstanceDetail", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders every field from /api/instances/:name", async () => {
    mockInstanceFetch({
      schema_version: 1,
      name: "demo",
      splice_version: "0.4.12",
      status: "running",
      created_at: "2026-05-25T10:00:00Z",
      uptime: "2h 14m",
      compose_project: "cdk-demo",
      docker_network: "cdk-demo_default",
      container_prefix: "cdk-demo",
      project_dir: "/home/u/.canton-devkit/demo",
      data_dir: "/home/u/.canton-devkit/demo/data",
    });

    render(<InstanceDetail name="demo" />);

    // Wait for the loading state to clear.
    await waitFor(() => {
      expect(screen.getByText("0.4.12")).toBeInTheDocument();
    });
    // Identity + runtime + paths — pin one from each block to
    // catch a future refactor that drops a section. "cdk-demo"
    // appears in both compose-project and container-prefix
    // fields, so use getAllByText and assert the count.
    expect(screen.getAllByText("cdk-demo")).toHaveLength(2);
    expect(screen.getByText("2h 14m")).toBeInTheDocument();
    expect(
      screen.getByText("/home/u/.canton-devkit/demo/data"),
    ).toBeInTheDocument();
  });

  it("shows the live-probe-failed warning pill when set", async () => {
    mockInstanceFetch({
      schema_version: 1,
      name: "demo",
      splice_version: "0.4.12",
      status: "partial",
      created_at: "2026-05-25T10:00:00Z",
      compose_project: "cdk-demo",
      docker_network: "cdk-demo_default",
      container_prefix: "cdk-demo",
      project_dir: "/x",
      data_dir: "/x/data",
      live_probe_failed: true,
    });

    render(<InstanceDetail name="demo" />);
    await waitFor(() => {
      expect(screen.getByText(/live probe failed/i)).toBeInTheDocument();
    });
  });

  it("shows em-dash for missing uptime", async () => {
    // Uptime is optional in the type — a freshly-stopped instance
    // may not carry it. The grid uses "—" as the muted fallback.
    mockInstanceFetch({
      schema_version: 1,
      name: "demo",
      splice_version: "0.4.12",
      status: "stopped",
      created_at: "2026-05-25T10:00:00Z",
      compose_project: "cdk-demo",
      docker_network: "cdk-demo_default",
      container_prefix: "cdk-demo",
      project_dir: "/x",
      data_dir: "/x/data",
      // no uptime field
    });

    render(<InstanceDetail name="demo" />);
    // Find the row labelled "uptime" and check its sibling.
    await waitFor(() => {
      const uptimeLabel = screen.getByText("uptime");
      // Sibling is the next div under the same grid-row.
      expect(uptimeLabel.nextElementSibling?.textContent).toBe("—");
    });
  });

  it("shows the error line when the fetch fails", async () => {
    mockInstanceFetch(
      { code: "INSTANCE_NOT_FOUND", error: "instance ghost not found" },
      404,
    );
    render(<InstanceDetail name="ghost" />);
    await waitFor(() => {
      expect(screen.getByText(/instance ghost not found/i)).toBeInTheDocument();
    });
  });

  it("posts to /recreate and fires onChanged when the Recreate button is clicked", async () => {
    // The restart button is offered on running / paused / failed /
    // partial. The click invokes recreateInstance which POSTs to the
    // backend; on the 202 response the detail card refetches and
    // bubbles onChanged so the dashboard's row updates.
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.endsWith("/recreate")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              instance: "demo",
              events_url: "/api/instances/demo/events",
            }),
            { status: 202, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
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
            project_dir: "/x",
            data_dir: "/x/data",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(true));

    const onChanged = vi.fn();
    render(
      <InstanceDetail name="demo" statusHint="running" onChanged={onChanged} />,
    );

    // Wait for the Recreate button to appear (the action-button
    // bar renders once statusHint resolves).
    const restartBtn = await screen.findByRole("button", { name: /recreate/i });
    fireEvent.click(restartBtn);

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0]);
      expect(
        calls.some(
          (u: string) =>
            typeof u === "string" && u.endsWith("/api/instances/demo/recreate"),
        ),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(onChanged).toHaveBeenCalled();
    });
  });

  it("re-fetches when the name prop changes", async () => {
    // The Dashboard hands a new name when the user switches
    // instances. Without the useEffect dep on `name`, the
    // first-fetched detail would stick forever.
    let i = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(() => {
        i++;
        const which = i === 1 ? "demo" : "hubble";
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              name: which,
              splice_version: i === 1 ? "0.4.12" : "0.4.11",
              status: "running",
              created_at: "2026-05-25T10:00:00Z",
              compose_project: `cdk-${which}`,
              docker_network: `cdk-${which}_default`,
              container_prefix: `cdk-${which}`,
              project_dir: `/x/${which}`,
              data_dir: `/x/${which}/data`,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }),
    );

    const { rerender } = render(<InstanceDetail name="demo" />);
    await waitFor(() => {
      expect(screen.getByText("0.4.12")).toBeInTheDocument();
    });
    rerender(<InstanceDetail name="hubble" />);
    await waitFor(() => {
      expect(screen.getByText("0.4.11")).toBeInTheDocument();
    });
  });
});
