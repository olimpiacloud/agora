# Carbon

Connects Paseo to a Carbon daemon running on the same machine, so agents get a real Linux desktop with Chromium and you can watch and steer it from Paseo.

Each Claude or Codex agent receives the Carbon MCP server with its own Carbon cursor. Agents in the same Paseo workspace share one Carbon workspace (one desktop and one browser profile), each with its own windows and input seat. Add the label `carbon/enabled=false` to an agent to skip it.

The Carbon panel shows the desktop live. Frames are JPEG images sent only when the screen changes, through Paseo's existing connection, so it works on mobile and needs no open ports. Tap to click, long press to right click, type into the focused window, send keys, scroll, and take or return control when an agent asks for a human (for example on a CAPTCHA or login).

Screenshots taken with Carbon tools appear as cards in the agent chat with a link to the live view.

Setup: install and run Carbon (`carbon serve`, listening on 127.0.0.1:7788) on the daemon host. The plugin reads the Carbon admin token from `/var/lib/carbon/admin.token` (override with `CARBON_HOME` or `CARBON_URL`) and stores per-agent cursor tokens in `~/.carbon-paseo/state.json` with mode 0600.
