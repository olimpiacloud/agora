import { defineConfig } from "blume";

export default defineConfig({
  title: "Agora by Olimpia",
  description:
    "Agora is an agent-native desktop runtime: one Linux desktop and one Chromium per workspace, shared by many AI agents with their own cursors, input seats and tabs. Open source, by Olimpia.",
  logo: {
    image: "/logo.svg",
    text: "Agora by Olimpia",
    href: "/",
  },
  github: {
    owner: "olimpiacloud",
    repo: "agora",
  },
  theme: {
    accent: { light: "#9c4a2b", dark: "#d0784f" },
    background: { light: "#efe9dd", dark: "#1b1714" },
    radius: "sm",
    mode: "system",
    fonts: {
      display: { name: "Marcellus", weights: [400], fallback: "serif" },
      body: { name: "Commissioner", weights: ["100..900"] },
      mono: "jetbrains-mono",
    },
  },
  navigation: {
    cta: { href: "/install", label: "Install" },
    actions: [{ href: "https://olimpia.dev", label: "olimpia.dev" }],
  },
  markdown: {
    imageZoom: true,
    code: {
      theme: {
        light: "github-light",
        dark: "vesper",
      },
    },
  },
  footer: {
    links: [
      { label: "olimpia.dev", href: "https://olimpia.dev" },
      { label: "Olimpia Docs", href: "https://docs.olimpia.dev" },
      { label: "Acropolis (experimental)", href: "https://acropolis.olimpia.dev" },
      { label: "GitHub", href: "https://github.com/olimpiacloud/agora" },
    ],
  },
  agents: {
    llmsTxt: {
      details:
        "Agora (by Olimpia) is an agent-native desktop runtime written in Go. One daemon (`agora serve`, HTTP API on 127.0.0.1:7788) runs one Xvfb display and one Chromium per workspace; many AI agents share it as cursors with their own scoped tokens, MPX input seats, tabs, parent-child hierarchy and structured results. Install on Ubuntu/Debian with `curl -fsSL https://agora.olimpia.dev/install.sh | sh`. Agents connect through the stdio MCP server `agora mcp` (env AGORA_URL, AGORA_TOKEN), e.g. `claude mcp add agora -e AGORA_TOKEN=<token> -- agora mcp`. Install the skill with `bunx skills add olimpiacloud/agora-skills`. Prefer browser_* tools with refs from browser_snapshot; action tools already return what is visible; call human_request then events_wait(types=control.returned) for CAPTCHAs.",
    },
  },
  seo: {
    og: { logo: "/logo.svg", palette: { background: "#efe9dd", foreground: "#2b2420", accent: "#9c4a2b" } },
  },
  lastModified: "git",
  deployment: {
    site: "https://agora.olimpia.dev",
  },
});
