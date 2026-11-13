# Architecture

## Decision

A Go daemon (a modular monolith) that runs **one virtual X server (Xvfb) per active workspace**, acts as a minimal EWMH window manager itself, and multiplexes every cursor (agent) of the workspace inside that desktop:

- **Truly concurrent input with MPX (XInput 2.2).** Each cursor that needs graphical input gets, on demand, its own *master pointer + master keyboard* pair with its own XTEST device and its own keyboard focus. Two agents type at the same time into two different windows without mixing keys or modifiers (test C). Seats are a scarce resource (about 6 per display, bounded by the X server's device limit) and are recycled after inactivity.
- **Semantic input over CDP** for Chromium tabs: clicks, keys and text insertion go straight to the target, without OS focus, so cursors on different tabs compete for nothing.
- **One Chromium per workspace**, driven through `--remote-debugging-pipe` (no TCP port), running as the unprivileged `agora` user with Chrome's sandbox enabled. Each cursor opens its own windows; `isolated=true` uses a separate BrowserContext.
- **On-demand capture**: XShm of the root window or, for covered windows, Composite. Damage tracking (XDamage) wakes the encoder only when pixels change.
- **Streaming**: H.264 encoded once and fanned out to every viewer, with IDR on request. VAAPI hardware encoding (libva loaded at runtime, P-frames only, colour conversion on the GPU, only damaged rows uploaded) when a GPU supports it, x264 (cgo, `ultrafast/zerolatency`) otherwise or after any VAAPI failure; `AGORA_ENCODER` overrides the choice. WebRTC transport (Pion) when UDP is available, WebSocket + WebCodecs as a fallback that crosses HTTP tunnels, and damage-driven JPEG for light clients (the Paseo panel, mobile). With no viewers, neither the capturer nor the encoder exists.
- **Decoupled persistence**: the Chrome profile lives on disk by name; checkpoints record tabs (with their owner), apps to relaunch and cursors. *Freeze* stops every process (cgroup freezer or SIGSTOP) keeping RAM; *Suspend* checkpoints, closes the browser gracefully and frees everything; *Resume* rebuilds and restores tabs to their owners.
- **Idle reaping**: a tab no cursor has used for `--tab-idle` (15 minutes) is closed, unless its cursor is waiting for a human or someone used the viewer in that time; Chromium stops after two minutes with no tabs and relaunches on the next browser call. Apps of cursors that already ended (for example relaunched from a checkpoint) are stopped once they have run for `--tab-idle`; cancelling or deleting a cursor stops its apps right away. A workspace with no agent activity or human input for `--suspend-after` (30 minutes) is suspended even if someone is watching it, and resumes on the next call; viewers reconnect with `wake=0` so they do not resume it on their own. A destroyed workspace keeps its record, profile, home and checkpoints for `--workspace-retention` (7 days) and is purged after that.

## Alternatives considered

| Option | Why not (for now) |
|---|---|
| Headless Wayland (wlroots/sway, Weston) | Native multi-seat and per-toplevel capture are attractive, but there is no mature Go client for per-seat virtual pointer/keyboard or screencopy, and Chromium and X11 apps end up on Xwayland anyway. MPX on Xvfb gave truly concurrent input with much less risk. The graphics backend is isolated in `internal/x11` so a Wayland one can be added. |
| Xpra | Very complete per-window remoting, but it is a whole Python stack with its own protocol and client; it duplicates what Agora needs to control (damage, encoding, per-agent input). |
| VNC/noVNC | The baseline: one server per desktop, framebuffer polling, no notion of agents or multiple pointers. |
| One Chromium per agent / one container per cursor | What the design rules out; the benchmark shows the cost (about 4.5x the memory with 5 agents). |
| Playwright | Not needed: CDP over a pipe covers what is used, and Agora's own semantic layer is more compact for LLMs. |

## Known limits

- Isolation between cursors of the same workspace is logical (locks, tab owners, scoped tokens), not a security boundary. Workspaces are sandboxed by default (`--sandbox-default`): Chrome and their apps run in their own mount, PID, IPC and UTS namespaces with a minimal `/dev` (see [SANDBOX.md](SANDBOX.md)). Only the admin can create a workspace with `"sandbox": false`, and workspaces created before the default keep their config. Without the sandbox, the apps and Chrome of every workspace run as the same `agora` user, so separate workspaces are separate processes, profiles and displays, but not a security boundary between each other. Xvfb runs as a dedicated `agora-x` user (`--x-user`), neither root nor the apps user, so apps cannot signal another workspace's X server, read its capture memory or replace its socket.
- *Hibernated* (memory checkpoint) is not implemented; RAM is not restored after *Suspend*.
- Xvfb has no GPU; Chromium uses software rasterization; WebGL runs on SwiftShader (CPU) in every workspace.
- WebRTC needs UDP to the host; behind a Cloudflare Tunnel the WebSocket transport is used.
