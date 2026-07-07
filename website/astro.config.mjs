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
				// Code-block typography is resolved at build time by
				// Expressive Code — plain CSS overrides don't reach the
				// inner <pre>, so set it here (this fixes the browser
				// falling back to its default monospace inside frames).
				styleOverrides: {
					codeFontFamily: "'Paper Mono', ui-monospace, 'SF Mono', Menlo, monospace",
					codeFontSize: '0.875rem',
					codeLineHeight: '1.7',
					uiFontFamily: "'Switzer', ui-sans-serif, system-ui, sans-serif",
					borderRadius: '0.5rem',
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
