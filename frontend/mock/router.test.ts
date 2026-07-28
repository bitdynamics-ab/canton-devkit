import { describe, expect, it } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import { EventEmitter } from "node:events";
import { createMockRouter } from "./router.ts";

function mockReq(method: string, body = ""): IncomingMessage {
  const req = new EventEmitter() as IncomingMessage & EventEmitter;
  req.method = method;
  process.nextTick(() => {
    if (body) req.emit("data", Buffer.from(body));
    req.emit("end");
  });
  return req;
}

function mockRes(): ServerResponse & { status: number; headers: Record<string, string>; body: string } {
  const res = {
    status: 0,
    headers: {} as Record<string, string>,
    body: "",
    writeHead(status: number, headers?: Record<string, string>) {
      this.status = status;
      if (headers) Object.assign(this.headers, headers);
    },
    end(chunk?: string | Buffer) {
      if (chunk) this.body += String(chunk);
    },
    write(chunk: string) {
      this.body += chunk;
    },
    on() {
      return this;
    },
  };
  return res as ServerResponse & typeof res;
}

describe("mock router", () => {
  it("GET /api/version returns schema 1", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/version");
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body);
    expect(body.schema_version).toBe(1);
  });

  it("GET /api/instances returns seeded demo instance", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/instances");
    const body = JSON.parse(res.body);
    expect(body.instances.some((i: { name: string }) => i.name === "demo")).toBe(true);
  });

  it("POST /api/instances returns 202 and adds instance", async () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(
      mockReq("POST", JSON.stringify({ name: "new-mock", version: "0.6.4" })),
      res,
      "/api/instances",
    );
    await new Promise((r) => setTimeout(r, 10));
    expect(res.status).toBe(202);
    const body = JSON.parse(res.body);
    expect(body.events_url).toContain("/api/instances/new-mock/events");

    const listRes = mockRes();
    router.handle(mockReq("GET"), listRes, "/api/instances");
    const list = JSON.parse(listRes.body);
    expect(list.instances.some((i: { name: string }) => i.name === "new-mock")).toBe(true);
  });

  it("unmatched route returns 404 envelope", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/unknown-endpoint");
    expect(res.status).toBe(404);
    const body = JSON.parse(res.body);
    expect(body.code).toBe("NOT_FOUND");
  });
});
