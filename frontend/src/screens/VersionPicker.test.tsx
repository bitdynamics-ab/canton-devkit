import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { SpliceVersionEntry } from "../api";
import { VersionPicker, compareSpliceTags } from "./CreateLocalNetModal";

// VersionPicker — pins the "always a <select> dropdown, never a
// free-text <input>" invariant for the curated Splice catalogue
// picker (an earlier build regressed to an <input> fallback when the
// version list came back empty).
//
// Two contracts under test:
//   1. With versions: native HTML <select> + one <option> per entry,
//      "latest" first, semver-desc next; onChange fires onSelect.
//   2. Without versions (loading / API-error): still a <select>,
//      disabled, with a single placeholder option — never an <input>.

const FIXTURES: SpliceVersionEntry[] = [
  { tag: "0.6.4", status: "latest", major: "0.6", commit: "abc1234567890" },
  { tag: "0.6.3", status: "supported", major: "0.6", commit: "def4567890123" },
  { tag: "0.5.18", status: "supported", major: "0.5", commit: "fed7890123456" },
];

describe("VersionPicker — curated catalogue dropdown", () => {
  it("renders a native <select> when versions are present", () => {
    render(
      <VersionPicker
        versions={FIXTURES}
        selected="0.6.4"
        onSelect={() => {}}
      />,
    );

    // Accessible-name lookup plus a tagName pin: a refactor to a
    // custom combobox must preserve the aria-label, and the "must be
    // a <select>" assertion enforces native semantics.
    const select = screen.getByLabelText(/splice version/i);
    expect(select.tagName).toBe("SELECT");
    expect(select).not.toBeDisabled();
  });

  it("never renders an <input> when versions are present (regression guard)", () => {
    const { container } = render(
      <VersionPicker
        versions={FIXTURES}
        selected="0.6.4"
        onSelect={() => {}}
      />,
    );
    // Whatever the picker renders, it must NOT be a text input.
    expect(container.querySelector("input")).toBeNull();
  });

  it("lists every supplied version as an <option>", () => {
    render(
      <VersionPicker
        versions={FIXTURES}
        selected="0.6.4"
        onSelect={() => {}}
      />,
    );
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(FIXTURES.length);
    expect(options.map((o) => o.getAttribute("value"))).toEqual(
      expect.arrayContaining(["0.6.4", "0.6.3", "0.5.18"]),
    );
  });

  it("sorts 'latest' first regardless of tag-string order", () => {
    // Shuffle: put latest last to prove the sort runs.
    const shuffled: SpliceVersionEntry[] = [
      { tag: "0.5.18", status: "supported", major: "0.5", commit: "fed7" },
      { tag: "0.6.3", status: "supported", major: "0.6", commit: "def4" },
      { tag: "0.6.4", status: "latest", major: "0.6", commit: "abc1" },
    ];
    render(
      <VersionPicker
        versions={shuffled}
        selected="0.6.4"
        onSelect={() => {}}
      />,
    );
    const options = screen.getAllByRole("option");
    expect(options[0].getAttribute("value")).toBe("0.6.4");
  });

  it("annotates the latest entry with a ' (latest)' suffix", () => {
    render(
      <VersionPicker
        versions={FIXTURES}
        selected="0.6.4"
        onSelect={() => {}}
      />,
    );
    expect(screen.getByText(/0\.6\.4 \(latest\)/)).toBeTruthy();
  });

  it("fires onSelect with the chosen tag on change", async () => {
    const onSelect = vi.fn();
    render(
      <VersionPicker
        versions={FIXTURES}
        selected="0.6.4"
        onSelect={onSelect}
      />,
    );
    const select = screen.getByLabelText(/splice version/i);
    await userEvent.selectOptions(select, "0.5.18");
    expect(onSelect).toHaveBeenCalledWith("0.5.18");
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
