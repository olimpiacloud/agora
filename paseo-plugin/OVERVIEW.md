# Agora

Connects Paseo to an Agora daemon running on the same machine, so agents get a real Linux desktop with Chromium and you can watch and steer it from Paseo.

Each Claude or Codex agent receives the Agora MCP server with its own Agora cursor. Agents in the same Paseo workspace share one Agora workspace (one desktop and one browser profile), each with its own windows and input seat. Add the label `agora/enabled=false` to an agent to skip it.

The Agora panel shows the desktop live. Frames are JPEG images sent only when the screen changes, through Paseo's existing connection, so it works on mobile and needs no open ports. Tap to click, long press to right click, type into the focused window, send keys, scroll, and take or return control when an agent asks for a human (for example on a CAPTCHA or login).

Screenshots taken with Agora tools appear as cards in the agent chat with a link to the live view.

Setup: install and run Agora (`agora serve`, listening on 127.0.0.1:7788) on the daemon host. The plugin reads the Agora admin token from `/var/lib/agora/admin.token` (override with `AGORA_HOME` or `AGORA_URL`) and stores per-agent cursor tokens in `~/.agora-paseo/state.json` with mode 0600.
