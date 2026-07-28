import type { ServerResponse } from "node:http";

export function jsonResponse(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

export function textResponse(res: ServerResponse, status: number, body: string): void {
  res.writeHead(status, {
    "Content-Type": "text/plain; charset=utf-8",
    "Content-Length": Buffer.byteLength(body),
  });
  res.end(body);
}

export function noContent(res: ServerResponse): void {
  res.writeHead(204);
  res.end();
}

export function notFound(res: ServerResponse): void {
  jsonResponse(res, 404, {
    code: "NOT_FOUND",
    error: "not found",
  });
}

export function parseJsonBody<T>(raw: string): T | undefined {
  if (!raw) return undefined;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return undefined;
  }
}

export async function readRequestBody(req: { on: Function }): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    req.on("error", reject);
  });
}

export function sendSseEvent(
  res: ServerResponse,
  data: unknown,
  opts?: { id?: string; event?: string },
): void {
  if (opts?.id) res.write(`id: ${opts.id}\n`);
  if (opts?.event) res.write(`event: ${opts.event}\n`);
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

export function beginSse(res: ServerResponse): void {
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
  });
}

export function scheduleKeepalive(res: ServerResponse, intervalMs = 30000): NodeJS.Timeout {
  return setInterval(() => {
    res.write(": keepalive\n\n");
  }, intervalMs);
}
