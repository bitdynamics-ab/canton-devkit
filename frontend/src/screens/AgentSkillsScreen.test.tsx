import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AgentSkillsScreen } from "./AgentSkillsScreen";

// AgentSkillsScreen (BIT-189) tests — render the skill catalogue,
// preview a skill, and post a one-click install with the right body.
// The screen fetches /api/skills directly (no InstanceSelection
// provider needed), so we just stub fetch.

const SKILLS = [
  {
    filename: "localnet-lifecycle.md",
    name: "canton-localnet-lifecycle",
    description: "Start, inspect, and stop a Canton LocalNet.",
    body: "# Lifecycle\n\ndpm localnet up --name dev\n",
  },
  {
    filename: "dar-upload.md",
    name: "canton-dar-upload",
    description: "Upload and inspect DARs.",
    body: "# DAR\n\ndpm localnet dar upload ...\n",
  },
];

// stubFetch lets a test observe each install body and decide the
// `skipped` set per request. By default nothing is skipped; tests that
// exercise the clobber-safe path return skipped files unless the
// request carried force=true.
function stubFetch(opts?: {
  installSpy?: (body: { target: string; force?: boolean }) => void;
  skippedUnlessForced?: string[];
}) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/skills") {
        return Promise.resolve(
          new Response(
            JSON.stringify({ schema_version: 1, skills: SKILLS }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url === "/api/skills/install") {
        const body = init?.body
          ? (JSON.parse(init.body as string) as { target: string; force?: boolean })
          : { target: "claude" };
        opts?.installSpy?.(body);
        const skipped =
          opts?.skippedUnlessForced && !body.force ? opts.skippedUnlessForced : [];
        const installed = SKILLS.filter((s) => !skipped.includes(s.filename)).map(
          (s) => `/home/u/.${body.target}/skills/${s.filename}`,
        );
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              target: body.target,
              dir: `/home/u/.${body.target}/skills`,
              installed,
              count: installed.length,
              skipped,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return Promise.resolve(new Response("not found", { status: 404 }));
    }),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("AgentSkillsScreen", () => {
  it("renders the skill catalogue from /api/skills", async () => {
    stubFetch();
    render(<AgentSkillsScreen />);
    await waitFor(() => {
      expect(screen.getByText("canton-localnet-lifecycle")).toBeTruthy();
    });
    expect(screen.getByText("canton-dar-upload")).toBeTruthy();
  });

  it("previews the selected skill body", async () => {
    stubFetch();
    render(<AgentSkillsScreen />);
    // First skill is auto-selected; its body should render in the preview.
    await waitFor(() => {
      expect(screen.getByText(/dpm localnet up --name dev/)).toBeTruthy();
    });
  });

  it("posts a claude install with the right body shape", async () => {
    let posted: unknown = null;
    stubFetch({ installSpy: (b) => (posted = b) });
    render(<AgentSkillsScreen />);
    await waitFor(() => screen.getByText("canton-localnet-lifecycle"));

    const user = userEvent.setup();
    await user.click(screen.getByText("~/.claude/skills"));

    await waitFor(() => {
      expect(posted).toEqual({ target: "claude", force: false });
    });
    // Success indicator shows the returned dir.
    await waitFor(() => {
      expect(screen.getByText(/installed/i)).toBeTruthy();
    });
  });

  it("surfaces skipped files and force-reinstalls on Overwrite", async () => {
    const posts: Array<{ target: string; force?: boolean }> = [];
    stubFetch({
      installSpy: (b) => posts.push(b),
      skippedUnlessForced: ["dar-upload.md"],
    });
    render(<AgentSkillsScreen />);
    await waitFor(() => screen.getByText("canton-localnet-lifecycle"));

    const user = userEvent.setup();
    await user.click(screen.getByText("~/.claude/skills"));

    // First install reports a preserved (locally-modified) file.
    await waitFor(() => {
      expect(screen.getByText(/preserved/i)).toBeTruthy();
    });
    expect(screen.getByText(/dar-upload\.md/)).toBeTruthy();
    expect(posts[0]).toEqual({ target: "claude", force: false });

    // Overwrite re-posts with force=true; the skipped warning clears.
    await user.click(screen.getByRole("button", { name: /overwrite/i }));
    await waitFor(() => {
      expect(posts[1]).toEqual({ target: "claude", force: true });
    });
    await waitFor(() => {
      expect(screen.queryByText(/preserved/i)).toBeNull();
    });
  });
});
