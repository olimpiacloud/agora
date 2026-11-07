# Security policy

## Reporting a vulnerability

Don't open a public issue. Report it privately, either way:

- GitHub private reporting: **Security** tab → **Report a vulnerability** ([direct link](https://github.com/olimpiacloud/agora/security/advisories/new)).
- Email to **security@olimpia.dev**.

Include, if you can:

- the version (`agora version`) and how you run it (systemd unit, root or not, `--listen`, tunnel in front of it);
- the steps or a minimal script that reproduces it;
- what an attacker gains (reading the admin token, escaping a cursor's permissions, reaching another workspace, running code as root, reaching CDP or the X server from outside, etc.).

We acknowledge receipt within three business days, agree on a disclosure date with the reporter, and credit them in the advisory unless they prefer otherwise.

## Supported versions

Agora is at 0.x: only the latest release receives security fixes.

| version | supported |
|---|---|
| latest `0.x` | yes |
| earlier | no |

## Threat model

- **Trusted:** whoever operates the host and holds the admin token (`/var/lib/agora/admin.token`, mode 0600).
- **Partially trusted:** agents holding cursor tokens. A cursor can only act within its workspace and its permissions, and can only hand narrower permissions to its children. Isolation between cursors of the same workspace is logical (locks, tab owners, scoped tokens), not a security boundary: put agents that must not see each other in separate workspaces.
- **Untrusted:** the web content loaded in Chromium and the apps launched on the desktop. Browsers and apps run as the unprivileged `agora` user with Chrome's sandbox enabled.

What Agora guarantees by default:

- The daemon listens only on `127.0.0.1:7788` and refuses other interfaces unless `AGORA_ALLOW_PUBLIC=1` is set. Expose it through an authenticated tunnel (for example Cloudflare Tunnel with Access), never as an open port.
- Chromium is driven through `--remote-debugging-pipe`: there is no CDP TCP port. X displays are local sockets, not TCP.
- Every API call needs a token; viewer links carry short-lived tokens scoped to one workspace.
