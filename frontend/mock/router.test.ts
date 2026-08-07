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

  it("GET /api/tokens/identity returns roles for the requested instance", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/tokens/identity?instance=demo&role=app-provider");
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body);
    expect(body.schema_version).toBe(1);
    expect(body.instance).toBe("demo");
    expect(body.available_roles).toContain("app-user");
    expect(body.current_role).toBe("app-provider");
  });

  it("GET /api/tokens/allocations returns the seeded list", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/tokens/allocations?instance=demo");
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body);
    expect(Array.isArray(body.allocations)).toBe(true);
    expect(typeof body.aliases).toBe("object");
  });

  it("GET /api/tokens/transfers returns the pending offers list", () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/tokens/transfers?instance=demo");
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body);
    expect(Array.isArray(body.pending_transfers)).toBe(true);
    expect(typeof body.aliases).toBe("object");
  });

  it("POST /api/tokens/identity does not fall through to the token-detail route", () => {
    // The generic /api/tokens/{symbol} regex must not swallow /identity.
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("GET"), res, "/api/tokens/identity");
    const body = JSON.parse(res.body);
    expect(body.available_roles).toBeDefined();
    expect(body.symbol).toBeUndefined();
  });

  it("POST /api/tokens returns a full TokenRef", async () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(
      mockReq("POST", JSON.stringify({ name: "RTK", symbol: "RTK", decimals: 6, initial_supply: "1000", issuer: "alice::abc" })),
      res,
      "/api/tokens?instance=demo",
    );
    await new Promise((r) => setTimeout(r, 10));
    expect(res.status).toBe(201);
    const body = JSON.parse(res.body);
    expect(body.symbol).toBe("RTK");
    expect(body.issuer_party).toBe("alice::abc");
    expect(body.instrument_id).toBe("RTK");
  });

  it("POST /api/tokens/demo returns the DemoResult shape", async () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("POST", "{}"), res, "/api/tokens/demo?instance=demo");
    await new Promise((r) => setTimeout(r, 10));
    expect(res.status).toBe(201);
    const body = JSON.parse(res.body);
    expect(body.token.symbol).toBeDefined();
    expect(body.issuer.party_id).toBeDefined();
    expect(body.holder).toBeDefined();
    expect(body.seeded).toBe(true);
  });

  it("POST /api/tokens/{symbol}/allocate returns an allocation id", async () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(mockReq("POST", "{}"), res, "/api/tokens/DEMO/allocate?instance=demo");
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body);
    expect(body.allocation_id).toBeDefined();
  });

  it("POST /api/tokens/allocations/{id}/withdraw and /cancel return 204", () => {
    const router = createMockRouter();
    for (const action of ["withdraw", "cancel"]) {
      const res = mockRes();
      router.handle(
        mockReq("POST", "{}"),
        res,
        `/api/tokens/allocations/00allocation1/${action}?instance=demo`,
      );
      expect(res.status).toBe(204);
    }
  });

  it("POST /api/tokens/{symbol}/transfer returns transfer_instruction_id + settled", async () => {
    const router = createMockRouter();
    const res = mockRes();
    router.handle(
      mockReq("POST", JSON.stringify({ from: "bob", to: "alice", amount: "100" })),
      res,
      "/api/tokens/RTK/transfer?instance=demo",
    );
    expect(res.status).toBe(201);
    const body = JSON.parse(res.body);
    expect(body.transfer_instruction_id).toBe("tx-mock-1");
    expect(body.settled).toBe(false);
  });
});
