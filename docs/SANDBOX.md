# Sandboxed workspaces

Set `"sandbox": true` in a workspace config. Chrome and every app launched in that workspace then run in a confined view of the host:

```json
POST /v1/workspaces
{"name": "untrusted", "start": true, "config": {"sandbox": true}}
```

## What it does

Each sandboxed process starts through `agora sandbox-exec`. The daemon re-runs its own binary in new mount, PID, IPC and UTS namespaces (Linux features that give a process its own view of mounts, process IDs, shared memory and hostname). The helper then does the following:

- Mounts the whole system read-only and without setuid bits.
- Hides every top-level directory except the system ones (`/usr`, `/etc`, `/opt`, `/bin`, `/sbin`, `/lib*`, `/var`, `/sys`, `/dev`, `/proc`, `/snap`) behind empty tmpfs mounts. That covers `/root`, `/home`, `/tmp`, `/run`, `/mnt`, `/srv`, `/projects` and so on.
- Also hides `/var/lib` (which holds `/var/lib/agora`, Docker and databases), `/var/log`, `/var/tmp`, `/var/spool`, `/var/mail`, `/var/www`, `/var/backups` and `/var/opt`, plus the Agora home directory wherever it lives.
- Binds back, read-write, only what the workspace needs: its own directory (`workspaces/<id>`, which contains its home and run directory), its Chrome profile, and its own X socket.
- Gives the workspace a private `/tmp` and `/dev/shm`, and a `/proc` that shows only its own processes. Apps cannot see, signal or ptrace processes of other workspaces, even though they run as the same `agora` user.
- Drops to the unprivileged apps user with no supplementary groups and an empty capability bounding set, then runs the program. The helper stays as PID 1, reaps zombies and forwards SIGTERM, SIGINT and SIGHUP.

Chrome's own sandbox keeps working: its renderers still run under seccomp-bpf (a kernel filter on system calls) in their own user namespace. `TestSandboxedWorkspace` in `e2e/sandbox_test.go` checks this, together with the uid, the hidden paths, the read-only system and the process isolation.

## Why not bubblewrap

We tried bubblewrap first. On Ubuntu 24.04 and later, the AppArmor profile `bwrap-userns-restrict` confines every process started from `/usr/bin/bwrap` with `unpriv_bwrap`, which denies all capabilities. This applies even when the caller is root. Chrome inside it cannot create its user-namespace sandbox, falls back to the setuid helper, and aborts. The only bwrap options are disabling Chrome's sandbox or editing the system AppArmor policy, and we accept neither. The helper does the same mount setup as bwrap, in about 200 lines of Go, from the unconfined `agora` binary.

## Limits

Be honest with yourself about what this protects against.

- **Requires root.** The daemon must run as root with an apps user (the default for `agora.service`). Otherwise, starting a sandboxed workspace fails.
- **Network is shared.** Sandboxed processes use the host network namespace. They can reach `127.0.0.1` services, including the Agora API (which still requires a token) and any other local server. Do not run unauthenticated local services next to untrusted workspaces.
- **The X server is not sandboxed.** Xvfb runs outside the sandbox. X11 offers no isolation between clients on the same display. Every app in the workspace can read the screen and inject input into every other window of that workspace. Displays of other workspaces need their own cookie, which is not visible inside the sandbox.
- **Readable system files stay readable.** `/etc` and `/usr` are visible read-only. Anything world-readable there (configs, installed tools) can be read. Secrets must not live in world-readable files under those paths.
- **Same user, same kernel.** All workspaces run as one apps user and share the kernel. A kernel exploit or a Chrome sandbox escape plus a kernel bug escapes the sandbox. For hostile code, use a VM or a separate machine.
- **No resource limits by itself.** CPU and memory limits come from the workspace cgroup (`budget`), not from the sandbox.
- **Launched agents are not sandboxed.** Agent processes started by `cursor_spawn` (`claude`, `codex`) run as the daemon user, because they need its credentials. The sandbox covers Chrome and `desktop_launch` apps only.
- **D-Bus is unavailable.** `/run` is hidden, so apps that need the system or session bus lose those features.
