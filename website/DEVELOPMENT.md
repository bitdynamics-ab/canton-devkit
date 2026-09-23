# Documentation site development

Maintainer notes for the canton-devkit documentation website, built with
[Astro](https://astro.build/) + [Starlight](https://starlight.astro.build/).

The published site is
[https://bitdynamics-ab.github.io/canton-devkit/](https://bitdynamics-ab.github.io/canton-devkit/).
Pages live in `src/content/docs/` as Markdown/MDX; the sidebar is
configured in `astro.config.mjs`.

## Run locally

Requires Node.js 22+ (see `.nvmrc`).

```sh
cd website
npm install
npm run dev        # dev server at http://localhost:4321/canton-devkit
```

## Build

```sh
npm run build      # static output in dist/
npm run preview    # serve the production build locally
```

## Deploy

Pushes to `main` that touch `website/**` or `docs/**` trigger
`.github/workflows/docs.yml`, which builds the site and deploys it to
GitHub Pages.

## Sitemap / Google Search Console

Starlight emits a sitemap when `site` and `base` are set in
`astro.config.mjs`. After deploy, the public URLs are:

- Index: https://bitdynamics-ab.github.io/canton-devkit/sitemap-index.xml
- Pages: https://bitdynamics-ab.github.io/canton-devkit/sitemap-0.xml
- robots.txt: https://bitdynamics-ab.github.io/canton-devkit/robots.txt

In Search Console, use the URL-prefix property
`https://bitdynamics-ab.github.io/canton-devkit/` and submit the full
index URL above (not `/sitemap-index.xml` on the github.io host root —
that 404s for this project Pages site).
