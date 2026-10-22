import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { CampaignWatermark, downloadBlob, replaceOpaqueIframes } from "./CampaignWatermark";

const { domToBlob } = vi.hoisted(() => ({ domToBlob: vi.fn() }));
vi.mock("modern-screenshot", () => ({ domToBlob }));

const setupClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");

afterEach(() => {
  domToBlob.mockReset();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  if (setupClipboard) Object.defineProperty(navigator, "clipboard", setupClipboard);
});

type StubItem = { items: Record<string, Blob | Promise<Blob>> };

function stubImageClipboard(write: (items: StubItem[]) => Promise<void>) {
  vi.stubGlobal(
    "ClipboardItem",
    class {
      constructor(public items: Record<string, Blob | Promise<Blob>>) {}
    },
  );
  const spy = vi.fn(write);
  Object.defineProperty(navigator, "clipboard", { value: { write: spy }, configurable: true });
  return spy;
}

const consumeItems = async (items: StubItem[]) => {
  await items[0].items["image/png"];
};

function renderAt(code: string, path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <CampaignWatermark code={code} />
    </MemoryRouter>,
  );
}

function shareButton() {
  return screen.getByRole("button", { name: /share your run/i });
}

async function openPrompt(code: string, path = "/") {
  domToBlob.mockRejectedValue(new Error("capture failed"));
  renderAt(code, path);
  fireEvent.click(shareButton());
  return screen.findByRole("link", { name: /post on x/i });
}

function decodedHref(link: HTMLElement): string {
  return decodeURIComponent(link.getAttribute("href") ?? "");
}

describe("CampaignWatermark", () => {
  it("shows the code and a pre-filled X intent on Post on X with tags + hashtag (no auth)", async () => {
    const post = await openPrompt("CCT-A7Q2M9XK");
    expect(screen.getByText("CCT-A7Q2M9XK")).toBeInTheDocument();

    const raw = post.getAttribute("href") ?? "";
    expect(raw).toMatch(/^https:\/\/x\.com\/intent\/tweet\?/);
    expect(raw).toContain("hashtags=CCTools");

    const decoded = decodedHref(post);
    expect(decoded).toContain("CCT-A7Q2M9XK");
    expect(decoded).toContain("@CantonNetwork");
    expect(decoded).toContain("@bitdynamics_cc");
    expect(decoded).toMatch(/spun up a real Canton network/i);

    expect(post).toHaveAttribute("target", "_blank");
    expect(post.getAttribute("rel") ?? "").toContain("noopener");
  });

  it("tailors the share copy to the current screen", async () => {
    expect(decodedHref(await openPrompt("CCT-A7Q2M9XK", "/tokens"))).toMatch(/minted a token/i);
  });

  it("renders nothing without a code (normal builds show no watermark)", () => {
    const { container } = renderAt("");
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByRole("button", { name: /share your run/i })).toBeNull();
  });

  it("percent-encodes the code so it can't break out of the share URL query", async () => {
    const raw = (await openPrompt("CCT-A&hashtags=evil")).getAttribute("href") ?? "";
    expect(raw).toContain("%26hashtags%3Devil");
    expect(raw).not.toContain("&hashtags=evil");
  });

  it("doesn't claim Share opens X (the user opens it from the prompt)", () => {
    renderAt("CCT-A7Q2M9XK");
    expect(shareButton()).toHaveAttribute("title", "Capture this page and get a pre-filled X post");
  });

  it("lets the code be mouse-selected without the pill swallowing clicks", () => {
    renderAt("CCT-A7Q2M9XK");
    const codeEl = screen.getByText("CCT-A7Q2M9XK");
    expect(codeEl.style.pointerEvents).toBe("auto");
    expect(codeEl.style.userSelect).toBe("text");
    expect(screen.getByRole("note").style.pointerEvents).toBe("none");
  });
});

