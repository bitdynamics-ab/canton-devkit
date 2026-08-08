import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { SpliceVersionEntry } from "../api";
import { VersionPicker, compareSpliceTags } from "./CreateLocalNetModal";

// VersionPicker — curated Splice versions render as segmented PILLS
// (radiogroup); non-curated "available" tags fall into a compact "more"
// <select> so a 30-entry catalogue doesn't overflow a pill row. The
// load-bearing invariant is unchanged: never a free-text <input>. The
// empty / loading / error states still fall back to a disabled <select>.

const FIXTURES: SpliceVersionEntry[] = [
  { tag: "0.6.4", status: "latest", major: "0.6", commit: "abc1234567890" },
  { tag: "0.6.3", status: "supported", major: "0.6", commit: "def4567890123" },
  { tag: "0.5.18", status: "supported", major: "0.5", commit: "fed7890123456" },
];

describe("VersionPicker — curated catalogue pills", () => {
  it("renders a radiogroup of pills when versions are present", () => {
    render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={() => {}} />,
    );
    // The aria-label is preserved on the radiogroup; each curated version
    // is a radio pill.
    expect(
      screen.getByRole("radiogroup", { name: /splice version/i }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(FIXTURES.length);
  });

  it("never renders a free-text <input> (regression guard)", () => {
    const { container } = render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={() => {}} />,
    );
    // The entire point of this file: the picker must never be a text input.
    expect(container.querySelector("input")).toBeNull();
  });

  it("renders a pill for every curated version", () => {
    render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={() => {}} />,
    );
    for (const f of FIXTURES) {
      expect(
        screen.getByRole("radio", { name: new RegExp(f.tag.replace(/\./g, "\\.")) }),
      ).toBeInTheDocument();
    }
  });

  it("marks the selected version via aria-checked", () => {
    render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={() => {}} />,
    );
    expect(screen.getByRole("radio", { name: /0\.6\.4/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /0\.6\.3/ })).not.toBeChecked();
  });

  it("sorts 'latest' first regardless of tag-string order", () => {
    // Shuffle: put latest last to prove the sort runs.
    const shuffled: SpliceVersionEntry[] = [
      { tag: "0.5.18", status: "supported", major: "0.5", commit: "fed7" },
      { tag: "0.6.3", status: "supported", major: "0.6", commit: "def4" },
      { tag: "0.6.4", status: "latest", major: "0.6", commit: "abc1" },
    ];
    render(
      <VersionPicker versions={shuffled} selected="0.6.4" onSelect={() => {}} />,
    );
    expect(screen.getAllByRole("radio")[0]).toHaveTextContent("0.6.4");
  });

  it("labels the latest pill and shows the selected version's status + sha", () => {
    render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={() => {}} />,
    );
    expect(screen.getByRole("radio", { name: /0\.6\.4/ })).toHaveTextContent(
      /latest/i,
    );
    // Metadata line — real status label + short commit sha.
    expect(screen.getByText(/latest curated · sha abc1234/i)).toBeInTheDocument();
  });

  it("fires onSelect with the chosen tag when a pill is clicked", async () => {
    const onSelect = vi.fn();
    render(
      <VersionPicker versions={FIXTURES} selected="0.6.4" onSelect={onSelect} />,
    );
    await userEvent.click(screen.getByRole("radio", { name: /0\.5\.18/ }));
    expect(onSelect).toHaveBeenCalledWith("0.5.18");
  });

  it("keeps non-curated 'available' tags out of the pills but reachable via a 'more' select", async () => {
    const onSelect = vi.fn();
    const withAvailable: SpliceVersionEntry[] = [
      ...FIXTURES,
      { tag: "0.6.14", status: "available", major: "", commit: "999aaaa1111" },
    ];
    render(
      <VersionPicker versions={withAvailable} selected="0.6.4" onSelect={onSelect} />,
    );
    // Still only the 3 curated pills.
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.queryByRole("radio", { name: /0\.6\.14/ })).toBeNull();
    // The available tag lives in the "more" select and still selects.
    const more = screen.getByRole("combobox", { name: /more splice versions/i });
    await userEvent.selectOptions(more, "0.6.14");
    expect(onSelect).toHaveBeenCalledWith("0.6.14");
  });

  it("renders a disabled <select> when versions are empty (NOT an <input>)", () => {
    const { container } = render(
      <VersionPicker versions={[]} selected="" onSelect={() => {}} />,
    );
    const select = screen.getByLabelText(/splice version/i);
    expect(select.tagName).toBe("SELECT");
    expect(select).toBeDisabled();
    // The empty state must not fall back to a free-text input —
    // pinning this is the entire point of this test file.
    expect(container.querySelector("input")).toBeNull();
  });

  it("shows a loading placeholder option while loading", () => {
    render(
      <VersionPicker versions={[]} selected="" onSelect={() => {}} loading />,
    );
    expect(screen.getByText(/loading curated versions/i)).toBeTruthy();
  });

  it("shows a no-versions placeholder when loaded but empty (not stuck on Loading)", () => {
    render(<VersionPicker versions={[]} selected="" onSelect={() => {}} />);
    expect(screen.getByText(/no curated versions available/i)).toBeTruthy();
    expect(screen.queryByText(/loading/i)).toBeNull();
  });

  // Regression: a failed fetch (5xx) used to collapse into the same
  // empty state as loading, leaving "Loading…" on screen forever.
  it("distinguishes a fetch error from loading (still a <select>, never an input)", () => {
    const { container } = render(
      <VersionPicker
        versions={[]}
        selected=""
        onSelect={() => {}}
        loading={false}
        error="server returned 503"
      />,
    );
    expect(container.querySelector("input")).toBeNull();
    expect(container.querySelector("select")).not.toBeNull();
    expect(screen.getByText(/couldn't load versions/i)).toBeTruthy();
    expect(screen.getByText(/503/)).toBeTruthy();
    // And NOT the loading text.
    expect(screen.queryByText(/loading curated versions/i)).toBeNull();
  });

  it("loading takes precedence and shows the loading placeholder", () => {
    render(<VersionPicker versions={[]} selected="" onSelect={() => {}} loading error={null} />);
    expect(screen.getByText(/loading curated versions/i)).toBeTruthy();
  });
});

