import { defineConfig } from "blume";
import { family } from "./family";

export default defineConfig({
  ...family("agora"),
  title: "Agora by Olimpia",
  description:
    "Agora is an agent-native desktop runtime: one Linux desktop and one Chromium per workspace, shared by many AI agents with their own cursors, input seats and tabs. Open source, by Olimpia.",
  logo: {
    image: "/logo.svg",
    text: "Agora",
    href: "/",
  },
  github: {
    owner: "olimpiacloud",
    repo: "agora",
  },
  agents: {
    llmsTxt: {
      details:
        "Agora (by Olimpia) is an agent-native desktop runtime written in Go. One daemon (`agora serve`, HTTP API on 127.0.0.1:7788) runs one Xvfb display and one Chromium per workspace; many AI agents share it as cursors with their own scoped tokens, MPX input seats, tabs, parent-child hierarchy and structured results. Install on Ubuntu/Debian with `curl -fsSL https://agora.olimpia.dev/install.sh | sudo sh`. Agents connect through the stdio MCP server `agora mcp` (env AGORA_URL, AGORA_TOKEN), e.g. `claude mcp add agora -s user -- agora mcp` or `codex mcp add agora -- agora mcp`. Install the skill with `bunx skills add olimpiacloud/agora-skills`. Prefer browser_* tools with refs from browser_snapshot; action tools already return what is visible; call human_request then events_wait(types=control.returned) for CAPTCHAs.",
    },
  },
});
