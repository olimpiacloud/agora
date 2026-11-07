# Agora

Connects Paseo to an Agora daemon running on the same machine, so agents get a real Linux desktop with Chromium and you can watch and steer it from Paseo.

Each Claude or Codex agent receives the Agora MCP server with its own Agora cursor. Agents in the same Paseo workspace share one Agora workspace (one desktop and one browser profile), each with its own windows and input seat. Add the label `agora/enabled=false` to an agent to skip it.

The Agora panel shows the desktop live. Frames are JPEG images sent only when the screen changes, through Paseo's existing connection, so it works on mobile and needs no open ports. Tap to click, long press to right click, type into the focused window, send keys, scroll, and take or return control when an agent asks for a human (for example on a CAPTCHA or login).

The panel has two views: the full desktop, and an agent wall with one tile per agent showing that agent's own window live, labeled with its name and status. Every agent's cursor is drawn on top as a colored arrow with its name.

When an agent takes a screenshot or opens a page with Agora tools, the chat shows a live preview card of the desktop with all cursors and the active agents. Only the most recent card of each workspace streams; older cards keep their last frame, and a card falls back to the stored screenshot when the stream is unavailable. Viewers of the same workspace share one stream from Agora, which closes when nobody is watching.

Setup: install and run Agora (`agora serve`, listening on 127.0.0.1:7788) on the daemon host. The plugin reads the Agora admin token from `/var/lib/agora/admin.token` (override with `AGORA_HOME` or `AGORA_URL`) and stores per-agent cursor tokens in `~/.agora-paseo/state.json` with mode 0600.
