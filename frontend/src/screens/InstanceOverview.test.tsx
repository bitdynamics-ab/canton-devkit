import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { InstanceOverview } from "./InstanceOverview";
import { ConfirmHost } from "../components/ConfirmDialog";

// Routes every fetch InstanceOverview fires. `cfg` tweaks the branches
// individual tests care about; everything else returns a sane running-
// instance default so mount never rejects on an unmocked call.
function setupFetch(
  cfg: {
    summary?: "off" | { tps: number };
    tx?: { status: number; body: unknown };
    instanceStatus?: string;
  } = {},
) {
  const calls: { url: string; method: string }[] = [];
  const json = (body: unknown, status = 200) =>
    Promise.resolve(
      new Response(typeof body === "string" ? body : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  const spy = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? "GET" });

    if (url.endsWith("/containers"))
      return json({
        schema_version: 1,
        instance: "demo",
        containers: [
          {
            name: "cdk-demo-sv-web-ui-1",
            service: "sv-web-ui",
            state: "restarting",
            status: "Restarting (2) 3 seconds ago",
          },
          {
            name: "cdk-demo-canton-1",
            service: "canton",
            state: "running",
            health: "healthy",
            status: "Up 9 minutes (healthy)",
          },
        ],
        healthy_count: 1,
        starting_count: 0,
        unhealthy_count: 0,
        restarting_count: 1,
        exited_count: 0,
      });

    if (url.includes("/metrics/summary")) {
      if (!cfg.summary || cfg.summary === "off")
        return json(
          { code: "OBSERVABILITY_PROFILE_OFF", error: "observability off" },
          409,
        );
      return json({
        schema_version: 1,
        instance: "demo",
        metrics: { ledger_tps_5m: cfg.summary.tps },
      });
    }
    if (url.includes("/metrics/range"))
      return json({
        status: "success",
        data: {
          resultType: "matrix",
          result: [{ metric: {}, values: [[1000, "1.2"], [1300, "12.9"]] }],
        },
      });

    if (url.endsWith("/jwt"))
      return json({
        schema_version: 1,
        token: "header.payload.signature",
        party: "alice::abc",
        audience: "https://canton.network.global",
        role: "app-provider",
        warning_dev_secret: "dev secret in use — DO NOT use in prod",
        expires_in_seconds: 86400,
      });

    if (url.includes("/app-config")) {
      if (url.includes("format=json"))
        return json({
          schema_version: 1,
          instance: "demo",
          vars: { CANTON_APP_USER_PARTY: "alice::abc" },
        });
      return json("CANTON_APP_PROVIDER_USER=ledger-api-user\n");
    }

    if (url.includes("/transactions")) {
      if (cfg.tx) return json(cfg.tx.body, cfg.tx.status);
      return json({
        schema_version: 1,
        instance: "demo",
        role: "app-provider",
        ledger_end: 1200,
        count: 1,
        transactions: [
          {
            kind: "transaction",
            offset: 1199,
            update_id: "u1199",
            record_time: "2026-05-30T15:42:12Z",
            event_count: 1,
            events: [
              { kind: "exercise", contract_id: "0x77c0bbbb", template: "abcd:Retail.Token:Token" },
            ],
          },
        ],
      });
    }

    if (url.includes("/observability"))
      return json({ schema_version: 1, instance: "demo", prometheus: true, grafana: true, enabled: true });

    if (
      url.endsWith("/stop") ||
      url.endsWith("/start") ||
      url.endsWith("/down") ||
      url.endsWith("/pause") ||
      url.endsWith("/resume")
    )
      return Promise.resolve(new Response(null, { status: 204 }));
    if (url.endsWith("/recreate"))
      return json({ schema_version: 1, instance: "demo", events_url: "/api/instances/demo/events" }, 202);

    if (/\/api\/instances\/[^/?]+(?:\?|$)/.test(url))
      return json({
        schema_version: 1,
        name: "demo",
        splice_version: "0.6.4",
        status: cfg.instanceStatus ?? "running",
        created_at: "2026-07-27T08:46:00Z",
        uptime: "9m",
        compose_project: "cdk-demo",
        docker_network: "cdk-demo_default",
        container_prefix: "cdk-demo",
        project_dir: "/x",
        data_dir: "/x/data",
        endpoints: [
          { key: "app_provider_grpc", label: "provider gRPC", url: "localhost:61246", scheme: "grpc" },
          { key: "validator_ui", label: "Validator UI", url: "localhost:61200", scheme: "http", reachability: "ok" },
          { key: "sv_ui", label: "SV UI", url: "localhost:61204", scheme: "http", reachability: "unreachable" },
        ],
      });

    return Promise.reject(new Error(`unmocked: ${init?.method ?? "GET"} ${url}`));
  });
  vi.stubGlobal("fetch", spy);
  return { calls };
}

function urls(calls: { url: string; method: string }[]) {
  return calls.map((c) => c.url);
}

