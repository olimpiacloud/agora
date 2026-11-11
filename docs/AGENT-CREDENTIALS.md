# A non-admin credential for the user's own agent

Status: proposal. Nothing here changes how Agora behaves today. The prototype described below is off unless `AGORA_MCP_SESSION=1` is set, and it is not documented anywhere else.

## Problem

The installer gives the operator's agent the admin token. When `scripts/install.sh` runs under `sudo` and finds Claude Code or Codex for `SUDO_USER`, `save_token` copies `admin.token` to `~/.config/agora/token` (mode 0600, `scripts/install.sh:214-227`), and `configure_agents` registers `agora mcp` with `AGORA_TOKEN_FILE` pointing at it (`scripts/install.sh:269`, `:287`). Run as plain root, it writes no file and `agora mcp` reads `/var/lib/agora/admin.token` itself (`internal/client/client.go:37-47`). `README.md:52` documents this.

That contradicts the project's own trust model:

- `SECURITY.md:29-31`: the operator holding the admin token is trusted, agents holding cursor tokens are partially trusted, and web content is untrusted.
- `site/docs/agents/security.mdx:15`: the admin token is for "Nobody but the operator".
- `site/docs/agents/connect.mdx:33`: "The admin token reaches every workspace. Use cursor tokens for agents."

These agents read untrusted pages through Agora's browser tools. With the admin token, there are two ways a page that injects instructions can take over the host:

1. **Through the MCP tools.** `cursor_spawn` (`internal/mcp/tools.go:372`) accepts `launch: {adapter: "command", command: [...]}`. The admin skips every launch check (`internal/api/server.go:692-715`). The `command` adapter runs the argv as given (`internal/core/launcher.go:210-214`), and `launchAgent` (`internal/core/launcher.go:260`) starts it as the daemon user without a sandbox. Under the shipped systemd unit, that user is root.
2. **Through the file.** Claude Code and Codex have shell tools. Any process of that user can read `~/.config/agora/token` and call the API directly, MCP or not.

No safer credential exists to hand out today. A cursor token belongs to one cursor, and a cursor that is never launched and never acts is cancelled after 24 hours (`internal/core/scheduler.go:256-271`).

## What an admin token adds

Each row compares the admin with a default cursor (permissions `desktop, browser, process, spawn, message`, `internal/core/cursor.go:27`).

| Capability | Through `agora mcp` | Admin | Default cursor | Evidence |
|---|---|---|---|---|
| Launch `command` or `codex` agents, or set `launch.env` | `cursor_spawn` | yes | refused | `internal/api/server.go:704-711` |
| Launch an agent with the `full` profile | `cursor_spawn` | yes | refused | `internal/api/server.go:712-715` |
| Place a child in any workspace | `cursor_spawn` | yes | only in its own workspace or one it owns | `internal/api/server.go:700-703`, `:834-840` |
| Act in any workspace (desktop, browser, apps, board, locks) | every workspace-scoped tool | yes | own workspace only | `internal/api/server.go:125-134`, `internal/api/auth.go:150-161` |
| List every workspace | `workspace_list` | yes | own workspace only | `internal/api/server.go:225-234` |
| Reuse a named browser profile, set `browser_args` | `workspace_create` (`profile`) | yes | refused | `internal/api/server.go:210-214` |
| Create an unsandboxed workspace when the default is sandboxed | not exposed | yes | ignored | `internal/api/server.go:204`, `:897-902` |
| Create workspaces | `workspace_create` | unlimited | needs `workspace`; at most 5 live per cursor tree | `internal/api/server.go:189-192`, `:862-895` |
| suspend, freeze, checkpoint, fork, resize, layout, browser-restart | `workspace_op` | yes | needs `workspace` | `internal/api/server.go:606-616` |
| See, wait on, cancel, pause or transfer any cursor | `cursor_list`, `cursor_get`, `cursor_wait`, `cursor_control` | yes | own subtree (listing also own workspace) | `internal/api/server.go:135-148`, `:473-483`, `:505-525`, `:753-756` |
| Message any cursor | `cursor_message` | yes | own tree or workspace | `internal/api/server.go:744-752`, `:939-948` |
| Events of every workspace | `events_wait` | yes | own workspace | `internal/api/server.go:950-975` |
| Per-workspace usage of the whole host | `system_info` | yes | own workspace only | `internal/api/server.go:155-164` |
| Worktree of a repository outside `--worktree-roots`, for any cursor | `worktree_create` | yes | own cursor, inside the roots | `internal/api/server.go:540-548`, `internal/core/worktree.go:86-91` |
| Kill any process of a workspace | not exposed | yes | needs `process`; with `workspace` any, else its own | `internal/api/server.go:919-937` |
| Delete a workspace | not exposed | yes | refused | `internal/api/server.go:246` |
| Mint viewer links (`agora open`) | not exposed | yes | refused | `internal/api/server.go:169`, `cmd/agora/main.go:183` |
| Take or return human control | not exposed | yes | refused | `internal/api/server.go:607-610` |
| Remove a worktree | not exposed | yes | refused | `internal/api/server.go:565` |
| Act as any cursor with `X-Agora-Cursor` | `AGORA_ACT_AS` | yes, and stays admin | ignored | `internal/api/auth.go:116-125` |

