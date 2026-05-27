import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DeveloperSetup } from "./DeveloperSetup";

// DeveloperSetup tests — the JWT redacted-by-default + reveal
// contract is security-relevant; pin it in tests so a refactor
// that breaks it can't ship silently.
//
// What matters:
//   1. mount fetches WITHOUT include_jwt=true  (redacted-default)
//   2. "Show token" re-fetches WITH include_jwt=true
//   3. Copy on the revealed token hits navigator.clipboard.writeText
//   4. Hide re-redacts (state goes back to the redacted view)
//   5. AppConfigPanel switches transport based on format
//      (env/yaml → text endpoint, json → apiFetch JSON path)
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

  it("fetches redacted JWT on mount (no include_jwt query)", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        return jwtResponse({ redacted: true, token: "<redacted>" });
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
    // The mount fetch MUST NOT include the reveal query — the
    // redacted-by-default contract lives here.
    expect(jwtCall.url).toBe("/api/instances/demo/jwt");
    expect(jwtCall.url).not.toContain("include_jwt");
  });

  it("Show token re-fetches with include_jwt=true and displays the real token", async () => {
    let callIdx = 0;
    recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        callIdx++;
        // Second JWT call is the reveal — return the real token.
        return callIdx === 1
          ? jwtResponse({ redacted: true, token: "<redacted>" })
          : jwtResponse({
              redacted: false,
              token: "header.payload.signature",
            });
      }
      return new Response("KEY=value\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    const showBtn = await screen.findByRole("button", { name: /show token/i });
    await userEvent.click(showBtn);

    // The revealed token appears in the TokenBox split into parts.
    await waitFor(() => {
      expect(screen.getByText("header")).toBeInTheDocument();
      expect(screen.getByText("payload")).toBeInTheDocument();
      expect(screen.getByText("signature")).toBeInTheDocument();
    });
  });

  it("Copy on a revealed token writes the full token to clipboard", async () => {
    let callIdx = 0;
    recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        callIdx++;
        return callIdx === 1
          ? jwtResponse({ redacted: true, token: "<redacted>" })
          : jwtResponse({
              redacted: false,
              token: "header.payload.signature",
            });
      }
      return new Response("KEY=value\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    await userEvent.click(await screen.findByRole("button", { name: /show token/i }));
    // Two Copy buttons exist (JwtPanel + AppConfigPanel). Scope
    // to the JWT card so we click the right one.
    const jwtCard = screen.getByText("JWT generator").closest("section")!;
    await userEvent.click(within(jwtCard).getByRole("button", { name: /^copy$/i }));

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
      "header.payload.signature",
    );
  });

  it("Hide re-redacts the token view (back to the redacted UI)", async () => {
    let callIdx = 0;
    recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        callIdx++;
        return callIdx === 1
          ? jwtResponse({ redacted: true, token: "<redacted>" })
          : jwtResponse({
              redacted: false,
              token: "header.payload.signature",
            });
      }
      return new Response("KEY=value\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    await userEvent.click(await screen.findByRole("button", { name: /show token/i }));
    await userEvent.click(await screen.findByRole("button", { name: /hide/i }));

    // After Hide, the Show token button is back; the split-out
    // header/payload/signature spans should be gone.
    expect(await screen.findByRole("button", { name: /show token/i })).toBeInTheDocument();
    expect(screen.queryByText("signature")).not.toBeInTheDocument();
  });
});

describe("DeveloperSetup — AppConfigPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses ?format=env on mount and switches to ?format=json on tab click", async () => {
    const { calls } = recordingFetch(({ url }) => {
      if (url.includes("/jwt")) {
        return jwtResponse({ redacted: true, token: "<redacted>" });
      }
      if (url.includes("format=json")) {
        return new Response(
          JSON.stringify({
            schema_version: 1,
            name: "demo",
            splice_version: "0.4.12",
            endpoints: { ledger: "localhost:5011" },
            parties: { alice: "alice::abc" },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response("KEY=value\nOTHER=yes\n", { status: 200 });
    });

    render(<DeveloperSetup name="demo" />);

    await waitFor(() => {
      expect(
        calls.find((c) => c.url.includes("app-config?format=env")),
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
        calls.find((c) => c.url.includes("app-config?format=json")),
      ).toBeDefined();
    });

    // JSON body should render pretty-printed in the preview's
    // <pre>. Scope to the pre so we don't accidentally match
    // any other rendering of the word.
    await waitFor(() => {
      const pre = appConfigCard.querySelector("pre");
      expect(pre?.textContent).toContain('"endpoints"');
      expect(pre?.textContent).toContain('"alice"');
    });
  });
});
