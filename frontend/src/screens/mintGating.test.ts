import { describe, expect, it } from "vitest";
import { mintDisabledReason } from "./TokensScreen";
import type { InstrumentRef } from "../api";

// mintDisabledReason gates on the machine generation tag, not the human
// display label — so renaming the standard label can't silently break the
// mint/burn guards (the bug we'd otherwise introduce relabelling V1/V2).
describe("mintDisabledReason", () => {
  const base: InstrumentRef = { admin: "a", instrument_id: "X", on_ledger: true };

  it("disables mint for a V1 token (Amulet) — no user mint", () => {
    const amulet: InstrumentRef = {
      ...base,
      symbol: "Amulet",
      instrument_id: "Amulet",
      standard: "Splice Amulet",
      generation: "v1",
    };
    expect(mintDisabledReason(amulet)).toMatch(/no standard mint/i);
  });

  it("enables mint for an on-ledger V2 token", () => {
    const v2: InstrumentRef = {
      ...base,
      symbol: "MYT",
      standard: "Token Standard V2 (CIP-0112)",
      generation: "v2",
      on_ledger: true,
    };
    expect(mintDisabledReason(v2)).toBeNull();
  });

  it("shows the recorded-only hint for a recorded V2 token", () => {
    const recorded: InstrumentRef = {
      ...base,
      symbol: "MYT",
      standard: "Token Standard V2 (CIP-0112)",
      generation: "v2",
      on_ledger: false,
    };
    expect(mintDisabledReason(recorded)).toMatch(/recorded only/i);
  });

  it("disables mint when generation is missing (safe default)", () => {
    const unknown: InstrumentRef = { ...base, symbol: "Q", standard: "?" };
    expect(mintDisabledReason(unknown)).toMatch(/no standard mint/i);
  });
});
