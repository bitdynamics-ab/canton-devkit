// Tiny scale helpers used by every chart primitive in this dir.
//
// We don't import d3-scale — these charts are simple enough that
// 30 lines of linear scaling beats pulling 30 KB of d3. Each helper
// is pure: same inputs → same outputs, no React state, no DOM.

export interface Domain {
  min: number;
  max: number;
}

/**
 * Returns a function that maps an input number into the [0, range]
 * interval, treating min/max as the input domain. Falls back to
 * range/2 (mid) when the domain is degenerate (min == max).
 *
 * Used for X (time-axis) and Y (value-axis) of every chart.
 */
export function linearScale(domain: Domain, range: number): (v: number) => number {
  const span = domain.max - domain.min;
  if (span === 0 || !Number.isFinite(span)) {
    return () => range / 2;
  }
  const scale = range / span;
  return (v) => (v - domain.min) * scale;
}

/** Extent over an iterable of numbers; ignores NaN. */
export function extent(values: Iterable<number>): Domain {
  let min = Infinity;
  let max = -Infinity;
  for (const v of values) {
    if (Number.isNaN(v)) continue;
    if (v < min) min = v;
    if (v > max) max = v;
  }
  if (min === Infinity) return { min: 0, max: 1 };
  if (min === max) {
    // Single value or all-same — pad symmetrically so the line
    // doesn't render as a degenerate horizontal at the chart edge.
    const pad = Math.abs(min) * 0.1 || 1;
    return { min: min - pad, max: max + pad };
  }
  return { min, max };
}

/**
 * Generates ~targetCount evenly-spaced tick values inside a domain,
 * rounded to nice numbers (1/2/5 × 10^k). Used for Y-axis labels.
 */
export function niceTicks(domain: Domain, targetCount = 4): number[] {
  const span = domain.max - domain.min;
  if (span === 0) return [domain.min];
  const roughStep = span / targetCount;
  const mag = Math.pow(10, Math.floor(Math.log10(roughStep)));
  const norm = roughStep / mag;
  let step: number;
  if (norm < 1.5) step = 1 * mag;
  else if (norm < 3) step = 2 * mag;
  else if (norm < 7) step = 5 * mag;
  else step = 10 * mag;
  const start = Math.ceil(domain.min / step) * step;
  const out: number[] = [];
  for (let v = start; v <= domain.max + step * 0.001; v += step) {
    out.push(Number(v.toFixed(10)));
  }
  return out;
}
