# Changelog

All notable changes to Agora. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- Idle reaping: tabs unused for `--tab-idle` (15 minutes by default) are closed unless their cursor is waiting for a human or the viewer was used in that time, Chromium stops after two minutes with no tabs, and apps of cursors that already ended are stopped. Events `scheduler.tab_closed`, `scheduler.browser_stopped` and `scheduler.app_stopped`.
- Stream endpoints take `wake=0`: on a suspended workspace they answer `{"type":"bye","reason":"suspended"}` (WebRTC: `409`) instead of resuming it. The web viewer and the Paseo panel use it on reconnects and show a Resume button.
- Pending cursors without a launch and without a Paseo agent are cancelled after 24 hours, and the Paseo plugin deletes cursors of Paseo agents that no longer exist, are closed or are archived.
- `agora serve --cursor-retention` (7 days by default, `0` keeps them): finished cursors with no live descendants are deleted after that time.
- `make check`: gofmt, vet, the unit tests and both TypeScript typechecks; tests that need Xvfb skip when it is missing.
- Snapshots report ARIA state (`checked` and `disabled` from `aria-*`, plus `selected` and `expanded`).
- `board_read` returns posts oldest first with `more: true` when there are more than `limit`, so posts are no longer skipped.
- `agora serve --sandbox-default` (on by default): new workspaces are sandboxed unless the admin creates them with `"sandbox": false`; cursors cannot opt out. Existing workspaces keep their stored setting.
- `agora serve --worktree-roots`: directories where cursors may create git worktrees.
- `agora serve --x-user`: the user Xvfb runs as.
- Deleting a cursor closes its tabs and stops its apps, and tabs owned by cursors that already ended can be used by others.
- The Paseo live panel takes pointer, wheel and keyboard input.
- `AGORA_TOKEN_FILE` for the CLI and MCP server.
- `agora serve --workspace-retention` (7 days by default, `0` keeps them): destroyed workspaces are purged after that time, together with their browser profile (unless a live workspace shares it), home and checkpoints. They used to stay on disk forever. On the first start after upgrading, workspaces destroyed more than 7 days ago are purged within a minute. Deleting a cursor also removes its `agents/<id>` directory and log.

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
- Snapshots stop collecting at the element cap and compute names only for the elements they return, and tabs no longer enable the CDP `Runtime` domain.
- MCP coordinates are mapped from the last screenshot only until `browser_snapshot boxes=true`, `desktop_windows` or a resize; tool descriptions say which coordinate space applies. `browser_eval` results over 64 KB are truncated in MCP.
- Tab actions on a tab with an open JavaScript dialog fail at once with the dialog text instead of waiting for the timeout, and an action that opens a dialog returns it.
- Encoder rate control keeps the VBV buffer at the real bitrate when the frame rate changes, failed grabs refresh the frame without forcing a keyframe, and a WebRTC viewer asks for a keyframe when it connects.
- The Paseo plugin times out daemon requests after 10 s, and the web viewer reconnects after repeated decoder errors.
- The Paseo panel sends input over its viewer socket (held buttons and keys are released when the panel goes away, and non-finite coordinates are dropped), the agent wall shares one cursor-metadata poll and only polls visible tiles, and toolbar chips no longer remount on every frame.
- Screenshots are kept per workspace (64 shots or 24 MB each, 256 MB in total) and dropped when the workspace suspends or is destroyed.
- MCP tool calls are cancelled on `notifications/cancelled`; after stdin closes, running calls get up to 30 s to finish.
- Deleting a cursor (also when the daemon prunes a finished one) removes its git worktrees when they have no modified or untracked files, and frees their port block; the branch and its commits stay. A worktree with changes is kept and reported with a `worktree.kept` event. One cursor tree can hold at most 10 worktrees; the admin is not limited.

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
- Only admins can launch `codex` subagents: Codex keeps its built-in shell, which would run as the daemon user.
- Cursors need `desktop` to open streams, `desktop` or `browser` for saved screenshots, `browser` to list tabs, `process` to list processes and `message` to message cursors or post on the board. `checkpoint`, `resize` and `layout` need the `workspace` permission; `thaw` no longer does, since every call resumes a workspace anyway. An admin acting as a cursor is never limited.
- Cursors cannot lock the `workspace` surface; other surface locks need the matching permission, expire after at most 10 minutes and carry a reason of at most 200 characters.
- Workspaces created by a cursor stay sandboxed when its own workspace is, cannot exceed its budget, and one cursor tree can keep at most 5 live workspaces.
- Cursor uploads must come from the workspace home.
- Browser navigation, new tabs and autostart URLs accept only `http`, `https`, `about:blank` and `data:`.
- Password and one-time-code inputs no longer leak their value as the element name in snapshots.
- Worktree git commands run without repository hooks or fsmonitor, and worktree port counts are limited to 20.
- The agent environment allowlist also passes `AWS_`, `GOOGLE_`, `CLOUD_ML_`, `AZURE_` and `VERTEX_` variables, and logs the names of variables it drops.
- Xvfb runs as a dedicated X user (`agora serve --x-user`, `agora-x` by default, created by the installer) instead of root, and never as the apps user: apps cannot signal it, attach its MIT-SHM segments or replace its socket, and the daemon checks the socket owner and peer credentials before dialing. Without that user it falls back to root and logs why.
- Sandboxed workspaces get a minimal `/dev` (null, zero, full, random, urandom, tty, a private devpts and `/dev/shm`) and a `nodev` root.
- Human control (`control-take`, `control-return`) is admin-only.
- Cursors only get worktrees of repositories owned by the daemon user, inside `--worktree-roots` when set.
- The installer writes the agents' token to a `0600` file and registers it as `AGORA_TOKEN_FILE` instead of passing it in argv and storing it in agent configs (existing inline entries are migrated), and aborts instead of building `main` when no release is found. The uninstaller removes that file as its owner.
- Read-only tab actions no longer claim a tab, and claims are atomic, so a cursor that only looked at a tab cannot close it when it ends.
- The stream `hello` message no longer includes `token_hash` or `launch.env` of the workspace's cursors, which every viewer (including read-only viewer links) could read.

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
- `agora open <ws> --control=false` ignored flags after the workspace and granted control.
- A slow viewer on a still screen could keep a stale picture forever, and the stream loop could spin when every viewer had disconnected.
- A data race on xgb's global extension tables could crash the daemon; X extensions are registered once per process.
- A viewer `take` racing with its disconnect could leave a permanent human lock.
- The CDP client could hang a call made while the browser closed, writes ignored their context, and events could be lost right after launch.
- Attaching to a tab twice created two CDP sessions, and a failed attach left a half-attached tab or an orphan target.
- Navigating to a `#fragment` of the current page always timed out.
- An agent result line over 16 KB lost its usage and was stored as broken JSON.
- A cursor cancelled while it completed could keep its result, failed state writes are retried, and a clean shutdown waits for agents to record how they exited.
- Pruning could delete finished cursors that a live root still needed, breaking `cursor_get` and `cursor_wait`; whole finished trees are pruned instead.
- A file or symlink planted at `home/Downloads` or `home/.xdg-run` stopped the workspace from starting.
- File operations fall back to a no-follow walk when `openat2` is blocked by seccomp, and root caps how much of an apps-user file it reads.
- The dashboard tab list now refreshes on tab actions and reclaimed tabs.
- Share links can refresh the lock list (viewer tokens may read `GET /v1/workspaces/{id}/locks`).
- A sandboxed app killed right after launch could ignore SIGTERM.
- Opening or attaching to a page that shows a dialog on load no longer hangs.
- Shutdown waits for launched agents to record their result before the final state flush.
- Detailed usage of a suspending workspace, and the set of purged workspace logs, no longer grow without bound.
- The Paseo plugin removes the Agora workspace of a Paseo workspace when it is archived, and its periodic reconcile removes workspaces whose Paseo workspace no longer exists (never-started ones are purged; ones that ran keep their profile on disk). Busy workspaces and workspaces without a Paseo label are left alone. Scheduled Paseo runs no longer pile up workspaces.
- A paused agent whose status changed without `resume` (a CAPTCHA followed by a human returning control, or a `status` call) stayed stopped while shown as running; its process is now resumed whenever the cursor leaves `paused`, and cancelling a paused agent no longer waits 5 seconds.
- A cascading cancel marks the cursor cancelled before its children, so a subagent spawned during the cancel can no longer survive it.
- An expired workspace is destroyed once: `workspace.expired` and `workspace.destroyed` are no longer published again every two seconds while the destroy runs, and destroying an already destroyed workspace does nothing.
- Input from a viewer that was already detached no longer marks a suspended or frozen workspace active or restarts it, a call that arrives while an idle suspend is queued keeps its runtime, the list of tabs to restore survives a restore that restored nothing, and a browser crash is recorded (with its tabs and a `browser.crashed` event) even when an API call notices it before the watcher does.

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