In `agora mcp` itself the admin flag only keeps the `workspace` argument visible in `tools/list` (`internal/mcp/server.go:366`). Everything else is enforced by the daemon, so a fix belongs in what token `agora mcp` holds, not in which tools it shows.

The rows marked "not exposed" are still one `curl` away for an agent with a shell and the admin token.

## Options

### A: `agora mcp` drops privilege at start

When it finds an admin token, `agora mcp` creates a workspace and a cursor for the session and uses that cursor's token for every tool call. A variable keeps the old behaviour.

- Closes: path 1. Injected instructions can no longer reach admin-only calls through the MCP tools.
- Does not close: path 2. The admin token is still in a file the agent's shell can read.
- Correction to the plan: `AGORA_ACT_AS` is not a way to do this. With the admin token and an `X-Agora-Cursor` header, the principal keeps `Admin: true` (`internal/api/auth.go:116-125`). The session has to switch to the cursor's own token.
- Cost: small, client-side only. The admin can already do everything the session needs.

### B1: a standing cursor stored instead of the admin token

The installer creates a root cursor with no workspace and permissions `desktop, browser, process, spawn, message, workspace`, and stores its token in `~/.config/agora/token`. Each `agora mcp` session creates or reuses a workspace owned by that cursor, places a child cursor in it, and works as the child.

- Closes: paths 1 and 2. What sits in the user's home can no longer launch arbitrary commands, reach other workspaces, or mint viewer links.
- Does not close: the standing token is still readable by the agent. Whoever holds it has a partially trusted cursor: up to 5 live workspaces (`internal/api/server.go:862`), any number of `claude` subagents in its tree, which run as the daemon user with only the Agora tools (`internal/core/launcher.go:167-171`), `desktop_launch` apps in its own workspaces, and worktrees of any git repository the daemon can read unless `--worktree-roots` is set (`internal/core/worktree.go:139-160`). The caps on launched agents and on workspaces per tree (plans 006 and 007) bound this.
- Reuses the whole permission model: no new principal type, the same checks the Paseo plugin's cursors go through (`paseo-plugin/server/agora.ts:143-149`).
- Cost: daemon changes (see "Changes the full feature would need"), installer and uninstaller changes, a migration.
- The user's agent loses everything in the table above: the `codex` and `command` adapters, `full` profiles, named browser profiles, `browser_args`, the sandbox opt-out, other workspaces, deleting workspaces, viewer links, human control. The operator keeps all of them through `sudo agora ...`.

### B2: a new token kind

A fourth principal next to admin, cursor and viewer, whose only power is to open sessions (create a workspace and a cursor in it).

- Closes the same paths as B1, and holds less: the stored token could not act in a workspace itself.
- Cost: a new token type in `internal/api/auth.go` with storage, rotation and listing; every handler has to reject it by default, as handlers do for viewers today (`internal/api/server.go:107-110`); and the `GET /v1/workspaces` and ownership rules still need the same changes as in B1. More code for a small gain over B1 with a marker only the admin can set.

### C: documentation only

Keep the installer as it is, state the risk in `README.md`, `SECURITY.md` and the agent docs, and add an installer flag to skip saving the token.

- Closes nothing. Makes the trade-off visible and lets careful operators opt out.
- Cost: an afternoon.

## Recommendation

B1, with A's session mechanics in `agora mcp`.

- It is the only option that takes the admin credential out of the agent's reach. A alone leaves path 2 open to any agent that has a shell, which is every agent the installer configures.
- It adds no principal type. Standing and session cursors are ordinary cursors, checked by code that already guards Paseo agents and launched subagents.
- The prototype shows that the session part works on today's API. What is missing is a handful of daemon changes, each small and testable.

