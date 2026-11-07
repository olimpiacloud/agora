import { defineConfig } from "cf/config";

export default defineConfig({
  worker: {
    name: "agora-docs",
    compatibilityDate: "2026-11-04",
    assets: {
      htmlHandling: "drop-trailing-slash",
      notFoundHandling: "404-page",
    },
    domains: ["agora.olimpia.dev"],
    observability: { enabled: true },
  },
});
