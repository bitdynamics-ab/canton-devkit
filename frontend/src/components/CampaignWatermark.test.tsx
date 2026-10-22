import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { CampaignWatermark, downloadBlob, replaceOpaqueIframes } from "./CampaignWatermark";

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));
vi.mock("modern-screenshot", () => ({ domToBlob }));

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
    renderAt("CCT-A7Q2M9XK");
    expect(screen.getByText("CCT-A7Q2M9XK")).toBeInTheDocument();

    const share = screen.getByRole("link", { name: /share/i });
    expect(share.getAttribute("href") ?? "").toMatch(/^https:\/\/x\.com\/intent\/tweet\?/);
    expect(share.getAttribute("href") ?? "").toContain("hashtags=CCTools");

    const decoded = shareHref();
    expect(decoded).toContain("CCT-A7Q2M9XK");
    expect(decoded).toContain("@CantonNetwork");
    expect(decoded).toContain("@bitdynamics_cc");
    expect(decoded).toMatch(/spun up a real Canton network/i);

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

  it("doesn't claim Share opens X (the user opens it from the prompt)", () => {
    renderAt("CCT-A7Q2M9XK");
    expect(screen.getByRole("link", { name: /share/i })).toHaveAttribute(
      "title",
      "Capture this page and get a pre-filled X post",
    );
  });
});

describe("CampaignWatermark share", () => {
  afterEach(() => {
    domToBlob.mockReset();
    vi.unstubAllGlobals();
  });

  it("captures once when Share is clicked again mid-capture, with the iframe hook", async () => {
    let finish: (b: Blob) => void = () => {};
    domToBlob.mockReturnValue(new Promise<Blob>((r) => (finish = r)));
    const write = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("ClipboardItem", class { constructor(public items: unknown) {} });
    Object.defineProperty(navigator, "clipboard", { value: { write }, configurable: true });

    renderAt("CCT-A7Q2M9XK");
    const share = screen.getByRole("link", { name: /share/i });
    fireEvent.click(share);
    fireEvent.click(share);
    await vi.waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    expect(domToBlob.mock.calls[0][1].onCloneNode).toBe(replaceOpaqueIframes);

    finish(new Blob(["png"], { type: "image/png" }));
    expect(await screen.findByText(/copied to your clipboard/i)).toBeInTheDocument();
    expect(write).toHaveBeenCalledTimes(1);
    expect(domToBlob).toHaveBeenCalledTimes(1);
  });
});

describe("replaceOpaqueIframes", () => {
  it("swaps an unreadable iframe for a same-size labelled card", () => {
    const root = document.createElement("div");
    root.innerHTML = `<p>keep</p><iframe title="Splice Wallet — app-user" style="width: 640px; height: 480px;"></iframe>`;
    replaceOpaqueIframes(root);

    expect(root.querySelector("iframe")).toBeNull();
    const card = root.querySelector<HTMLElement>("[data-iframe-placeholder]");
    expect(card).not.toBeNull();
    expect(card!.textContent).toBe("Splice Wallet — app-user");
    expect(card!.style.width).toBe("640px");
    expect(card!.style.height).toBe("480px");
    expect(root.querySelector("p")?.textContent).toBe("keep");
  });

  it("ignores non-element nodes", () => {
    expect(() => replaceOpaqueIframes(document.createTextNode("x"))).not.toThrow();
  });
});

describe("downloadBlob", () => {
  it("revokes the object URL only after the download has started", () => {
    vi.useFakeTimers();
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const revoke = vi.fn();
    vi.stubGlobal("URL", { createObjectURL: () => "blob:x", revokeObjectURL: revoke });
    try {
      downloadBlob(new Blob(["png"]), "shot.png");
      expect(revoke).not.toHaveBeenCalled();
      vi.advanceTimersByTime(30000);
      expect(revoke).toHaveBeenCalledWith("blob:x");
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
      vi.restoreAllMocks();
    }
  });
});