describe("compareSpliceTags (semver-aware ordering)", () => {
  // Higher comes back positive (a newer than b), matching localeCompare
  // sign conventions the picker's sort relies on.
  it("ranks a final release ABOVE its own pre-release", () => {
    // The bug: localeCompare(numeric) put 0.6.4-rc.1 after 0.6.4.
    expect(compareSpliceTags("0.6.4", "0.6.4-rc.1")).toBeGreaterThan(0);
    expect(compareSpliceTags("0.6.4-rc.1", "0.6.4")).toBeLessThan(0);
  });

  it("orders patch/minor/major numerically", () => {
    expect(compareSpliceTags("0.6.10", "0.6.9")).toBeGreaterThan(0); // 10 > 9, not string "10" < "9"
    expect(compareSpliceTags("0.7.0", "0.6.99")).toBeGreaterThan(0);
    expect(compareSpliceTags("1.0.0", "0.99.99")).toBeGreaterThan(0);
  });

  it("orders pre-releases of the same core (rc.2 > rc.1)", () => {
    expect(compareSpliceTags("0.6.4-rc.2", "0.6.4-rc.1")).toBeGreaterThan(0);
    expect(compareSpliceTags("0.6.4-rc.1", "0.6.4-rc.10")).toBeLessThan(0);
  });

  it("falls back to localeCompare for non-semver tags", () => {
    // No crash, deterministic order for tags like the catalogue's
    // channel aliases.
    expect(compareSpliceTags("token-standard-v2", "next-cilr")).not.toBe(0);
    expect(compareSpliceTags("next-cilr", "next-cilr")).toBe(0);
  });

  it("sorts a mixed list newest-first when used as the picker does", () => {
    const tags = ["0.6.3", "0.6.4-rc.1", "0.6.4", "0.6.10", "0.7.0"];
    const sorted = [...tags].sort((a, b) => compareSpliceTags(b, a));
    expect(sorted).toEqual(["0.7.0", "0.6.10", "0.6.4", "0.6.4-rc.1", "0.6.3"]);
  });
});
