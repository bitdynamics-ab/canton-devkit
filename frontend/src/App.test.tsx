import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { App } from "./App";

const ISSUES = "https://github.com/bitdynamics-ab/canton-devkit/issues/new";

function stubServer(version: Record<string, unknown> | "down") {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      if (url.includes("/api/version")) {
        if (version === "down") return Promise.reject(new TypeError("Failed to fetch"));
        return Promise.resolve(
          new Response(JSON.stringify({ name: "canton-devkit", schema_version: 1, ...version }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url.includes("/api/instances")) {
        return Promise.resolve(
          new Response(JSON.stringify({ schema_version: 1, instances: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return Promise.reject(new Error(`unmocked fetch: ${url}`));
    }),
  );
}

function renderApp() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <App />
    </MemoryRouter>,
  );
}

describe("App build info from /api/version", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows canton-devkit localnet hints and pre-fills the bug report on a standalone build", async () => {
    stubServer({ cli_prefix: "canton-devkit localnet", version: "v0.18.1-cctools.1 (f78650e)" });
    renderApp();
    expect(await screen.findByText("canton-devkit localnet up --name demo")).toBeInTheDocument();
    expect(screen.queryByText(/dpm localnet/)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /report issue/i })).toHaveAttribute(
      "href",
      `${ISSUES}?template=bug_report.yml&version=v0.18.1-cctools.1%20(f78650e)`,
    );
  });

  it("shows dpm localnet hints under DPM", async () => {
    stubServer({ cli_prefix: "dpm localnet", version: "v0.18.1 (abc1234)" });
    renderApp();
    expect(await screen.findByText("dpm localnet up --name demo")).toBeInTheDocument();
    expect(screen.queryByText(/canton-devkit localnet/)).not.toBeInTheDocument();
  });

  it("falls back to dpm localnet and a template-only issue link on an older server", async () => {
    stubServer({});
    renderApp();
    expect(await screen.findByText("dpm localnet up --name demo")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /report issue/i })).toHaveAttribute(
      "href",
      `${ISSUES}?template=bug_report.yml`,
    );
  });

  it("names both launch commands when the server is unreachable", async () => {
    stubServer("down");
    renderApp();
    expect(await screen.findByText("Server unreachable")).toBeInTheDocument();
    expect(screen.getByText("canton-devkit localnet ui")).toBeInTheDocument();
    expect(screen.getByText("dpm localnet ui")).toBeInTheDocument();
  });
});
