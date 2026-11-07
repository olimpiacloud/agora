# Agora docs

Source of [agora.olimpia.dev](https://agora.olimpia.dev), built with [Blume](https://useblume.dev).

## Develop

```bash
bun install
bun run dev
```

The site runs on http://localhost:4321. Content is MDX in `docs/`; each `(group)` folder is a sidebar section ordered by its `meta.ts`. Branding (Olimpia palette and fonts) lives in `blume.config.ts` and `theme.css`.

## Build

```bash
bun run build
bunx blume validate
```

`bun run build` copies `../scripts/install.sh` and `../scripts/uninstall.sh` into `public/` (when they exist) so the site serves them at `/install.sh` and `/uninstall.sh`, then writes the static site to `dist/`. The build also emits `llms.txt`, `llms-full.txt`, `skill.md` and a Markdown version of every page (`/<page>.md`).

## Deploy

The site is a static-assets Cloudflare Worker named `agora-docs` with the custom domain `agora.olimpia.dev` (zone `olimpia.dev`). Configuration is in `cloudflare.config.ts` (Worker, domain, HTML handling) and `wrangler.config.ts` (assets directory `dist`).

```bash
bun run deploy
```

This builds and runs `cf deploy`. You need the `cf` CLI logged in to the Olimpia Cloudflare account (`cf auth login`), or `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` in the environment. `cloudflare.config.ts` needs Node.js 22.18 or later; `cf` does not load it under Bun.

Check the result:

```bash
curl -sI https://agora.olimpia.dev/
curl -fsSL https://agora.olimpia.dev/install.sh | head
```
