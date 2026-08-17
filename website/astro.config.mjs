// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://bitdynamics-ab.github.io',
	base: '/canton-devkit',
	integrations: [
		starlight({
			title: 'Canton DevKit',
			description:
				'One command to a full Canton LocalNet — spin up, inspect, and tear down a complete Canton developer stack.',
			customCss: [
				// Self-hosted fonts (see src/fonts/ for licenses) + typography.
				'./src/styles/fonts.css',
				'./src/styles/custom.css',
			],
			expressiveCode: {
				// Design system: code blocks stay dark ink in BOTH themes
				// (one dark syntax theme), 4px radius, JetBrains Mono.
				themes: ['github-dark'],
				styleOverrides: {
					codeFontFamily: "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace",
					codeFontSize: '0.8125rem',
					codeLineHeight: '1.6',
					uiFontFamily: "'Archivo', -apple-system, 'Segoe UI', sans-serif",
					borderRadius: '4px',
					codeBackground: '#0B0F1A',
					frames: { editorBackground: '#0B0F1A', terminalBackground: '#0B0F1A', terminalTitlebarBackground: '#10151F', editorTabBarBackground: '#10151F', shadowColor: 'transparent' },
				},
			},
			social: [
				{
					icon: 'github',
					label: 'GitHub',
					href: 'https://github.com/bitdynamics-ab/canton-devkit',
				},
			],
			editLink: {
				baseUrl: 'https://github.com/bitdynamics-ab/canton-devkit/edit/main/website/',
			},
			sidebar: [
				{
					label: 'Getting Started',
					items: [{ slug: 'getting-started' }],
				},
				{
					label: 'Case studies',
					items: [{ slug: 'case-studies/cip0112-token', label: 'Issue a CIP-0112 token' }],
				},
				{
					label: 'Guides',
					items: [
						{ slug: 'guides/localnet-lifecycle' },
						{ slug: 'guides/explorer' },
						{ slug: 'guides/observability' },
						{ slug: 'guides/dashboard-customization' },
						{ slug: 'guides/tokens' },
						{ slug: 'guides/homebrew' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ slug: 'reference/versions' },
						{ slug: 'reference/packaging' },
						{ slug: 'reference/telemetry' },
						{ slug: 'reference/limitations' },
						{ slug: 'reference/faq' },
						{ slug: 'reference/troubleshooting' },
						{ slug: 'reference/e2e-testing' },
					],
				},
				{
					label: 'Operations',
					items: [{ slug: 'operations/telemetry-collector' }],
				},
			],
		}),
	],
});