describe("CampaignWatermark share", () => {
  it("captures once when Share is clicked again mid-capture, with the iframe hook", async () => {
    let finish: (b: Blob) => void = () => {};
    domToBlob.mockReturnValue(new Promise<Blob>((r) => (finish = r)));
    const write = stubImageClipboard(consumeItems);

    renderAt("CCT-A7Q2M9XK");
    const share = shareButton();
    fireEvent.click(share);
    fireEvent.click(share);
    await vi.waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    expect(domToBlob.mock.calls[0][1].onCloneNode).toBe(replaceOpaqueIframes);

    finish(new Blob(["png"], { type: "image/png" }));
    expect(await screen.findByRole("link", { name: /post on x/i })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/copied to your clipboard/i);
    expect(write).toHaveBeenCalledTimes(1);
    expect(domToBlob).toHaveBeenCalledTimes(1);
  });

  it("starts the clipboard write inside the click with a pending PNG (Safari user-gesture rule)", async () => {
    let finish: (b: Blob) => void = () => {};
    domToBlob.mockReturnValue(new Promise<Blob>((r) => (finish = r)));
    const write = stubImageClipboard(consumeItems);

    renderAt("CCT-A7Q2M9XK");
    fireEvent.click(shareButton());

    expect(write).toHaveBeenCalledTimes(1);
    expect(domToBlob).not.toHaveBeenCalled();
    const png = write.mock.calls[0][0][0].items["image/png"];
    expect(png).toBeInstanceOf(Promise);

    const blob = new Blob(["png"], { type: "image/png" });
    await vi.waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    finish(blob);
    await expect(png).resolves.toBe(blob);
    expect(await screen.findByRole("link", { name: /post on x/i })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/copied to your clipboard/i);
  });

  it("downloads the capture when the clipboard refuses the image", async () => {
    const blob = new Blob(["png"], { type: "image/png" });
    domToBlob.mockResolvedValue(blob);
    stubImageClipboard(() => Promise.reject(new DOMException("denied", "NotAllowedError")));
    const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:shot");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const downloads: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push(this.download);
    });

    renderAt("CCT-A7Q2M9XK");
    fireEvent.click(shareButton());

    expect(await screen.findByRole("link", { name: /post on x/i })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/saved to your downloads/i);
    expect(create).toHaveBeenCalledWith(blob);
    expect(downloads).toEqual(["canton-devkit-CCT-A7Q2M9XK.png"]);
  });

  it("reports an error, without downloading, when the page can't be captured", async () => {
    domToBlob.mockRejectedValue(new Error("tainted canvas"));
    const write = stubImageClipboard(consumeItems);
    const create = vi.spyOn(URL, "createObjectURL");

    renderAt("CCT-A7Q2M9XK");
    fireEvent.click(shareButton());

    expect(await screen.findByRole("link", { name: /post on x/i })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/couldn.t capture the page/i);
    expect(write).toHaveBeenCalledTimes(1);
    expect(create).not.toHaveBeenCalled();
  });

  it("announces capturing and the outcome through one always-mounted live region", async () => {
    let finish: (b: Blob) => void = () => {};
    domToBlob.mockReturnValue(new Promise<Blob>((r) => (finish = r)));
    stubImageClipboard(consumeItems);

    renderAt("CCT-A7Q2M9XK");
    const status = screen.getByRole("status");
    expect(status).toBeEmptyDOMElement();

    fireEvent.click(shareButton());
    expect(status).toHaveTextContent(/capturing the page/i);

    await vi.waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(1));
    finish(new Blob(["png"], { type: "image/png" }));
    await vi.waitFor(() => expect(status).toHaveTextContent(/copied to your clipboard/i));
    expect(screen.getByRole("status")).toBe(status);
  });

  it("is a real button, so Space and Enter start the capture", async () => {
    const user = userEvent.setup();
    domToBlob.mockRejectedValue(new Error("capture failed"));
    renderAt("CCT-A7Q2M9XK");
    const share = shareButton();
    expect(share.tagName).toBe("BUTTON");

    share.focus();
    await user.keyboard(" ");
    await screen.findByRole("link", { name: /post on x/i });
    expect(domToBlob).toHaveBeenCalledTimes(1);

    share.focus();
    await user.keyboard("{Enter}");
    await vi.waitFor(() => expect(domToBlob).toHaveBeenCalledTimes(2));
  });

  it("moves focus to Post on X, which follows Share in the DOM", async () => {
    domToBlob.mockRejectedValue(new Error("capture failed"));
    renderAt("CCT-A7Q2M9XK");
    const share = shareButton();
    share.focus();
    fireEvent.click(share);

    const post = await screen.findByRole("link", { name: /post on x/i });
    expect(post).toHaveFocus();
    expect(share.compareDocumentPosition(post) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("closes the prompt from the Dismiss button and hands focus back to Share", async () => {
    await openPrompt("CCT-A7Q2M9XK");
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("link", { name: /post on x/i })).toBeNull();
    expect(shareButton()).toHaveFocus();
  });

  it("closes the prompt on Escape", async () => {
    const user = userEvent.setup();
    await openPrompt("CCT-A7Q2M9XK");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("link", { name: /post on x/i })).toBeNull();
    expect(shareButton()).toHaveFocus();
  });

  it("keeps the Escape that closes the prompt from reaching window-level Escape handlers", async () => {
    const user = userEvent.setup();
    const onWindowKey = vi.fn();
    window.addEventListener("keydown", onWindowKey);
    try {
      const post = await openPrompt("CCT-A7Q2M9XK");
      expect(post).toHaveFocus();
      await user.keyboard("{Escape}");
      expect(screen.queryByRole("link", { name: /post on x/i })).toBeNull();
      expect(onWindowKey).not.toHaveBeenCalled();

      await user.keyboard("{Escape}");
      expect(onWindowKey).toHaveBeenCalledTimes(1);
    } finally {
      window.removeEventListener("keydown", onWindowKey);
    }
  });

  it("stays open until dismissed, then closes shortly after Post on X is used", async () => {
    const post = await openPrompt("CCT-A7Q2M9XK");
    post.addEventListener("click", (e) => e.preventDefault());
    vi.useFakeTimers();
    try {
      act(() => vi.advanceTimersByTime(60000));
      expect(screen.getByRole("link", { name: /post on x/i })).toBe(post);

      fireEvent.click(post);
      act(() => vi.advanceTimersByTime(800));
      expect(screen.queryByRole("link", { name: /post on x/i })).toBeNull();
      expect(shareButton()).toHaveFocus();
    } finally {
      vi.useRealTimers();
    }
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
