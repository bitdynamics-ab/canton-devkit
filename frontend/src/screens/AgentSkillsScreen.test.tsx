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

function stubFetch(installSpy?: (body: unknown) => void) {
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
        if (installSpy && init?.body) installSpy(JSON.parse(init.body as string));
        return Promise.resolve(
          new Response(
            JSON.stringify({
              schema_version: 1,
              target: "claude",
              dir: "/home/u/.claude/skills",
              installed: ["/home/u/.claude/skills/localnet-lifecycle/SKILL.md"],
              count: 1,
              skipped: [],
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
    stubFetch((b) => {
      posted = b;
    });
    render(<AgentSkillsScreen />);
    await waitFor(() => screen.getByText("canton-localnet-lifecycle"));

    const user = userEvent.setup();
    await user.click(screen.getByText("~/.claude/skills"));

    await waitFor(() => {
      expect(posted).toEqual({ target: "claude" });
    });
    // Success indicator shows the returned dir.
    await waitFor(() => {
      expect(screen.getByText(/installed/i)).toBeTruthy();
    });
  });
});
