// Unit tests for the docs→site sync transforms (scripts/sync-docs.mjs).
// Run with: npm test  (node --test)

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  buildEntries,
  siteLink,
  transformLinks,
  escapeYaml,
  renderPage,
} from './sync-docs.mjs';

const MAP = [
  { src: 'getting-started.md', dest: 'getting-started', description: 'Install and verify.' },
  { src: 'faq.md', dest: 'reference/faq', description: 'Common questions.' },
  { src: 'tokens.md', dest: 'guides/tokens', description: '' },
];
const destBySrc = new Map(MAP.map(e => [e.src, e.dest]));

test('buildEntries keeps mapped sources and auto-publishes unmapped ones', () => {
  const { bySrc, warnings } = buildEntries(
    ['getting-started.md', 'faq.md', 'tokens.md', 'newdoc.md'],
    MAP,
    [],
  );
  assert.equal(bySrc.get('newdoc.md').dest, 'reference/newdoc');
  assert.equal(bySrc.get('getting-started.md').dest, 'getting-started');
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /newdoc\.md is not in docs-map/);
});

test('buildEntries skips do-not-publish (internal/process) docs', () => {
  const { bySrc, warnings } = buildEntries(
    ['faq.md', 'changes-from-proposal.md'],
    MAP,
    ['changes-from-proposal.md'],
  );
  assert.ok(!bySrc.has('changes-from-proposal.md'));
  assert.equal(warnings.length, 0);
});

test('siteLink rewrites sibling docs to relative site URLs', () => {
  // From reference/faq to getting-started (a top-level page): two levels up.
  assert.equal(
    siteLink('reference/faq', 'getting-started.md', destBySrc),
    '../../getting-started/',
  );
  // Preserves anchors.
  assert.equal(
    siteLink('reference/faq', 'getting-started.md#4-compatibility-matrix', destBySrc),
    '../../getting-started/#4-compatibility-matrix',
  );
});

test('siteLink returns null for unknown targets', () => {
  assert.equal(siteLink('reference/faq', 'nope.md', destBySrc), null);
});

test('transformLinks rewrites sibling .md links and keeps external/anchor links', () => {
  const body = [
    'See [FAQ](faq.md) and [matrix](getting-started.md#4-compatibility-matrix).',
    'External [Canton](https://canton.network/) stays.',
    'In-page [top](#intro) stays.',
  ].join('\n');
  const out = transformLinks(body, 'guides/tokens', destBySrc);
  assert.match(out, /\[FAQ\]\(\.\.\/\.\.\/reference\/faq\/\)/);
  assert.match(out, /\[matrix\]\(\.\.\/\.\.\/getting-started\/#4-compatibility-matrix\)/);
  assert.match(out, /\[Canton\]\(https:\/\/canton\.network\/\)/);
  assert.match(out, /\[top\]\(#intro\)/);
});

test('transformLinks sends out-of-docs relative links to GitHub blob base', () => {
  const out = transformLinks(
    'See [proposal](../README.md) for details.',
    'getting-started',
    destBySrc,
    'https://github.com/o/r/blob/main/',
  );
  assert.match(out, /\[proposal\]\(https:\/\/github\.com\/o\/r\/blob\/main\/README\.md\)/);
});

test('escapeYaml quotes and escapes double quotes and backslashes', () => {
  assert.equal(escapeYaml('plain'), '"plain"');
  assert.equal(escapeYaml('has "quote"'), '"has \\"quote\\""');
  assert.equal(escapeYaml('back\\slash'), '"back\\\\slash"');
});

test('renderPage extracts H1 into title, emits editUrl, drops H1 from body', () => {
  const raw = '# Frequently Asked Questions\n\nSome body text.\n';
  const out = renderPage(MAP[1], raw, destBySrc, {
    editBase: 'https://github.com/o/r/edit/main/docs/',
  });
  assert.match(out, /^---\n/);
  assert.match(out, /title: "Frequently Asked Questions"/);
  assert.match(out, /description: "Common questions\."/);
  assert.match(out, /editUrl: "https:\/\/github\.com\/o\/r\/edit\/main\/docs\/faq\.md"/);
  assert.ok(!out.includes('# Frequently Asked Questions'));
  assert.match(out, /Some body text\./);
});

test('renderPage omits description when empty but always emits editUrl', () => {
  const raw = '# Tokens\n\nBody.\n';
  const out = renderPage(MAP[2], raw, destBySrc, {
    editBase: 'https://github.com/o/r/edit/main/docs/',
  });
  assert.ok(!out.includes('description:'));
  assert.match(out, /editUrl: "https:\/\/github\.com\/o\/r\/edit\/main\/docs\/tokens\.md"/);
});

test('renderPage falls back to filename-derived title when no H1', () => {
  const raw = 'No heading here.\n';
  const out = renderPage(MAP[2], raw, destBySrc, {
    editBase: 'https://github.com/o/r/edit/main/docs/',
  });
  assert.match(out, /title: "tokens"/);
});
