import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DeveloperSetup } from "./DeveloperSetup";

// DeveloperSetup tests — LocalNet surfaces usable (raw) tokens by
// default. Pin that contract so a refactor that re-redacts the UI
// (making copy-pasted config unusable) can't ship silently.
//
// What matters:
//   1. JWT panel fetches WITH include_jwt=true on mount and renders
//      the real token split into header.payload.signature
//   2. Copy writes the full token to navigator.clipboard.writeText
//   3. AppConfigPanel fetches WITH include_jwt=true and switches
//      transport based on format (env/yaml → text endpoint, json →
//      apiFetch JSON path)
//
// Other surface (chip-row interactions, audience input) is
// trivial and covered by the build/typecheck step; testing it
// would duplicate React.

interface FetchCall {
  url: string;
  init?: RequestInit;
}

function recordingFetch(handler: (call: FetchCall) => Response): {
  spy: ReturnType<typeof vi.fn>;
  calls: FetchCall[];
} {
  const calls: FetchCall[] = [];
  const spy = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return handler({ url, init });
  });
  vi.stubGlobal("fetch", spy);
  return { spy, calls };
}

function jwtResponse(opts: { redacted: boolean; token: string }) {
  return new Response(
    JSON.stringify({
      schema_version: 1,
      token: opts.token,
      redacted: opts.redacted,
      party: "alice::abc123",
      audience: "https://canton.network.global",
      role: "app-provider",
      warning_dev_secret: "dev secret in use — DO NOT use in prod",
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

describe("DeveloperSetup — JwtPanel", () => {
  beforeEach(() => {
    // Clipboard mock reset per test so spy.mock.calls is clean.
    Object.defineProperty(navigator, "clipboard", {
      writable: true,
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("fetches a usable JWT on mount with include_jwt=true and renders it", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        return jwtResponse({ redacted: false, token: "header.payload.signature" });
      }
      // app-config text fetch from AppConfigPanel
      return new Response("KEY=value\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    await waitFor(() => {
      const jwtCall = calls.find((c) => c.url.includes("/jwt"));
      expect(jwtCall).toBeDefined();
    });
    const jwtCall = calls.find((c) => c.url.includes("/jwt"))!;
    // LocalNet surfaces a usable token — the mount fetch opts into
    // the raw token so the generated JWT is copy-pasteable.
    expect(jwtCall.url).toContain("include_jwt=true");

    // The real token renders in the TokenBox split into parts.
    await waitFor(() => {
      expect(screen.getByText("header")).toBeInTheDocument();
      expect(screen.getByText("payload")).toBeInTheDocument();
      expect(screen.getByText("signature")).toBeInTheDocument();
    });
  });

  it("Copy writes the full token to clipboard", async () => {
    recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        return jwtResponse({ redacted: false, token: "header.payload.signature" });
      }
      return new Response("KEY=value\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    // Wait for the token to render before copying.
    await screen.findByText("signature");
    // Two Copy buttons exist (JwtPanel + AppConfigPanel). Scope
    // to the JWT card so we click the right one.
    const jwtCard = screen.getByText("JWT generator").closest("section")!;
    await userEvent.click(within(jwtCard).getByRole("button", { name: /^copy$/i }));

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
      "header.payload.signature",
    );
  });
});

describe("DeveloperSetup — AppConfigPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses ?format=env on mount and switches to ?format=json on tab click", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        return jwtResponse({ redacted: false, token: "header.payload.signature" });
      }
      if (url.includes("format=json")) {
        return new Response(
          JSON.stringify({
            schema_version: 1,
            instance: "demo",
            vars: {
              CANTON_PARTICIPANT_LEDGER_APP_USER_PORT: "2901",
              CANTON_APP_USER_PARTY: "alice::abc",
            },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response("KEY=value\nOTHER=yes\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    await waitFor(() => {
      expect(
        calls.find(
          (c) =>
            c.url.includes("app-config?format=env") &&
            c.url.includes("include_jwt=true"),
        ),
      ).toBeDefined();
    });

    // The AppConfigPanel hosts the format chip-row. Scope the
    // query so we don't accidentally click the JwtPanel's role
    // chip-row, which doesn't include "json" so it'd fail anyway —
    // but defensive scoping is cheap.
    const appConfigCard = screen.getByText("App config").closest("section")!;
    await userEvent.click(within(appConfigCard).getByRole("button", { name: "json" }));

    await waitFor(() => {
      expect(
        calls.find(
          (c) =>
            c.url.includes("app-config?format=json") &&
            c.url.includes("include_jwt=true"),
        ),
      ).toBeDefined();
    });

    // JSON body should render pretty-printed in the preview's
    // <pre>. Scope to the pre so we don't accidentally match
    // any other rendering of the word. The shared EnvExport shape
    // nests everything under "vars".
    await waitFor(() => {
      const pre = appConfigCard.querySelector("pre");
      expect(pre?.textContent).toContain('"vars"');
      expect(pre?.textContent).toContain("alice::abc");
    });
  });
});
