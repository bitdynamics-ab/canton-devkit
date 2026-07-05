#!/usr/bin/env node
// Regenerates the site's doc pages from the repository's docs/*.md.
//
// docs/ is the single source of truth for documentation content. This
// script runs automatically before `astro dev` and `astro build`
// (predev/prebuild), so the site can never drift from the repo docs.
//
// Transform per page:
//   - first `# H1` becomes the Starlight frontmatter title (removed from body)
//   - `description` comes from docs-map.mjs (site-only metadata)
//   - `editUrl` points at the canonical docs/<src> on GitHub, not the
//     generated mirror under website/
//   - links to sibling docs (`other.md`, `other.md#anchor`) are rewritten
//     to relative site URLs; links to other repo files become GitHub URLs
//   - output written to src/content/docs/<dest>.md
//
// Unmapped docs/*.md are published under reference/<name> with a warning
// (except internal/process docs in doNotPublish, which are skipped).
// Generated pages that no longer correspond to a source are pruned
// (hand-authored pages listed in docs-map.mjs are never touched).
//
// The transform helpers are exported for unit testing; the filesystem
// generation only runs when this file is invoked directly as a CLI.

import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { dirname, join, posix, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { docsMap, handAuthored, doNotPublish, repoBlobBase, docsEditBase } from '../docs-map.mjs';

// Build the src→dest lookup, auto-mapping any docs/*.md not in the map so
// new docs always publish. Internal/process docs (doNotPublish) are
// skipped so they never leak onto the grant-facing site.
export function buildEntries(sources, map = docsMap, skip = doNotPublish) {
  const bySrc = new Map(map.map(e => [e.src, e]));
  const warnings = [];
  for (const f of sources) {
    if (skip.includes(f)) continue;
    if (!bySrc.has(f)) {
      const dest = 'reference/' + f.replace(/\.md$/, '');
      warnings.push(`sync-docs: docs/${f} is not in docs-map.mjs — auto-publishing at ${dest}/ (add a map entry to place it properly)`);
      bySrc.set(f, { src: f, dest, description: '' });
    }
  }
  return { bySrc, warnings };
}

export function siteLink(fromDest, target, destBySrc) {
  const [file, anchor] = target.split('#');
  const dest = destBySrc.get(file);
  if (dest === undefined) return null;
  // Starlight page URLs end in a slash, so the browser resolves relative
  // links against the page path itself (e.g. /reference/telemetry/) —
  // compute relative to fromDest, not its parent directory.
  let rel = posix.relative(fromDest, dest);
  if (!rel.startsWith('.')) rel = './' + rel;
  return `${rel}/${anchor ? '#' + anchor : ''}`;
}

export function transformLinks(body, fromDest, destBySrc, blobBase = repoBlobBase) {
  return body.replace(/\]\(([^)\s]+)\)/g, (whole, target) => {
    if (/^(https?:|mailto:|#)/.test(target)) return whole;
    if (/^[\w./-]+\.md(#[\w-]*)?$/.test(target) && !target.includes('/')) {
      const link = siteLink(fromDest, target, destBySrc);
      if (link) return `](${link})`;
    }
    // Relative link out of docs/ (repo file or directory) → GitHub.
    const resolved = posix.normalize(posix.join('docs', target));
    return `](${blobBase}${resolved.replace(/^(\.\.\/)+/, '')})`;
  });
}

export function escapeYaml(s) {
  return `"${s.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

// Turn one docs/*.md source into the generated Starlight page string.
export function renderPage(entry, raw, destBySrc, opts = {}) {
  const blobBase = opts.blobBase ?? repoBlobBase;
  const editBase = opts.editBase ?? docsEditBase;
  const m = raw.match(/^#\s+(.+)\n/);
  const title = m ? m[1].trim() : entry.src.replace(/\.md$/, '');
  const body = m ? raw.slice(m[0].length) : raw;
  const fm = [
    '---',
    `title: ${escapeYaml(title)}`,
    ...(entry.description ? [`description: ${escapeYaml(entry.description)}`] : []),
    `editUrl: ${escapeYaml(editBase + entry.src)}`,
    '---',
    '',
  ].join('\n');
  return fm + transformLinks(body, entry.dest, destBySrc, blobBase).replace(/^\n+/, '');
}

function generate() {
  const here = dirname(fileURLToPath(import.meta.url));
  const websiteDir = join(here, '..');
  const repoRoot = join(websiteDir, '..');
  const docsDir = join(repoRoot, 'docs');
  const outDir = join(websiteDir, 'src', 'content', 'docs');

  const sources = readdirSync(docsDir).filter(f => f.endsWith('.md'));
  const { bySrc, warnings } = buildEntries(sources);
  for (const w of warnings) console.warn(w);
  const destBySrc = new Map([...bySrc.values()].map(e => [e.src, e.dest]));

  const produced = new Set(handAuthored);
  for (const entry of bySrc.values()) {
    const raw = readFileSync(join(docsDir, entry.src), 'utf8');
    const outPath = join(outDir, entry.dest + '.md');
    mkdirSync(dirname(outPath), { recursive: true });
    writeFileSync(outPath, renderPage(entry, raw, destBySrc));
    produced.add(entry.dest + '.md');
  }

  // Prune orphans: generated pages whose source disappeared.
  const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(d => {
    const p = join(dir, d.name);
    return d.isDirectory() ? walk(p) : [p];
  });
  for (const p of walk(outDir)) {
    const rel = relative(outDir, p).split('\\').join('/');
    if (!produced.has(rel)) {
      console.warn(`sync-docs: pruning orphan page ${rel}`);
      rmSync(p);
    }
  }

  console.log(`sync-docs: generated ${bySrc.size} pages from docs/ (${handAuthored.length} hand-authored pages untouched)`);
}

// Only regenerate files when run as a CLI, so tests can import the pure
// helpers without touching the filesystem.
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  generate();
}
