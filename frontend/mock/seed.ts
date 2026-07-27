#!/usr/bin/env node
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  buildDerivedTargets,
  buildSeedTargets,
  parseSeedArgs,
  rewriteInstanceName,
  validateSchema,
  type SeedOptions,
  type SeedTarget,
} from "./seed-utils.ts";
import { FIXTURES_DIR } from "./store.ts";

const mockDir = dirname(fileURLToPath(import.meta.url));
const fixturesDir = join(mockDir, "fixtures");

interface FetchResult {
  ok: boolean;
  status: number;
  body: unknown;
}

async function fetchJson(baseUrl: string, path: string): Promise<FetchResult> {
  const resp = await fetch(`${baseUrl}${path}`, {
    headers: { Accept: "application/json" },
  });
  const text = await resp.text();
  let body: unknown;
  try {
    body = text ? JSON.parse(text) : undefined;
  } catch {
    body = undefined;
  }
  return { ok: resp.ok, status: resp.status, body };
}

function applyRename(body: unknown, opts: SeedOptions): unknown {
  if (!opts.asName || opts.asName === opts.instance) return body;
  return rewriteInstanceName(body, opts.instance, opts.asName);
}

async function writeTarget(
  opts: SeedOptions,
  target: SeedTarget,
  fetched: Map<string, unknown>,
): Promise<boolean> {
  const url = `${opts.baseUrl}${target.path}`;
  if (opts.dryRun) {
    // eslint-disable-next-line no-console
    console.log(`[dry-run] ${target.file} <= ${url}`);
    return true;
  }

  const result = await fetchJson(opts.baseUrl, target.path);
  if (!result.ok) {
    const msg = `WARN skip ${target.file}: HTTP ${result.status}`;
    // eslint-disable-next-line no-console
    console.warn(msg);
    return false;
  }

  if (!validateSchema(result.body) && target.file !== "metrics-range.json") {
    // eslint-disable-next-line no-console
    console.warn(`WARN skip ${target.file}: schema_version mismatch`);
    return false;
  }

  const data = applyRename(result.body, opts);
  fetched.set(target.file, data);
  const outPath = join(fixturesDir, target.file);
  writeFileSync(outPath, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  // eslint-disable-next-line no-console
  console.log(`wrote ${target.file}`);
  return true;
}

async function main(): Promise<number> {
  const parsed = parseSeedArgs(process.argv.slice(2));
  if ("error" in parsed) {
    // eslint-disable-next-line no-console
    console.error(parsed.error);
    return 1;
  }
  const opts = parsed;

  // Verify backend is reachable.
  const version = await fetchJson(opts.baseUrl, "/api/version");
  if (!version.ok) {
    // eslint-disable-next-line no-console
    console.error(`Backend unreachable at ${opts.baseUrl} (HTTP ${version.status})`);
    return 1;
  }

  const targets = buildSeedTargets(opts);
  const fetched = new Map<string, unknown>();
  let instanceDetailOk = false;

  for (const target of targets) {
    const ok = await writeTarget(opts, target, fetched);
    if (target.path.startsWith(`/api/instances/${encodeURIComponent(opts.instance)}`) &&
        !target.path.includes("/containers") &&
        !target.path.includes("/contracts") &&
        !target.path.includes("/transactions") &&
        !target.path.includes("/dar") &&
        !target.path.includes("/metrics") &&
        target.file.startsWith("instance-")) {
      instanceDetailOk = ok;
    }
  }

  if (!instanceDetailOk && !opts.dryRun) {
    // eslint-disable-next-line no-console
    console.error(`Instance "${opts.instance}" not found on backend`);
    return 1;
  }

  const contracts = (fetched.get("contracts.json") ?? {}) as {
    contracts?: Array<{ contract_id?: string }>;
  };
  const transactions = (fetched.get("transactions.json") ?? {}) as {
    transactions?: Array<{ update_id?: string }>;
  };
  const dars = (fetched.get("dar.json") ?? {}) as { dars?: Array<{ id?: string }> };
  const tokens = (fetched.get("tokens.json") ?? {}) as {
    instruments?: Array<{ symbol?: string }>;
  };

  const derived = buildDerivedTargets(opts, contracts, transactions, dars, tokens);
  for (const target of derived) {
    await writeTarget(opts, target, fetched);
  }

  if (opts.dryRun) {
    // eslint-disable-next-line no-console
    console.log(`Fixtures dir: ${FIXTURES_DIR}`);
  }

  return 0;
}

main().then((code) => process.exit(code));
