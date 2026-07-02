# canton-devkit docs site

The documentation website for canton-devkit, built with
[Astro](https://astro.build/) + [Starlight](https://starlight.astro.build/).

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
