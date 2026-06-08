// CreateLocalNetModal — per-component observability toggle tests.
//
// We exercise the four toggle states (none / prom only / graf only /
// both) and the "Grafana without Prometheus" warning that signposts
// the no-data-source mistake. The submit path is covered by inspecting
// the POST body sent to /api/instances — that's the contract with the
// backend's handleCreate handler.

import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { CreateLocalNetModal } from "./CreateLocalNetModal";

function Providers({ children }: { children: React.ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}

function stubFetch(captured: { lastPostBody?: unknown }) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((rawUrl: string, init?: RequestInit) => {
      const url = typeof rawUrl === "string" ? rawUrl : (rawUrl as URL).toString();
      if (url === "/api/splice-versions" || url.startsWith("/api/splice-versions")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              versions: [
                {
                  tag: "latest",
                  canonical_tag: "0.6.4",
                  status: "stable",
                  is_default: true,
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.startsWith("/api/preflight")) {
        return Promise.resolve(
          new Response(JSON.stringify({ schema_version: 1, findings: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url === "/api/instances" && init?.method === "POST") {
        captured.lastPostBody = JSON.parse(String(init.body));
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
      return Promise.resolve(new Response("nope", { status: 404 }));
    }),
  );
}

describe("CreateLocalNetModal observability toggles", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders separate Prometheus and Grafana checkboxes", async () => {
    stubFetch({});
    render(
      <Providers>
        <CreateLocalNetModal open onClose={() => {}} onCreated={() => {}} />
      </Providers>,
    );
    // Both per-component labels must appear; the legacy single
    // "Enable observability" label must NOT (would mean the split
    // never landed).
    expect(await screen.findByLabelText("Enable Prometheus")).toBeInTheDocument();
    expect(screen.getByLabelText("Enable Grafana")).toBeInTheDocument();
    expect(screen.queryByText(/Enable observability/i)).toBeNull();
  });

  it("shows the Grafana-without-Prometheus warning only in that combo", async () => {
    stubFetch({});
    render(
      <Providers>
        <CreateLocalNetModal open onClose={() => {}} onCreated={() => {}} />
      </Providers>,
    );
    const prom = (await screen.findByLabelText("Enable Prometheus")) as HTMLInputElement;
    const graf = screen.getByLabelText("Enable Grafana") as HTMLInputElement;

    // Initially: neither toggled, no warning.
    expect(screen.queryByText(/no data source/i)).toBeNull();

    // Grafana only → warning surfaces.
    fireEvent.click(graf);
    expect(screen.getByText(/no data source/i)).toBeInTheDocument();

    // Add Prometheus → warning clears.
    fireEvent.click(prom);
    expect(screen.queryByText(/no data source/i)).toBeNull();

    // Drop Grafana → warning stays absent (only relevant when graf
    // is on without prom).
    fireEvent.click(graf);
    expect(screen.queryByText(/no data source/i)).toBeNull();
  });

  it("submits per-component profiles in the POST body", async () => {
    const captured: { lastPostBody?: unknown } = {};
    stubFetch(captured);
    render(
      <Providers>
        <CreateLocalNetModal open onClose={() => {}} onCreated={() => {}} />
      </Providers>,
    );
    const nameInput = await screen.findByPlaceholderText(/demo, pr-841/);
    fireEvent.change(nameInput, { target: { value: "demo" } });
    fireEvent.click(screen.getByLabelText("Enable Prometheus"));
    // Skip Grafana — verifies prometheus-only submits without grafana.

    const createBtn = await screen.findByRole("button", { name: /create localnet/i });
    await waitFor(() => expect(createBtn).not.toBeDisabled());
    fireEvent.click(createBtn);
    await waitFor(() => expect(captured.lastPostBody).toBeDefined());
    const body = captured.lastPostBody as { profiles?: string[] };
    expect(body.profiles).toEqual(["prometheus"]);
  });
});
