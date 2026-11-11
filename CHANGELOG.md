# Changelog

All notable changes to Agora. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- Idle reaping: tabs unused for `--tab-idle` (15 minutes by default) are closed unless their cursor is waiting for a human or the viewer was used in that time, Chromium stops after two minutes with no tabs, and apps of cursors that already ended are stopped. Events `scheduler.tab_closed`, `scheduler.browser_stopped` and `scheduler.app_stopped`.
- Stream endpoints take `wake=0`: on a suspended workspace they answer `{"type":"bye","reason":"suspended"}` (WebRTC: `409`) instead of resuming it. The web viewer and the Paseo panel use it on reconnects and show a Resume button.
- Pending cursors without a launch and without a Paseo agent are cancelled after 24 hours, and the Paseo plugin deletes cursors of Paseo agents that no longer exist, are closed or are archived.
- `agora serve --cursor-retention` (7 days by default, `0` keeps them): finished cursors with no live descendants are deleted after that time.
- `make check`: gofmt, vet, the unit tests and both TypeScript typechecks; tests that need Xvfb skip when it is missing.

### Changed

- Snapshots and observations list elements without their viewport boxes, so the diff after an action skips unchanged elements even after a scroll. `browser_snapshot` takes `boxes: true` to get them back.
- `desktop_screenshot` defaults to 1024 px wide and JPEG quality 60. `desktop_click`, `desktop_move`, `desktop_drag` and `desktop_scroll` take positions in the last screenshot and the MCP server maps them to the desktop; `browser_click`, `browser_hover`, `browser_drag` and `browser_scroll` do the same with the last `browser_screenshot` of the tab.
- `cursor_list`, `cursor_wait` and `cursor_spawn` return a short view of each cursor (`cursor_list` with a 300-character result preview); `cursor_get` returns the full result and progress. MCP results are compact JSON.
- Lean Claude Code subagents default to the `sonnet` model when `launch.model` is not set.
- Codex subagents get the lean tool profile and the lean prompt, and their token usage is recorded in `cursor.usage`.
- The Paseo plugin deletes an agent's cursor when Paseo closes or archives the agent, and gives Codex agents the lean tool descriptions.
- `agora serve` suspends workspaces after 30 minutes without activity by default; a viewer that only watches no longer keeps the workspace running (`--suspend-after`, `0` keeps them running). It also applies to existing workspaces without their own `suspend_after`.
- Agent cursors in the web viewer and the Paseo plugin glide between reported positions with spline interpolation ([perfect-cursors](https://github.com/steveruizok/perfect-cursors)) instead of a linear CSS transition or a jump.
- State files are written by a background writer that coalesces updates, off the manager lock, so agents no longer wait on disk syncs; write errors are logged and published as `store.error`. A clean shutdown flushes pending writes.
- Each workspace keeps its 20 newest checkpoint files (checkpoints with a profile snapshot are always kept), event logs rotate at 16 MiB, and log files of suspended workspaces are closed.
- The JPEG producer reuses its frame buffers instead of allocating two full-size images per frame.
- A viewer that keeps falling behind asks for keyframes with a backoff (1 s up to 8 s) instead of forcing one on every viewer each second, and a viewer whose connection failed is dropped.
- The dashboard no longer refetches cursors, workspaces and tabs on every pointer move.
- The Paseo plugin wakes live views at most every 100 ms per workspace.
- CI fails when the committed `web/dist` does not match `web/src`.

### Security

- Cursor responses no longer include `token_hash` or `launch.env`, which sibling cursors in the same workspace could read.
- The cursor token no longer appears in the agent's command line: Claude Code reads it from a `0600` MCP config file that is removed when the agent exits, and Codex from its environment.
- A subagent that requests only permissions its parent does not hold is refused instead of getting the default permission set.
- Non-admin cursors can no longer launch subagents with `launch.profile: "full"`, which loaded root's agent settings. Prompts starting with `-` are no longer parsed as CLI options, and launched agents get an allowlisted environment instead of the daemon's.
- Cursors need `desktop` to read the clipboard, the screenshot and the window list, and `process` to kill a process (only their own or a descendant's, unless they have `workspace`). `thaw` and `browser-restart` need the same permission as `freeze`. Spawning a subagent with a worktree needs `process`.
- Cursors without a workspace no longer see each other in `GET /v1/cursors`.
- Only viewer tokens are accepted as `?token=`; admin and cursor tokens must go in the `Authorization` header.
- App ids are validated, so they can no longer place the root-created app log outside the workspace.
- The daemon no longer follows symlinks planted by the apps user: the workspace directory and `run/` stay owned by root, and ownership changes, Chrome preferences and X authority files go through no-follow, beneath-the-base file operations.
- `browser_audit` checks broken links without cookies and skips logout and delete links.

### Fixed

- The prompt of `full` profile subagents named a tool that does not exist (`wait_events`).
- A controlling web viewer sent a `noop` on every `control.*` event, which the daemon treated as human input: every agent was locked out for 1.5 s and the stream jumped to 30 fps.
- Idle suspends are single-flight and skip a runtime that was resumed or used meanwhile, so a queued suspend no longer tears down a freshly resumed workspace.
- Suspending a destroyed workspace no longer makes it resumable again.
- A checkpoint of a frozen workspace no longer hangs forever (and with it the scheduler of every workspace).
- Two workspaces sharing a profile could deadlock the daemon while starting their browsers; a profile is now reserved atomically until its Chrome exits.
- Browser, app and viewer calls on a runtime that is shutting down fail instead of starting processes nobody tracks.
- A cancelled cursor could come back as `running` if it was cancelled while its agent was starting.
- Concurrent worktree spawns could get the same port block.
- A resize during capture could read past the frame buffer and crash the daemon; display size reads are locked and short frames are rejected.
- Damage was lost when a partial grab failed, leaving stale areas on every viewer.
- The encoder bitrate cap now follows the current frame rate instead of the one it was opened with.
- Windows can leave fullscreen with `_NET_WM_STATE_TOGGLE`.
- The WebSocket viewer recreates its video decoder after a decode error instead of freezing on the last frame.
- The dashboard stopped refreshing the tab list once 300 events had arrived.
- The Paseo plugin kept live hubs (and their JPEG streams) alive forever after a view closed on a static desktop, and concurrent agent opens could create duplicate workspaces.

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