describe("InstanceOverview — header + actions", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the header fields and the Running pill", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" ports="61200-61250" />);
    await waitFor(() => expect(screen.getByText(/Splice 0.6.4/)).toBeInTheDocument());
    expect(screen.getByRole("heading", { name: "demo" })).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText(/ports 61200-61250/)).toBeInTheDocument();
  });

  it("Stop posts to /stop (not /down) on a running instance", async () => {
    const { calls } = setupFetch();
    const onChanged = vi.fn();
    render(<InstanceOverview name="demo" statusHint="running" onChanged={onChanged} />);
    const stopBtn = await screen.findByRole("button", { name: /^Stop$/ });
    fireEvent.click(stopBtn);
    await waitFor(() =>
      expect(urls(calls).some((u) => u.endsWith("/api/instances/demo/stop"))).toBe(true),
    );
    expect(urls(calls).some((u) => u.endsWith("/down"))).toBe(false);
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("Start posts to /start on a stopped instance", async () => {
    const { calls } = setupFetch({ instanceStatus: "stopped" });
    render(<InstanceOverview name="demo" statusHint="stopped" />);
    const startBtn = await screen.findByRole("button", { name: /^Start$/ });
    fireEvent.click(startBtn);
    await waitFor(() =>
      expect(urls(calls).some((u) => u.endsWith("/api/instances/demo/start"))).toBe(true),
    );
  });

  it("Recreate routes through confirm and posts to /recreate", async () => {
    const { calls } = setupFetch();
    render(
      <>
        <InstanceOverview name="demo" statusHint="running" />
        <ConfirmHost />
      </>,
    );
    fireEvent.click(await screen.findByRole("button", { name: /recreate/i }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /recreate/i }));
    await waitFor(() =>
      expect(urls(calls).some((u) => u.endsWith("/api/instances/demo/recreate"))).toBe(true),
    );
  });

  it("Down routes through confirm and posts to /down", async () => {
    const { calls } = setupFetch({ instanceStatus: "stopped" });
    render(
      <>
        <InstanceOverview name="demo" statusHint="stopped" />
        <ConfirmHost />
      </>,
    );
    fireEvent.click(await screen.findByRole("button", { name: /^Down$/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^Down$/ }));
    await waitFor(() =>
      expect(urls(calls).some((u) => u.endsWith("/api/instances/demo/down"))).toBe(true),
    );
  });

  it("Remove confirms volume deletion and sends DELETE", async () => {
    const { calls } = setupFetch({ instanceStatus: "stopped" });
    render(
      <>
        <InstanceOverview name="demo" statusHint="stopped" />
        <ConfirmHost />
      </>,
    );
    fireEvent.click(await screen.findByRole("button", { name: /^Remove instance$/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Docker volumes, ledger data/i)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: /remove permanently/i }));
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.url.endsWith("/api/instances/demo") && c.method === "DELETE",
        ),
      ).toBe(true),
    );
  });
});

describe("InstanceOverview — monitoring gate", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the metrics-off strip and enables monitoring", async () => {
    const { calls } = setupFetch({ summary: "off" });
    render(<InstanceOverview name="demo" statusHint="running" />);
    await waitFor(() =>
      expect(screen.getByText(/metrics not collected/i)).toBeInTheDocument(),
    );
    const enable = screen.getByRole("button", { name: /enable monitoring/i });
    fireEvent.click(enable);
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.url.endsWith("/api/instances/demo/observability") && c.method === "POST",
        ),
      ).toBe(true),
    );
  });

  it("renders the throughput read-out when monitoring is on", async () => {
    setupFetch({ summary: { tps: 12.9 } });
    render(<InstanceOverview name="demo" statusHint="running" />);
    await waitFor(() => expect(screen.getByText("12.9")).toBeInTheDocument());
    expect(screen.queryByText(/metrics not collected/i)).not.toBeInTheDocument();
  });
});

describe("InstanceOverview — endpoints + containers", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows real reachability and a neutral dash for unprobed schemes", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    // provider gRPC has no reachability → em-dash, never "reachable".
    const grpcRow = (await screen.findByText("provider gRPC")).closest("div")!;
    expect(within(grpcRow).getByText("—")).toBeInTheDocument();
    expect(within(grpcRow).queryByText("reachable")).not.toBeInTheDocument();
    // The unreachable SV UI drives the stale-overlay footer.
    expect(screen.getByText(/not serving HTTP/i)).toBeInTheDocument();
  });

  it("derives the Up column from docker's status string", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    // "Restarting (2) …" → attempt count; "Up 9 minutes …" → duration.
    expect(await screen.findByText("2 attempts")).toBeInTheDocument();
    expect(screen.getByText("9 minutes")).toBeInTheDocument();
    expect(screen.getByText("1 of 2 healthy")).toBeInTheDocument();
  });
});

