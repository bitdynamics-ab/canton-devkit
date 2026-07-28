import type { ServerResponse } from "node:http";
import {
  beginSse,
  scheduleKeepalive,
  sendSseEvent,
} from "./http.ts";
import type { MockStore } from "./store.ts";

const CREATE_PROGRESS: Array<Record<string, unknown>> = [
  { kind: "step.started", step: "preflight" },
  { kind: "step.finished", step: "preflight" },
  { kind: "step.started", step: "compose_up" },
  { kind: "step.progress", step: "compose_up", percent: 50 },
  { kind: "step.finished", step: "compose_up" },
  { kind: "done", detail: "Instance is running" },
];

export function handleInstanceProgressSse(
  res: ServerResponse,
  store: MockStore,
  instance: string,
): void {
  beginSse(res);
  const keepalive = scheduleKeepalive(res);
  const queued = store.progressQueues.get(instance);
  const events = queued?.length ? queued : CREATE_PROGRESS;
  store.progressQueues.delete(instance);

  let i = 0;
  const tick = () => {
    if (i >= events.length) return;
    sendSseEvent(res, events[i], { id: String(i + 1) });
    i += 1;
    if (i < events.length) setTimeout(tick, 200);
  };
  tick();

  res.on("close", () => clearInterval(keepalive));
}

export function handleContractsStreamSse(res: ServerResponse): void {
  beginSse(res);
  const keepalive = scheduleKeepalive(res);
  sendSseEvent(
    res,
    {
      event: "created",
      contract_id: "00abc999",
      template: "Token:Holding",
      signatories: ["bob::def"],
      observers: [],
      offset: 1300,
      at: Date.now(),
      update_id: "u1300",
    },
    { event: "contracts" },
  );
  res.on("close", () => clearInterval(keepalive));
}

export function handleDarWatchSse(res: ServerResponse, instance: string, darId: string): void {
  beginSse(res);
  const keepalive = scheduleKeepalive(res);
  sendSseEvent(res, {
    instance,
    dar_id: darId || "token-dar",
    event: "watch_started",
    at: Math.floor(Date.now() / 1000),
    detail: "Mock DAR watch active",
  });
  res.on("close", () => clearInterval(keepalive));
}

export function queueCreateProgress(store: MockStore, instance: string): void {
  store.progressQueues.set(instance, structuredClone(CREATE_PROGRESS));
}
