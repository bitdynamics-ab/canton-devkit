const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

export function formatUptime(createdAt: string | undefined, now: number): string {
  if (!createdAt) return "";
  const t = Date.parse(createdAt);
  if (Number.isNaN(t)) return "";
  const d = now - t;
  if (d < 0) return "";
  if (d < HOUR_MS) return `${Math.floor(d / MINUTE_MS)}m`;
  if (d < DAY_MS) {
    const h = Math.floor(d / HOUR_MS);
    const m = Math.floor(d / MINUTE_MS) - h * 60;
    return `${h}h ${m}m`;
  }
  return `${Math.floor(d / DAY_MS)}d`;
}