describe("InstanceOverview — JWT + app config", () => {
  beforeEach(() => {
    Object.defineProperty(navigator, "clipboard", {
      writable: true,
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("renders the raw token split into parts and copies the full token", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    await waitFor(() => {
      expect(screen.getByText("header")).toBeInTheDocument();
      expect(screen.getByText("payload")).toBeInTheDocument();
      expect(screen.getByText("signature")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByRole("button", { name: /copy token/i }));
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("header.payload.signature");
  });

  it("fetches env config on mount and switches to json", async () => {
    const { calls } = setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    await waitFor(() =>
      expect(urls(calls).some((u) => u.includes("app-config?format=env"))).toBe(true),
    );
    const appConfig = screen.getByText("App config").closest("section")!;
    await userEvent.click(within(appConfig).getByRole("button", { name: "json" }));
    await waitFor(() =>
      expect(urls(calls).some((u) => u.includes("app-config?format=json"))).toBe(true),
    );
    await waitFor(() =>
      expect(appConfig.querySelector("pre")?.textContent).toContain("alice::abc"),
    );
  });
});

describe("InstanceOverview — fetch lifecycle", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the error line when the instance fetch fails", async () => {
    setupFetch();
    // setupFetch already stubbed every other endpoint (containers, jwt,
    // app-config, ...) with a sane running-instance shape; wrap it so
    // only the exact /api/instances/{name} GET 404s.
    const base = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        if (/\/api\/instances\/[^/?]+(?:\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ code: "INSTANCE_NOT_FOUND", error: "instance ghost not found" }),
              { status: 404, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
        return base(url, init);
      }),
    );

    render(<InstanceOverview name="ghost" statusHint="running" />);
    await waitFor(() => {
      expect(screen.getByText(/instance ghost not found/i)).toBeInTheDocument();
    });
  });

  it("re-fetches when the name prop changes", async () => {
    // Without the useEffect dep on `name`, the first instance would stick forever.
    setupFetch();
    const base = globalThis.fetch;
    let i = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        if (/\/api\/instances\/[^/?]+(?:\?|$)/.test(url)) {
          i++;
          const which = i === 1 ? "demo" : "hubble";
          return Promise.resolve(
            new Response(
              JSON.stringify({
                schema_version: 1,
                name: which,
                splice_version: i === 1 ? "0.6.4" : "0.6.3",
                status: "stopped",
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
        }
        return base(url, init);
      }),
    );

    const { rerender } = render(<InstanceOverview name="demo" statusHint="stopped" />);
    await waitFor(() => {
      expect(screen.getByText(/Splice 0.6.4/)).toBeInTheDocument();
    });
    rerender(<InstanceOverview name="hubble" statusHint="stopped" />);
    await waitFor(() => {
      expect(screen.getByText(/Splice 0.6.3/)).toBeInTheDocument();
    });
  });

  it("uses a neutral error banner for non-stop action failures", async () => {
    setupFetch({ instanceStatus: "stopped" });
    // setupFetch already stubbed a full running-instance fetch; wrap it so
    // only /start diverges to a failure, exercising the generic (non-stop)
    // action-error path.
    const base = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((url: string, init?: RequestInit) => {
        if (typeof url === "string" && url.endsWith("/start")) {
          return Promise.resolve(
            new Response(JSON.stringify({ code: "START_FAILED", error: "boom" }), {
              status: 500,
              headers: { "Content-Type": "application/json" },
            }),
          );
        }
        return base(url, init);
      }),
    );

    render(<InstanceOverview name="demo" statusHint="stopped" />);

    const startBtn = await screen.findByRole("button", { name: /^Start$/ });
    fireEvent.click(startBtn);

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent("Action failed: boom");
    });
    expect(screen.getByRole("alert")).not.toHaveTextContent("Stop failed");
  });
});

describe("InstanceOverview — tabs", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows ledger events after opening the Activity tab", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    await userEvent.click(await screen.findByRole("button", { name: "Activity" }));
    expect(await screen.findByText("exercise")).toBeInTheDocument();
    expect(screen.getByText("Retail.Token:Token")).toBeInTheDocument();
  });

  it("Activity tab surfaces the party-JWT hint", async () => {
    setupFetch({
      tx: { status: 503, body: { code: "EXPLORER_NEEDS_PARTY_JWT", error: "jwt lacks party rights" } },
    });
    render(<InstanceOverview name="demo" statusHint="running" />);
    await userEvent.click(await screen.findByRole("button", { name: "Activity" }));
    expect(await screen.findByText(/party-rights JWT/i)).toBeInTheDocument();
  });

  it("Snapshots tab renders the backup/restore panel", async () => {
    setupFetch();
    render(<InstanceOverview name="demo" statusHint="running" />);
    await userEvent.click(await screen.findByRole("button", { name: "Snapshots" }));
    expect(await screen.findByRole("button", { name: /download snapshot/i })).toBeInTheDocument();
  });
});
