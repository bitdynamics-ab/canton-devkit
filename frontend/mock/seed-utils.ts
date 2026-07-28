export const SCHEMA_VERSION = 1;

export interface SeedOptions {
  baseUrl: string;
  instance: string;
  role: string;
  asName?: string;
  dryRun: boolean;
}

export interface SeedTarget {
  file: string;
  path: string;
  optional?: boolean;
}

export function buildSeedTargets(opts: SeedOptions): SeedTarget[] {
  const { instance, role } = opts;
  const inst = encodeURIComponent(instance);
  const q = (params: Record<string, string>) => {
    const sp = new URLSearchParams(params);
    return `?${sp.toString()}`;
  };

  return [
    { file: "version.json", path: "/api/version" },
    { file: "instances.json", path: "/api/instances" },
    { file: `instance-${opts.asName ?? instance}.json`, path: `/api/instances/${inst}` },
    { file: "containers.json", path: `/api/instances/${inst}/containers`, optional: true },
    {
      file: "contracts.json",
      path: `/api/instances/${inst}/contracts${q({ role, limit: "50" })}`,
      optional: true,
    },
    {
      file: "transactions.json",
      path: `/api/instances/${inst}/transactions${q({ role, limit: "50" })}`,
      optional: true,
    },
    { file: "dar.json", path: `/api/instances/${inst}/dar${q({ role })}`, optional: true },
    {
      file: "metrics-summary.json",
      path: `/api/instances/${inst}/metrics/summary`,
      optional: true,
    },
    {
      file: "metrics-range.json",
      path: `/api/instances/${inst}/metrics/range${q({
        query: "canton_transactions_total",
        window: "1h",
        step: "60",
      })}`,
      optional: true,
    },
    {
      file: "tokens.json",
      path: `/api/tokens${q({ instance, role })}`,
      optional: true,
    },
    {
      file: "tokens-matrix.json",
      path: `/api/tokens/matrix${q({ instance, role })}`,
      optional: true,
    },
    {
      file: "parties.json",
      path: `/api/parties${q({ instance, role })}`,
      optional: true,
    },
    { file: "doctor.json", path: "/api/doctor" },
    { file: "preflight.json", path: "/api/preflight" },
    { file: "splice-versions.json", path: "/api/splice/versions" },
    { file: "skills.json", path: "/api/skills", optional: true },
  ];
}

export function buildDerivedTargets(
  opts: SeedOptions,
  contracts: { contracts?: Array<{ contract_id?: string }> },
  transactions: { transactions?: Array<{ update_id?: string }> },
  dars: { dars?: Array<{ id?: string }> },
  tokens: { instruments?: Array<{ symbol?: string }> },
): SeedTarget[] {
  const { instance, role } = opts;
  const inst = encodeURIComponent(instance);
  const q = (params: Record<string, string>) => `?${new URLSearchParams(params).toString()}`;
  const out: SeedTarget[] = [];

  const firstContract = contracts.contracts?.[0]?.contract_id;
  if (firstContract) {
    out.push({
      file: "contract-detail.json",
      path: `/api/instances/${inst}/contracts/${encodeURIComponent(firstContract)}${q({ role })}`,
      optional: true,
    });
  }

  const firstTx = transactions.transactions?.[0]?.update_id;
  if (firstTx) {
    out.push({
      file: "tx-replay.json",
      path: `/api/instances/${inst}/transactions/${encodeURIComponent(firstTx)}/replay${q({ role })}`,
      optional: true,
    });
  }

  const firstDar = dars.dars?.[0]?.id;
  if (firstDar) {
    const darEnc = encodeURIComponent(firstDar);
    out.push({
      file: "dar-inspect.json",
      path: `/api/instances/${inst}/dar/${darEnc}/inspect${q({ role })}`,
      optional: true,
    });
    out.push({
      file: "dar-vetting.json",
      path: `/api/instances/${inst}/dar/${darEnc}/vetting`,
      optional: true,
    });
  }

  const firstSymbol = tokens.instruments?.[0]?.symbol;
  if (firstSymbol) {
    const sym = encodeURIComponent(firstSymbol);
    const base = q({ instance, role });
    out.push(
      { file: "token-summary.json", path: `/api/tokens/${sym}/summary${base}`, optional: true },
      { file: "token-activity.json", path: `/api/tokens/${sym}/activity${base}`, optional: true },
      { file: "token-holdings.json", path: `/api/tokens/${sym}/holdings${base}`, optional: true },
    );
  }

  return out;
}

/** Rewrite instance name fields so pulled data can be served as a stable mock name. */
export function rewriteInstanceName(data: unknown, from: string, to: string): unknown {
  if (from === to) return data;
  const walk = (node: unknown): unknown => {
    if (Array.isArray(node)) return node.map(walk);
    if (node && typeof node === "object") {
      const out: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(node)) {
        if (k === "name" && v === from) out[k] = to;
        else if (k === "instance" && v === from) out[k] = to;
        else if (k === "compose_project" && typeof v === "string" && v.includes(from)) {
          out[k] = v.replaceAll(from, to);
        } else if (k === "container_prefix" && typeof v === "string" && v.startsWith(from)) {
          out[k] = v.replace(from, to);
        } else out[k] = walk(v);
      }
      return out;
    }
    return node;
  };
  return walk(data);
}

export function validateSchema(body: unknown): boolean {
  return (
    !!body &&
    typeof body === "object" &&
    "schema_version" in body &&
    (body as { schema_version: number }).schema_version === SCHEMA_VERSION
  );
}

export function parseSeedArgs(argv: string[]): SeedOptions | { error: string } {
  let baseUrl = "http://127.0.0.1:7777";
  let instance = "";
  let role = "app-user";
  let asName: string | undefined;
  let dryRun = false;

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--base-url" && argv[i + 1]) baseUrl = argv[++i];
    else if (arg === "--instance" && argv[i + 1]) instance = argv[++i];
    else if (arg === "--role" && argv[i + 1]) role = argv[++i];
    else if (arg === "--as" && argv[i + 1]) asName = argv[++i];
    else if (arg === "--dry-run") dryRun = true;
    else if (arg === "--help" || arg === "-h") {
      return {
        error: [
          "Usage: npm run mock:seed -- --instance <name> [options]",
          "  --base-url URL   Backend base URL (default http://127.0.0.1:7777)",
          "  --role ROLE      Role for scoped endpoints (default app-user)",
          "  --as NAME        Rewrite instance name in output (e.g. demo)",
          "  --dry-run        Print targets without writing files",
        ].join("\n"),
      };
    }
  }

  if (!instance) return { error: "--instance is required" };
  return { baseUrl: baseUrl.replace(/\/$/, ""), instance, role, asName, dryRun };
}
