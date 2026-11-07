# Changelog

All notable changes to Agora. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - Unreleased

First public release.

### Runtime

- One Go daemon that gives AI agents a real Linux desktop: one Xvfb and one Chromium per workspace, shared by every agent in it. Agora is its own minimal EWMH window manager.
- Workspaces with a lifecycle (`created → active → idle → frozen → suspended → destroyed`), copy-on-write profile forks, checkpoints, TTLs and budgets (memory, CPU, cursors, tabs). An unused workspace costs nothing.
- Cursors: agents with their own token, permissions that can only narrow when delegated, depth and child limits, messaging, progress reports, `wait`, pause, cascading cancel, and automatic release of seats, locks and tabs.
- Truly concurrent input: each cursor gets its own MPX pointer and keyboard on demand; in Chromium, input goes per tab over CDP. Surface locks and human takeover with priority.
- Hybrid control: a compact semantic snapshot with refs (`[e3] button "Sign in"`), clicks by ref or coordinates, fill, select, upload, downloads, dialogs, HTML5 drag, eval, device emulation and an embedded UI audit; pixel control for any X11 app.
- CAPTCHA and login handoff: challenges are detected, the cursor moves to `needs_human`, a person takes control from the viewer and hands it back, and the agent continues on the same tab and session.
- Freeze (cgroup freezer or SIGSTOP), suspend (checkpoint and graceful browser close) and resume with parallel tab restore to their owners.
- Browsers and apps run as the unprivileged `agora` user with Chrome's sandbox enabled; Chromium is driven over `--remote-debugging-pipe` with no TCP port. The daemon refuses to listen outside loopback.

### Streaming

- Damage-driven H.264 (x264) over WebRTC (Pion) or WebSocket + WebCodecs, encoded once and fanned out to every viewer; damage-driven JPEG for light clients.
- Adaptive frame rate: 30 fps while a human interacts, 15 fps while watching, 8 fps in observation mode. Nothing is captured or encoded without viewers.

### Integrations

- HTTP API and `agora api` client.
- MCP server (`agora mcp`) with 50 tools, permission-based tool profiles and incremental observations after each action.
- Agent launcher for Claude Code, Codex or any command, with a lean Claude Code profile (initial context per subagent down from 66.7k to 7.5k tokens) and per-cursor usage (tokens, turns, cost, duration).
- Git worktrees with a port range per subagent.
- Embedded React dashboard and viewer (`agora open` prints a viewer link with a short-lived token).
- Paseo plugin: live desktop panel, screenshot cards in the chat, and an Agora cursor and MCP server for every Paseo agent.
- `agora version` prints the version, commit and build date injected at build time.

### Tooling

- `agora-bench`, a reproducible benchmark against Xvfb + x11vnc + noVNC + Chrome, and an agent token benchmark.
- `scripts/install.sh` and `scripts/uninstall.sh` for Ubuntu and Debian (amd64 and arm64).
- CI (vet, gofmt, unit and e2e tests, dashboard build, plugin typecheck) and tagged releases with checksummed binaries.

[Unreleased]: https://github.com/olimpiacloud/agora/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/olimpiacloud/agora/releases/tag/v0.1.0
