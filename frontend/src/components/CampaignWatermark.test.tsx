import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { CampaignWatermark } from "./CampaignWatermark";

function renderAt(code: string, path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <CampaignWatermark code={code} />
    </MemoryRouter>,
  );
}

function shareHref(): string {
  return decodeURIComponent(
    screen.getByRole("link", { name: /share/i }).getAttribute("href") ?? "",
  );
}

describe("CampaignWatermark", () => {
  it("shows the code and a pre-filled X share link with tags + hashtag (no auth)", () => {
    renderAt("CCT-A7Q2M9XK"); // "/" → Overview default copy
    expect(screen.getByText("CCT-A7Q2M9XK")).toBeInTheDocument();

    const share = screen.getByRole("link", { name: /share/i });
    expect(share.getAttribute("href") ?? "").toMatch(/^https:\/\/x\.com\/intent\/tweet\?/);
    expect(share.getAttribute("href") ?? "").toContain("hashtags=CCTools");

    const decoded = shareHref();
    expect(decoded).toContain("CCT-A7Q2M9XK");
    expect(decoded).toContain("@CantonNetwork");
    expect(decoded).toContain("@bitdynamics_cc");
    expect(decoded).toMatch(/spun up a real Canton network/i); // Overview copy

    expect(share).toHaveAttribute("target", "_blank");
    expect(share.getAttribute("rel") ?? "").toContain("noopener");
  });

  it("tailors the share copy to the current screen", () => {
    renderAt("CCT-A7Q2M9XK", "/tokens");
    expect(shareHref()).toMatch(/minted a token/i);
  });

  it("renders nothing without a code (normal builds show no watermark)", () => {
    const { container } = renderAt("");
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByRole("link", { name: /share/i })).toBeNull();
  });

  it("percent-encodes the code so it can't break out of the share URL query", () => {
    renderAt("CCT-A&hashtags=evil");
    const raw = screen.getByRole("link", { name: /share/i }).getAttribute("href") ?? "";
    expect(raw).toContain("%26hashtags%3Devil");
    expect(raw).not.toContain("&hashtags=evil");
  });
});
