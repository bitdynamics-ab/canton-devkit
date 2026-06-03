import { describe, expect, it } from "vitest";
import { filter } from "./CommandPalette";

// Pure-function tests for the palette's filter — the only piece
// of the palette with non-trivial logic that's worth pinning in
// isolation. The keyboard / open / close flow is a smoke test
// best done in a manual pass (or a future Playwright run); doing
// it here would mostly exercise jsdom's event quirks.
//
// The contract the filter pins:
//   1. empty query → everything, in original order
//   2. case-insensitive substring on label OR hint
//   3. preserves input order (no reranking — grouping stays stable)
//   4. trimmed (whitespace pad doesn't tank the search)

interface TestAction {
  id: string;
  group: "Navigate" | "Switch instance";
  label: string;
  hint?: string;
  perform: () => void;
}

const ACTIONS: TestAction[] = [
  { id: "nav-overview", group: "Navigate", label: "Overview", hint: "/", perform: () => {} },
  { id: "nav-explorer", group: "Navigate", label: "Explorer", hint: "/explorer", perform: () => {} },
  { id: "nav-dar", group: "Navigate", label: "DAR Manager", hint: "/dar", perform: () => {} },
  { id: "inst-demo", group: "Switch instance", label: "demo", hint: "running · 0.4.12", perform: () => {} },
  { id: "inst-hubble", group: "Switch instance", label: "hubble", hint: "stopped · 0.4.11", perform: () => {} },
];

describe("CommandPalette filter", () => {
  it("returns everything for an empty query", () => {
    expect(filter(ACTIONS, "")).toHaveLength(ACTIONS.length);
  });

  it("returns everything for a whitespace-only query", () => {
    // Easy to break with a naive `if (!query)` check that
    // wouldn't trim. The palette receives raw input; padding
    // a stray space mid-type shouldn't blank the list.
    expect(filter(ACTIONS, "   ")).toHaveLength(ACTIONS.length);
  });

  it("matches case-insensitively against the label", () => {
    expect(filter(ACTIONS, "EXPLORER").map((a) => a.id)).toEqual(["nav-explorer"]);
  });

  it("matches against the hint as well as the label", () => {
    // "running" only appears in demo's hint, not the label.
    expect(filter(ACTIONS, "running").map((a) => a.id)).toEqual(["inst-demo"]);
  });

  it("returns multiple matches in input order", () => {
    // Both demo and explorer contain "e" — input order preserved.
    const got = filter(ACTIONS, "e").map((a) => a.id);
    expect(got).toEqual([
      "nav-overview",
      "nav-explorer",
      "nav-dar", // "DAR Manager"
      "inst-demo",
      "inst-hubble", // "stopped"
    ]);
  });

  it("returns [] when nothing matches", () => {
    expect(filter(ACTIONS, "xyzzy")).toEqual([]);
  });
});
