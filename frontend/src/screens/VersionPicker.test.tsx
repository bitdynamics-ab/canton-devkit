import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { SpliceVersionEntry } from "../api";
import { VersionPicker } from "./CreateLocalNetModal";

// VersionPicker — the curated Splice catalogue picker that used to be
// a custom button-list and briefly regressed to a free-text <input>
// fallback when the API call returned empty. These tests pin the
// "always a <select> dropdown, never a textbox" invariant a user
// explicitly requested.
//
// Two contracts under test:
//   1. With versions: native HTML <select> + one <option> per entry,
//      "latest" first, semver-desc next; onChange fires onSelect.
//   2. Without versions (loading / API-error): still a <select>,
//      disabled, with a single placeholder option. We MUST NOT render
//      an <input> in this state — that's the regression we're guarding.

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

    // Accessible-name lookup is the right shape: a future refactor
    // that swaps the element type (e.g. to a custom combobox) would
    // need to preserve role="combobox" / aria-label for screen-reader
    // parity. The "must be a <select>" pin enforces native semantics.
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
    // The earlier implementation had a fallback `<input>` branch.
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
    // The regression: empty-state must not fall back to a free-text
    // input. Pinning this is the entire point of this test file —
    // users reported the textbox felt like a regression from the
    // previous dropdown UX.
    expect(container.querySelector("input")).toBeNull();
  });

  it("shows a loading placeholder option in the empty state", () => {
    render(
      <VersionPicker versions={[]} selected="" onSelect={() => {}} />,
    );
    expect(screen.getByText(/loading curated versions/i)).toBeTruthy();
  });
});