A can still ship first as a stop-gap. It costs little and the B1 client code is a superset of it.

## What the prototype showed

`internal/api/session_flow_test.go` walks the flow against the real HTTP API, with no X or Chrome:

1. The admin creates a cursor with no workspace and permissions `desktop, browser, process, spawn, message, workspace`.
2. With that token, `POST /v1/workspaces` returns 201, and the workspace's owner is the standing cursor.
3. With that token, `POST /v1/cursors` with the new workspace returns 201 and the child's token (allowed by `ownsWorkspace`, `internal/api/server.go:834-840`).
4. With the child's token, `GET /v1/me` reports the workspace, and `GET /v1/workspaces/<id>` and `GET /v1/workspaces` show it. The runtime starts lazily on the first desktop or browser call (`internal/core/runtime.go:110`), so no explicit start is needed.
5. The child is refused every admin-only call: `command` and `codex` launches, `browser_args`, a named profile, deleting the workspace, viewer links and taking control all return 403.

`internal/mcp/session_test.go` covers the client side. With `AGORA_MCP_SESSION=1` and a token that `GET /v1/me` reports as a cursor without a workspace, the first tool call creates one workspace and one child cursor, switches the client to the child's token (kept in memory only), and makes the session workspace the default. Without the variable, `agora mcp` sends exactly the requests it sent before. If a creation fails, the client keeps its token and every tool call returns the error instead of carrying on with the more powerful credential. An admin token, or a cursor that already has a workspace, is left alone.

Gaps found, each pinned by an assertion in `session_flow_test.go` so a fix shows up as a failing test:

- **An owner without a workspace cannot see what it owns.** `GET /v1/workspaces` returns an empty list, and `GET /v1/workspaces/<id>`, `start` and opening a tab return 403, because `CanWorkspace` compares only against `p.Cursor.Workspace` (`internal/api/auth.go:150-161`). Only the board and child placement consult ownership. A session that wants to reuse a workspace cannot find it.
- **A tree is capped at 5 live workspaces, and only the admin can destroy one.** With one workspace per session, the sixth session fails with 403 and never recovers (`internal/api/server.go:862-895`, `:246`).
- **Nothing marks a standing cursor.** `reapCursors` cancels only cursors that are `pending`, have no launch and lack the `paseo_agent` label, after 24 hours (`internal/core/scheduler.go:262`). A standing cursor never acts in a workspace, so it stays `pending` and is reaped. It can avoid that by reporting once (`Report` moves `pending` to `running`, `internal/core/cursor.go:292-294`), after which neither the reaper nor `pruneCursors` ever removes its tree. Both escapes are available to any cursor: `paseo_agent` is a free-form label that a cursor can set on its own children (checked in the test), and any cursor can report. Neither distinguishes a standing cursor from any other, so it cannot be listed, rotated or protected as one.
- **Sessions leave cursors and workspaces behind.** Nothing ends the child cursor or releases the workspace when `agora mcp` exits. Session children that acted are `running` forever, and their whole tree escapes the retention prune because the standing cursor is never terminal.
- **A half-done session leaks.** If the workspace is created and the cursor creation fails, the workspace stays, and counts toward the cap.

## Changes the full feature would need

- **Daemon, marker** (`internal/core/cursor.go`, `internal/core/scheduler.go`, `internal/api/server.go`): a `standing` flag on cursors that only the admin can set at creation, never inherited, exempt from `reapCursors` and the prune, refused as a child of a cursor.
- **Daemon, ownership** (`internal/api/auth.go`, `internal/api/server.go`): let a standing cursor list and read the workspaces its tree owns, without acting in them. Reading is enough; sessions act through children.
- **Daemon, cleanup** (`internal/api/server.go`, `internal/core/workspace.go`): let a standing cursor suspend or destroy a workspace its tree owns, or end session children automatically when their token has been unused for a while, so the 5-workspace cap is not a dead end.
- **Daemon, label** (plan 028): reserve `paseo_agent` and similar labels for the admin.
- **`agora mcp`** (`internal/mcp/server.go`): the session mode from the prototype, plus workspace reuse by name, completing the child cursor on exit (`Serve` returns), and the admin-token branch of option A.
- **Installer** (`scripts/install.sh`): create the standing cursor with the admin token (`POST /v1/cursors` with the marker) and save its token instead of the admin token in `save_token`; register `agora mcp` with `AGORA_MCP_SESSION=1`.
- **Uninstaller** (`scripts/uninstall.sh`): cancel the standing cursor before removing `~/.config/agora/token` (it already removes the file, `scripts/uninstall.sh:79-85`).
- **Docs**: `README.md:52`, `SECURITY.md` (tokens table), `site/docs/agents/security.mdx`, `site/docs/agents/connect.mdx`, `site/docs/(start)/install.mdx`, and this file's status.

