import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupRestore } from "./BackupRestore";

// BackupRestore — UI parity for `localnet snapshot` /
// `localnet restore`. These tests pin three contracts:
//
//   1. Download dispatches a POST form (not a GET nav), so the
//      browser keeps the SPA mounted and surfaces the file via the
//      downloads bar.
//   2. Restore upload posts multipart with the expected field names
//      (`file`, `name`, optionally `force`) — the field names are a
//      wire contract the Go handler reads back, so a frontend rename
//      would break parity silently.
//   3. The success/error/uploading states render the announced ARIA
//      roles so screen readers + tests can target them.

describe("BackupRestore card", () => {
  beforeEach(() => {
    // Clean up any iframes left over from previous tests so we get
    // a deterministic "first call creates the iframe" assertion.
    document
      .querySelectorAll('iframe[name="_dpm_dl"]')
      .forEach((n) => n.remove());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the download button labeled for the current instance", () => {
    render(<BackupRestore instanceName="pebble" />);
    expect(
      screen.getByRole("button", { name: /download snapshot/i }),
    ).toBeTruthy();
    // The "mirrors CLI" hint surfaces the exact CLI command so the
    // user can see the equivalence without leaving the page.
    expect(
      screen.getByText(
        /dpm localnet snapshot --name pebble --to \.\/pebble\.tgz/,
      ),
    ).toBeTruthy();
  });

  it("dispatches a POST form to /api/instances/:name/snapshot on download", async () => {
    // Spy HTMLFormElement.prototype.submit because jsdom doesn't
    // actually navigate; we just want to assert the form is built
    // correctly. Spying on the prototype lets us read the
    // dispatching form's action/method right before submit.
    const submitSpy = vi
      .spyOn(HTMLFormElement.prototype, "submit")
      .mockImplementation(function (this: HTMLFormElement) {
        // capture for assertion outside the spy
        (window as any).__lastForm = {
          method: this.method,
          action: this.action,
          target: this.target,
        };
      });

    render(<BackupRestore instanceName="pebble" />);
    await userEvent.click(
      screen.getByRole("button", { name: /download snapshot/i }),
    );
    expect(submitSpy).toHaveBeenCalled();
    const f = (window as any).__lastForm;
    expect(f.method.toLowerCase()).toBe("post");
    expect(f.action).toMatch(/\/api\/instances\/pebble\/snapshot$/);
    // Target a hidden iframe so the SPA stays mounted; if a future
    // refactor drops this, the page would navigate away on download.
    expect(f.target).toBe("_dpm_dl");
    // Iframe must exist after the first download so subsequent
    // downloads reuse it (no leaks).
    expect(document.querySelector('iframe[name="_dpm_dl"]')).not.toBeNull();
  });

  it("surfaces a download-failure banner when the server returns an error instead of a file", async () => {
    // Regression: the hidden-iframe download swallowed server errors
    // (instance gone, docker failure, 5xx) — the button just flashed
    // and the user assumed success. The iframe navigates to the JSON
    // error body and fires `load`; the card must show an alert.
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(
      function (this: HTMLFormElement) {
        const frame = document.querySelector(
          'iframe[name="_dpm_dl"]',
        ) as HTMLIFrameElement;
        frame.dispatchEvent(new Event("load"));
      },
    );
    render(<BackupRestore instanceName="ghost" />);
    await userEvent.click(
      screen.getByRole("button", { name: /download snapshot/i }),
    );
    await waitFor(() => {
      const alert = screen.getByRole("alert");
      expect(alert.textContent).toMatch(/download failed/i);
    });
  });

  it("posts multipart with file + name fields when restoring (no --force by default)", async () => {
    const fd: FormData[] = [];
    const sendSpy = vi
      .spyOn(XMLHttpRequest.prototype, "send")
      .mockImplementation(function (this: XMLHttpRequest, body) {
        fd.push(body as FormData);
        // Simulate a 200 response with a JSON body so the
        // promise resolves and the "success" state renders.
        Object.defineProperty(this, "status", { value: 200 });
        Object.defineProperty(this, "responseText", {
          value: JSON.stringify({ name: "pebble", restored: true }),
        });
        this.dispatchEvent(new ProgressEvent("load"));
      });

    render(<BackupRestore instanceName="pebble" />);
    const file = new File(
      [new Uint8Array([0x1f, 0x8b, 0x08])], // gzip magic bytes
      "pebble.tgz",
      { type: "application/gzip" },
    );
    const dropZone = screen.getByLabelText(/drop snapshot file/i);
    // Trigger via the hidden file input (drop event is fiddly under
    // jsdom — click+input is the documented testing-library path
    // for file uploads).
    const input = dropZone.parentElement?.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await userEvent.upload(input, file);

    expect(sendSpy).toHaveBeenCalledTimes(1);
    const captured = fd[0];
    expect(captured.get("name")).toBe("pebble");
    expect(captured.get("file")).toBeInstanceOf(File);
    // Without the user checking --force, the field must be
    // absent so the server takes its safe default.
    expect(captured.get("force")).toBeNull();

    // Success state surfaces the announced status role.
    await waitFor(() => {
      expect(screen.getByRole("status")).toBeTruthy();
    });
  });

  it("includes force=true when the --force checkbox is checked", async () => {
    const fd: FormData[] = [];
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(
      function (this: XMLHttpRequest, body) {
        fd.push(body as FormData);
        Object.defineProperty(this, "status", { value: 200 });
        Object.defineProperty(this, "responseText", {
          value: JSON.stringify({ name: "pebble", restored: true }),
        });
        this.dispatchEvent(new ProgressEvent("load"));
      },
    );

    render(<BackupRestore instanceName="pebble" />);
    await userEvent.click(
      screen.getByLabelText(/force restore on version mismatch/i),
    );
    const file = new File([new Uint8Array([0x1f, 0x8b])], "p.tgz");
    const input = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await userEvent.upload(input, file);
    expect(fd[0].get("force")).toBe("true");
  });

  it("surfaces a 4xx error via role=alert with the server message", async () => {
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(
      function (this: XMLHttpRequest) {
        Object.defineProperty(this, "status", { value: 400 });
        Object.defineProperty(this, "responseText", {
          value: JSON.stringify({
            code: "INVALID_REQUEST",
            error: "invalid instance name",
          }),
        });
        this.dispatchEvent(new ProgressEvent("load"));
      },
    );

    render(<BackupRestore instanceName="pebble" />);
    const file = new File([new Uint8Array([0])], "bogus.tgz");
    const input = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await userEvent.upload(input, file);
    await waitFor(() => {
      const alert = screen.getByRole("alert");
      expect(alert.textContent).toMatch(/invalid instance name/i);
    });
  });

  it("resyncs target name when the parent switches instances", async () => {
    // Repro for the user-reported bug: open the UI on "dev", click
    // pebble, BackupRestore's targetName state was stuck at "dev"
    // because useState only honors its initial value on first mount.
    // After the fix, switching instances must update the input.
    const { rerender } = render(<BackupRestore instanceName="dev" />);
    const input = screen.getByLabelText(
      /restore target instance name/i,
    ) as HTMLInputElement;
    expect(input.value).toBe("dev");

    rerender(<BackupRestore instanceName="pebble" />);
    expect(input.value).toBe("pebble");
  });

  it("lets the user override the target instance name before restoring", async () => {
    const fd: FormData[] = [];
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(
      function (this: XMLHttpRequest, body) {
        fd.push(body as FormData);
        Object.defineProperty(this, "status", { value: 200 });
        Object.defineProperty(this, "responseText", {
          value: JSON.stringify({ name: "pebble-clone", restored: true }),
        });
        this.dispatchEvent(new ProgressEvent("load"));
      },
    );

    render(<BackupRestore instanceName="pebble" />);
    const nameInput = screen.getByLabelText(/restore target instance name/i);
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "pebble-clone");
    const file = new File([new Uint8Array([0])], "x.tgz");
    const fileInput = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    await userEvent.upload(fileInput, file);
    expect(fd[0].get("name")).toBe("pebble-clone");
  });
});
