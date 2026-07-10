import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { InstanceDetail } from "./InstanceDetail";
import { ConfirmHost } from "../components/ConfirmDialog";

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

  it("shows the unreachable-UI banner with the Recreate remediation", async () => {
    mockInstanceFetch({
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
        {
          label: "Postgres",
          url: "postgresql://localhost:5432",
          port: 5432,
          scheme: "postgresql",
        },
      ],
    });

    render(<InstanceDetail name="demo" />);
    await waitFor(() => {
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Wallet · app-user");
      expect(alert).toHaveTextContent(/not serving HTTP/i);
      expect(alert).toHaveTextContent("Recreate");
      expect(alert).toHaveTextContent("dpm localnet up --name demo");
    });
  });

  it("renders no reachability banner when probed endpoints are ok", async () => {
    mockInstanceFetch({
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
      endpoints: [
        {
          label: "Wallet · app-user",
          url: "http://localhost:4485",
          port: 4485,
          scheme: "http",
          reachability: "ok",
        },
      ],
    });

    render(<InstanceDetail name="demo" />);
    await waitFor(() => {
      expect(screen.getByText("0.4.12")).toBeInTheDocument();
    });
    expect(screen.queryByText(/not serving HTTP/i)).not.toBeInTheDocument();
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

  it("uses a neutral error banner for non-stop action failures", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.endsWith("/start")) {
        return Promise.resolve(
          new Response(JSON.stringify({ code: "START_FAILED", error: "boom" }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return Promise.resolve(
        new Response(
          JSON.stringify({
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
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<InstanceDetail name="demo" statusHint="stopped" />);

    const startBtn = await screen.findByRole("button", { name: /start/i });
    fireEvent.click(startBtn);

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent("Action failed: boom");
    });
    expect(screen.getByRole("alert")).not.toHaveTextContent("Stop failed");
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

    const onChanged = vi.fn();
    render(
      <>
        <InstanceDetail name="demo" statusHint="running" onChanged={onChanged} />
        <ConfirmHost />
      </>,
    );

    // Wait for the Recreate button to appear (the action-button
    // bar renders once statusHint resolves).
    const restartBtn = await screen.findByRole("button", { name: /recreate/i });
    fireEvent.click(restartBtn);

    // Recreate is destructive-ish, so it routes through the in-app
    // confirm dialog. Approve it by clicking the dialog's confirm.
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /recreate/i }));

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

  it("posts to /stop (not /down) when the Stop button is clicked on a running instance", async () => {
    // Gentle Stop = docker compose stop, containers kept. Distinct
    // from the Down button (docker compose down, removes containers).
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.endsWith("/stop")) {
        return Promise.resolve(new Response(null, { status: 204 }));
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

    const onChanged = vi.fn();
    render(
      <InstanceDetail name="demo" statusHint="running" onChanged={onChanged} />,
    );

    const stopBtn = await screen.findByRole("button", { name: /^Stop$/ });
    fireEvent.click(stopBtn);

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0]);
      expect(
        calls.some(
          (u: string) =>
            typeof u === "string" && u.endsWith("/api/instances/demo/stop"),
        ),
      ).toBe(true);
      // Must NOT have hit /down.
      expect(
        calls.some(
          (u: string) => typeof u === "string" && u.endsWith("/down"),
        ),
      ).toBe(false);
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("posts to /start when the Start button is clicked on a stopped instance", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.endsWith("/start")) {
        // 204 fast-start path.
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(
        new Response(
          JSON.stringify({
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
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    const onChanged = vi.fn();
    render(
      <InstanceDetail name="demo" statusHint="stopped" onChanged={onChanged} />,
    );

    const startBtn = await screen.findByRole("button", { name: /start/i });
    fireEvent.click(startBtn);

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0]);
      expect(
        calls.some(
          (u: string) =>
            typeof u === "string" && u.endsWith("/api/instances/demo/start"),
        ),
      ).toBe(true);
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("posts to /down when the Down button is clicked on a stopped instance", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (typeof url === "string" && url.endsWith("/down")) {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(
        new Response(
          JSON.stringify({
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
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    const onChanged = vi.fn();
    render(
      <>
        <InstanceDetail name="demo" statusHint="stopped" onChanged={onChanged} />
        <ConfirmHost />
      </>,
    );

    const downBtn = await screen.findByRole("button", { name: /^Down$/ });
    fireEvent.click(downBtn);

    // Down removes containers, so it routes through the in-app confirm
    // dialog. Approve it via the dialog's confirm button.
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^Down$/ }));

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => c[0]);
      expect(
        calls.some(
          (u: string) =>
            typeof u === "string" && u.endsWith("/api/instances/demo/down"),
        ),
      ).toBe(true);
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
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
