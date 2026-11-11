# Changelog

All notable changes to Agora. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- Idle reaping: tabs unused for `--tab-idle` (15 minutes by default) are closed unless their cursor is waiting for a human or the viewer was used in that time, Chromium stops after two minutes with no tabs, and apps of cursors that already ended are stopped. Events `scheduler.tab_closed`, `scheduler.browser_stopped` and `scheduler.app_stopped`.

### Changed

- Snapshots and observations list elements without their viewport boxes, so the diff after an action skips unchanged elements even after a scroll. `browser_snapshot` takes `boxes: true` to get them back.
- `desktop_screenshot` defaults to 1024 px wide and JPEG quality 60. `desktop_click`, `desktop_move`, `desktop_drag` and `desktop_scroll` take positions in the last screenshot and the MCP server maps them to the desktop; `browser_click`, `browser_hover`, `browser_drag` and `browser_scroll` do the same with the last `browser_screenshot` of the tab.
- `cursor_list`, `cursor_wait` and `cursor_spawn` return a short view of each cursor (`cursor_list` with a 300-character result preview); `cursor_get` returns the full result and progress. MCP results are compact JSON.
- Lean Claude Code subagents default to the `sonnet` model when `launch.model` is not set.
- Codex subagents get the lean tool profile and the lean prompt, and their token usage is recorded in `cursor.usage`.
- The Paseo plugin deletes an agent's cursor when Paseo closes or archives the agent, and gives Codex agents the lean tool descriptions.
- `agora serve` suspends workspaces after 30 minutes without activity or viewers by default (`--suspend-after`, `0` keeps them running). It also applies to existing workspaces without their own `suspend_after`.
- Agent cursors in the web viewer and the Paseo plugin glide between reported positions with spline interpolation ([perfect-cursors](https://github.com/steveruizok/perfect-cursors)) instead of a linear CSS transition or a jump.

### Security

- Cursor responses no longer include `token_hash` or `launch.env`, which sibling cursors in the same workspace could read.
- The cursor token no longer appears in the agent's command line: Claude Code reads it from a `0600` MCP config file that is removed when the agent exits, and Codex from its environment.

### Fixed

- The prompt of `full` profile subagents named a tool that does not exist (`wait_events`).

## [0.1.0] - 2026-11-06

First public release.

### Runtime

- One Go daemon that gives AI agents a real Linux desktop: one Xvfb and one Chromium per workspace, shared by every agent in it. Agora is its own minimal EWMH window manager.
- Workspaces with a lifecycle (`created → active → idle → frozen → suspended → destroyed`), copy-on-write profile forks, checkpoints, TTLs and budgets (memory, CPU, cursors, tabs). An unused workspace costs nothing.
- Cursors: agents with their own token, permissions that can only narrow when delegated, depth and child limits, messaging, progress reports, `wait`, pause, cascading cancel, and automatic release of seats, locks and tabs.
- Truly concurrent input: each cursor gets its own MPX pointer and keyboard on demand; in Chromium, input goes per tab over CDP. Surface locks and human takeover with priority.
- Hybrid control: a compact semantic snapshot with refs (`[e3] button "Sign in"`), clicks by ref or coordinates, fill, select, upload, downloads, dialogs, HTML5 drag, eval, device emulation and an embedded UI audit; pixel control for any X11 app.
- CAPTCHA and login handoff: challenges are detected, the cursor moves to `needs_human`, a person takes control from the viewer and hands it back, and the agent continues on the same tab and session.
- Freeze (cgroup freezer or SIGSTOP), suspend (checkpoint and graceful browser close) and resume with parallel tab restore to their owners.
- Shared workspace board with claims, so parallel subagents do not duplicate work.
- Optional sandboxed workspaces (`config.sandbox: true`) for untrusted workloads: read-only system, private home, no view of other workspaces.
- Browsers and apps run as the unprivileged `agora` user with Chrome's sandbox enabled; Chromium is driven over `--remote-debugging-pipe` with no TCP port. The daemon refuses to listen outside loopback.

### Streaming

- VA-API H.264 encoding on the GPU when a device is available (libva loaded at runtime), with automatic fallback to x264.
- Damage-driven H.264 (x264) over WebRTC (Pion) or WebSocket + WebCodecs, encoded once and fanned out to every viewer; damage-driven JPEG for light clients, shared per display and croppable to a cursor's window.
- Adaptive frame rate: 30 fps while a human interacts, 15 fps while watching, 8 fps in observation mode. Nothing is captured or encoded without viewers.

### Integrations

- HTTP API and `agora api` client.
- MCP server (`agora mcp`) with 50 tools, permission-based tool profiles and incremental observations after each action.
- Agent launcher for Claude Code, Codex or any command, with a lean Claude Code profile (initial context per subagent down from 66.7k to 7.5k tokens) and per-cursor usage (tokens, turns, cost, duration).
- Git worktrees with a port range per subagent.
- Embedded React dashboard and viewer (`agora open` prints a viewer link with a short-lived token).
- Paseo plugin: live desktop panel, live preview cards and an agent wall, screenshot cards in the chat, and an Agora cursor and MCP server for every Paseo agent.
- `agora version` prints the version, commit and build date injected at build time.

### Tooling

- `agora-bench`, a reproducible benchmark against Xvfb + x11vnc + noVNC + Chrome, a streaming latency suite, a soak test with leak detection, and an agent token benchmark.
- `scripts/install.sh` and `scripts/uninstall.sh` for Ubuntu and Debian (amd64 and arm64).
- CI (vet, gofmt, unit and e2e tests, dashboard build, plugin typecheck) and tagged releases with checksummed binaries.

[Unreleased]: https://github.com/olimpiacloud/agora/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/olimpiacloud/agora/releases/tag/v0.1.0