## Migration

Existing installs have the admin token in `~/.config/agora/token`, referenced by `AGORA_TOKEN_FILE` in the Claude Code and Codex configuration. The upgrade path that fits the installer's habits (it already migrates inline tokens, `scripts/install.sh:233-245`):

1. On the next run, create the standing cursor and overwrite `~/.config/agora/token` with its token. The MCP registration does not change; only `AGORA_MCP_SESSION=1` is added, which means removing and re-adding the server as `migrate_mcp` does.
2. Warn that the admin token was readable by the agent and print the rotation command the installer already uses (`sudo rm /var/lib/agora/admin.token && sudo systemctl restart agora`). Rotation restarts the service, which ends every running session and launched agent, so it should not happen silently.
3. Without `--keep-admin-token` the swap happens; with it, the installer keeps the admin token and only warns. Installs as plain root (no `SUDO_USER`) keep reading `/var/lib/agora/admin.token` and get the warning.

The Paseo plugin needs no migration: it keeps the admin token to itself and already hands out cursor tokens.

## Limits

- The standing token is still a credential in the user's home, readable by the agent. B1 limits what it can do, not who can use it.
- Launched `claude` subagents run as the daemon user (root under the shipped unit). Their restriction is Claude Code's own tool list (`--tools ""`), not the operating system (`docs/SANDBOX.md`, "Launched agents are not sandboxed"). A standing cursor can start them; plan 007's cap bounds how many.
- Cursors of the same workspace are not isolated from each other (`SECURITY.md:30`). If sessions share a workspace, a compromised session can disturb the others.
- The prototype creates a new workspace for every session and never cleans up. It is for testing the flow, not for daily use.
- If `GET /v1/me` fails at the first tool call in session mode, the whole session reports the error until `agora mcp` restarts. The identity is resolved once per process, as it is today.

## Open questions for the maintainer

1. **What may the user's main agent do?** Losing the `command` and `codex` adapters, `full` profiles, named profiles, `browser_args`, viewer links, other workspaces and workspace deletion, with `sudo agora ...` for those. Recommended: accept it for the default install. The two that will be missed most are viewer links (`agora open`) and shared logged-in profiles; both could come back later as narrow, admin-granted permissions instead of the admin token.
2. **Which option ships?** A now, B1, B2 or C. Recommended: A as a quick stop-gap only if B1 is more than a release away; otherwise B1 directly. C in any case for the release that still ships the admin token.
3. **Which workspace does a session use?** One per user, one per project directory, or one per session. Evidence: one per session hits the 5-workspace cap at the sixth session; logged-in state lives in the browser profile, and a cursor cannot name a shared profile (`internal/api/server.go:210-214`). Recommended: one workspace per project directory, found by name, so logins persist per project and the cap counts projects, not sessions.
4. **Migration of existing installs.** Replace the token silently on upgrade, or keep it and warn? Rotate the admin token as part of it? Recommended: replace with a warning, and print the rotation command instead of rotating, since rotation restarts the daemon.
5. **Rotation and revocation of the standing credential, and where it is listed.** Recommended: list standing cursors in `GET /v1/cursors` and the dashboard with their marker; revoke by cancelling (the token stops working at once, `internal/core/manager.go:287-300`); rotate by re-running the installer, which cancels the old one and creates a new one.
6. **How is a standing cursor marked so that no child can mark itself the same way?** Labels are free-form today. Recommended: a dedicated field that only the admin can set at creation, never a label.
7. **Should session children end when `agora mcp` exits?** Ending them frees seats and locks and lets the prune work, but a crashed client leaves them `running` either way. Recommended: complete on clean exit, and add an idle timeout for children whose token has not been used for some hours.
8. **Who may destroy a session workspace?** Today only the admin. Recommended: the owning tree, limited to workspaces it created, so the cap is self-healing.
9. **Should the standing cursor have `process`?** Without it, sessions cannot use `desktop_launch` or `worktree_create`, and worktrees are the riskiest residual power (any readable repository unless `--worktree-roots` is set). Recommended: keep `process`, and have the installer set `--worktree-roots` to the user's home when it creates a standing cursor.
