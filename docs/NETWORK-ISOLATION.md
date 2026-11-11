# Network isolation for sandboxed workspaces (proposal)

Status: spike. A prototype sits behind `AGORA_EXPERIMENTAL_NETNS=1` and is off by default. With the variable unset nothing changes. Nothing here ships to users yet.

Every claim below is marked with where it comes from: **measured** (a test or an experiment run on the development host, Ubuntu 26.04, kernel 7.0), or **read** (a manual page or the code, cited).

## Goal and non-goals

Goal: a page or an app in a sandboxed workspace cannot reach services bound to the host's loopback (databases, admin panels, development servers, the Agora API) or, if the maintainer wants it, the LAN and link-local addresses such as the cloud metadata endpoint `169.254.169.254`. Pages keep internet access, and tabs can still reach the development servers of the workspace's own subagents.

Non-goals:

- Isolating launched agents (`cursor_spawn`). They run on the host as the daemon user by design (`docs/SANDBOX.md`, "Launched agents are not sandboxed").
- Isolating cursors of the same workspace from each other. One workspace keeps one network.
- Bandwidth limits, traffic logging, or content filtering.
- Unsandboxed workspaces. They keep the host network.

## What depends on the shared network today

1. **Internet access** for pages and apps. Chrome and every app reach the network directly from the host namespace.
2. **Development servers of subagents.** A subagent launched with a worktree runs on the host, unsandboxed, with a port block in `PORT`, `AGORA_PORT_BASE` and `AGORA_PORTS` (`internal/core/launcher.go:287`). The docs promise that "several children can run dev servers side by side and test them in their own tabs" (`site/docs/agents/workflows.mdx:109`). Those servers listen on the host's loopback; the tabs live in the workspace's Chrome.
3. **Name resolution.** The sandbox sees the host's `/etc` read-only and hides `/run`.

Measured while doing this spike, and independent of network isolation: on hosts where `/etc/resolv.conf` is a symlink into `/run` (the Ubuntu default with systemd-resolved: `/etc/resolv.conf -> ../run/systemd/resolve/stub-resolv.conf`, `nameserver 127.0.0.53`), name resolution is **already broken** in sandboxed workspaces. Inside the helper, `/etc/resolv.conf` is a dangling link and `getent hosts example.com` exits 2. A sandboxed tab navigating to `https://example.com/` gets `net::ERR_NAME_NOT_RESOLVED`; the same navigation in an unsandboxed workspace on the same daemon loads "Example Domain". This should be fixed whatever is decided here (see "DNS").

What does not depend on the network:

- The X server. Sandboxed processes reach it through the socket file `/tmp/.X11-unix/X<n>`, bound into the sandbox (`internal/core/runtime.go`, `sandboxWrap`); Xvfb runs with `-nolisten tcp` (`internal/x11/display.go:190`). X also listens on an abstract unix socket, which belongs to a network namespace; clients fall back to the socket file (measured, see below).
- Chrome's control channel. The daemon drives Chrome over `--remote-debugging-pipe`, inherited file descriptors.
- The human's video stream. The daemon produces it on the host from the X server.
- The Agora API. Reachable from the sandbox today, but nothing inside needs it: apps get `AGORA_WORKSPACE` and no token.

## Design: one namespace per workspace

Namespaces are created per launch today: Chrome (`internal/browser/browser.go`, `sandbox.Attr()`) and every desktop app (`internal/core/workspace.go`, `launch`) start through their own helper with new mount, PID, IPC and UTS namespaces. A network namespace cannot work that way: a server an app starts on `127.0.0.1` must be reachable from the same workspace's Chrome. So `CLONE_NEWNET` must not go into `Attr()`. Instead there is one network namespace per workspace, and every sandboxed launch of that workspace joins it.

Prototype (in the tree, behind the switch):

- **Create and keep alive.** On the first sandboxed launch, `Runtime.sandboxWrap` starts a parked holder, `sleep infinity`, with `CLONE_NEWNET`, `Setpgid` and `Pdeathsig: SIGKILL`. The namespace is `/proc/<holder pid>/ns/net`. Starting it fails closed: the launch returns the error instead of falling back to the host network. No interface, route or firewall rule is created on the host.
- **Join.** `sandbox.Spec` has a `NetNS` field; `Wrap` emits `-netns PATH`. The helper, after its usage and PID-1 checks and **before** `setupRoot` (the `/proc` path stops being meaningful once the root is replaced), opens the path, calls `setns(fd, CLONE_NEWNET)` and brings `lo` up with two `ioctl`s. The helper already locks its goroutine to one OS thread (plan 019), so the namespace, like the capability bounding set, is inherited by the program `supervise` forks from that thread.
- **Tear down.** `Runtime.shutdown`, which runs on suspend and destroy (and on daemon shutdown, which suspends every workspace), kills and reaps the holder after the browser and apps are stopped. A launch racing with shutdown cannot start a new holder: `sandboxWrap` refuses while the runtime is closing.
- **Display crash.** The holder belongs to the runtime, not to the display. When Xvfb crashes the runtime survives and the next display reuses the same holder, so that path needs no change and leaks nothing.
- **Holder death.** If something kills the holder, it stays a zombie until shutdown reaps it, so its PID cannot be reused; the `/proc` path of a zombie has no `ns/net`, so later launches fail with "join network namespace". Fail closed, but the workspace needs a restart.

For production the parked process should go. Recommendation: the daemon creates the namespace itself on a locked thread (`unshare(CLONE_NEWNET)`, open `/proc/thread-self/ns/net`, `setns` back to the host namespace, unlock), keeps the file descriptor in the `Runtime`, and passes it to each helper as an inherited descriptor (`-netns-fd N`) instead of a path. An open descriptor keeps the namespace alive, needs no extra process, no bind mount under `/run/netns` that a crash could leave behind, and the kernel frees the namespace when the daemon exits. One caveat: `supervise` forwards every open descriptor from 3 upwards to the child, so the helper must close the namespace descriptor before it forks. The `Pdeathsig` of the current holder is tied to the OS thread that started it, which Go rarely retires; another reason not to keep the holder.

## What the prototype proved

All measured, as root, with the `agora` and `agora-x` users, Xvfb and `google-chrome`:

- `TestHelperJoinsNetworkNamespace` (`internal/sandbox/sandbox_test.go`): the helper joins a given namespace (its `/proc/self/ns/net` link equals the holder's), sees exactly one interface, `lo`, and without `-netns` stays in the caller's namespace.
- A manual run of the helper against a fresh namespace showed `lo` going from `<LOOPBACK> state DOWN` to `<LOOPBACK,UP,LOWER_UP>` with `127.0.0.1/8` and `::1/128`.
- `TestExperimentalNetns` (`e2e/netns_test.go`), with the switch on:
  - **Question 1, sharing:** an app and Chrome of the same workspace report the same network namespace link, different from the host's. A `python3 -m http.server` app listening on `127.0.0.1:8765` inside the workspace is reachable from the workspace's tab ("Directory listing" title). After `POST /suspend`, no process is left in that namespace.
  - **Question 2, X and Chrome:** Chrome starts, a `data:` page evaluates `document.title == "ok"`, and Chrome has a window on the workspace's X server (`GET /windows` is not empty). So the X client falls back from the abstract socket to the socket file. Chrome's log shows only the D-Bus errors every sandboxed workspace has.
  - **Question 3, loopback isolation:** navigating the tab to the harness's site on the host's loopback (`http://127.0.0.1:<port>/work?n=net`) does not load (the title is never "Work net"). The app sees only `lo` in `/proc/net/dev`.
- Control run: the same test with the switch forced off fails on every isolation assertion (the app lists the host's interfaces, including `docker0`, `tailscale0` and the LAN card; Chrome is in the host namespace; the host site loads with title "Work net"; hundreds of processes share the namespace after suspend). So the assertions are not vacuous.
- `TestSandboxedWorkspace` passes with the switch unset in the same invocation: the default path is unchanged.

## Egress options

The prototype has loopback only. Three ways to give it the internet back:

| | (a) veth pair + NAT by the daemon | (b) user-space stack (`pasta`, `slirp4netns`) | (c) no interface, forward proxy in the daemon |
|---|---|---|---|
| Host needs | `ip_forward=1`, a masquerade rule, an input filter for the veth side, per-workspace addresses; coexistence with ufw and Docker rules | the `passt` package (pasta) or `slirp4netns` installed; no sysctl or firewall change | nothing new; the daemon opens a listener inside the namespace |
| Host loopback | unreachable by default (martian destination); a chosen port needs DNAT **and** `route_localnet=1` on that veth | reachable by default through the gateway address; pasta `--no-map-gw` / `--map-host-loopback none`, slirp4netns `--disable-host-loopback` | unreachable; the proxy decides per destination |
| Host services on `0.0.0.0` | **reachable** through the gateway address unless filtered | reachable on the host's global address unless excluded (pasta `--map-guest-addr none`; no host firewall, so the LAN-facing address stays reachable) | refused by the proxy's policy |
| LAN, `169.254.169.254` | reachable unless filtered with nft | reachable; neither tool has a destination filter (read) | refused by the proxy's policy (measured) |
| UDP (QUIC, WebRTC in pages) | works | works (pasta, slirp4netns) | does not work; Chrome falls back to TCP for HTTP, WebRTC needs TURN over TCP/TLS |
| Apps other than Chrome | everything works | everything works | only programs that honour `HTTP(S)_PROXY` |
| DNS inside | generated `resolv.conf` with a reachable resolver | pasta `--dns-forward`; slirp4netns built-in `10.0.2.3` | not needed for Chrome (it sends host names to the proxy); apps resolve through the proxy or not at all |
| Per-workspace cost | a veth pair, a /30, nft rules; no process | one process per workspace (pasta forks to the background; exits when the namespace's procfs entry goes away) | a listener and goroutines in the daemon; no process |
| Runs as | root daemon (needs `CAP_NET_ADMIN` on the host) | can run unprivileged against a namespace it owns; outside the sandbox, so plan 019's `no_new_privs` does not constrain it | the daemon |

Evidence:

- **(a) measured, in a throwaway namespace only.** Run under `unshare --net --mount --pid --fork`: an outer private namespace played the host, an inner one the workspace, joined by a veth pair (`10.99.0.1/30` and `10.99.0.2/30`). Results: a service bound to `0.0.0.0:9002` on the "host" answered `200` through the gateway address; one bound to `127.0.0.1:9001` did not answer; a DNAT from `10.99.0.1:9001` to `127.0.0.1:9001` did not work until `net.ipv4.conf.vh.route_localnet=1` was set on the veth (then `200`); an nft input chain on the veth (`ct status dnat accept`, `ct state established,related accept`, `drop`) blocked `9002` and kept the DNATed `9001`. The host's ruleset (`nft -s list ruleset` hash), links, routes and `route_localnet` were identical before and after. Not measured: the masquerade path to the real internet, which would need host changes.
- **(b) read, not verified by experiment.** Neither `pasta` nor `slirp4netns` is installed on the development host, and the spike installs nothing. Sources: `pasta(1)` at https://passt.top/builds/latest/web/passt.1.html and `slirp4netns(1)` at https://github.com/rootless-containers/slirp4netns/blob/master/slirp4netns.1.md. Key points: pasta attaches with `--netns PATH` (or a PID); by default it translates the gateway address to the host's loopback ("Default is to translate the guest's default gateway address, unless --no-map-gw is given"); `-T, --tcp-ns` forwards "from target namespace to init namespace" with range syntax such as `41000-41019`, and `-t none` / `-u none` disable inbound forwarding; the default outbound rule auto-scans and forwards namespace connections to host loopback listeners, so `-T` and `-U` must be set explicitly; there is no option to restrict destination addresses. slirp4netns maps host loopback at `10.0.2.2` by default, `--disable-host-loopback` is all-or-nothing, its port API (`add_hostfwd`) is host-to-namespace only, and its manual warns that a loopback host resolver stays reachable through the built-in DNS.
- **(c) measured.** A 120-line Go program (kept out of the tree) created a TCP listener at `127.0.0.1:3128` **inside** a private namespace from a locked thread (`setns` in, `listen`, `setns` back) and served HTTP and `CONNECT` from the host side, dialing only the resolved address it had checked. The host had no listener on `3128`. From inside the namespace: a direct `https://example.com` failed (no route); through the proxy `CONNECT https://example.com` and `GET http://example.com` returned `200`; `127.0.0.1:47811` (the "allowed worktree port") returned `200`; `127.0.0.1:47812`, `localhost:47812`, `169.254.169.254` and `192.168.1.1` returned `403`; `CONNECT 127.0.0.1:47812` was refused. Resident memory: about 9 MB for the whole proxy process, which would be shared by every workspace inside the daemon.

Recommendation: **(c) for Chrome, the forward proxy in the daemon**, as the first shipped version. It needs nothing installed and no host firewall change (which this project's own operating rules forbid on the reference host), it enforces one destination policy for loopback, LAN, link-local and metadata in code that can be tested, it gives worktree ports for free (next section), and it removes the DNS problem for Chrome. Its costs are real: no UDP in pages, and apps that ignore proxy variables have no network at all. If those matter, (b) with pasta is the fallback: add it as an optional dependency, run with `--no-map-gw --map-host-loopback none --map-guest-addr none -t none -u none -T <worktree ports> -U none`, and add an nft table **inside the workspace namespace** (not the host's) to drop LAN and link-local destinations, since pasta cannot. Option (a) is not recommended: it puts per-workspace firewall and forwarding state on the host, next to ufw and Docker, and a mistake in it exposes every `0.0.0.0` service.

## Worktree ports

Each subagent with a worktree gets a block of host loopback ports, `AGORA_PORT_BASE` to `AGORA_PORT_BASE + AGORA_PORTS - 1` (`internal/core/launcher.go:287`). Requirement: a tab of the subagent's workspace reaches exactly the blocks of the cursors working in that workspace, and nothing else on the host's loopback.

- With (c): the proxy's policy allows `127.0.0.1:<port>` and `localhost:<port>` only when the port is inside a block of a live cursor whose workspace is this one. The daemon already knows the blocks (it allocates them), so the allow-list is computed per request; a cursor that ends loses access immediately. Measured in the experiment above with one allowed port. The tab keeps using `http://localhost:<PORT>`, as the docs say.
- With (b): pasta `-T <base>-<last>` per block. The list is fixed when pasta starts; a new subagent means restarting pasta for that workspace (connections drop) or starting one pasta per block. The namespace side sees the ports on its own loopback, so URLs stay `localhost:<PORT>`.
- With (a): DNAT per block on the veth plus `route_localnet=1` on that veth, which the experiment showed is required; the nft input chain must then accept only DNATed traffic. Rules change on every spawn.

## DNS

- Today, systemd-resolved hosts: broken in every sandboxed workspace (measured, above). Fix independent of this proposal: the helper builds the sandbox's mount tree and `/run` is a fresh tmpfs inside it, so it can write a generated file at the path the symlink points to (`/run/systemd/resolve/stub-resolv.conf`), or bind a generated `resolv.conf` over that path. In the host network namespace the generated file can keep `nameserver 127.0.0.53`.
- (c): Chrome needs no resolver. With an HTTP proxy it sends host names to the proxy, which resolves them on the host (measured: `CONNECT example.com` worked from a namespace with no resolver). The proxy must check the **resolved** address and dial that address, so a name that resolves to `127.0.0.1` or a LAN address is refused (measured with `localhost`). Apps resolve nothing unless they use the proxy.
- (b): pasta forwards DNS from an address of our choice with `--dns-forward ADDR` to the host's resolver; the generated `resolv.conf` then says `nameserver ADDR`. slirp4netns uses `10.0.2.3`. Both manuals note the loopback stub case; slirp4netns warns that it can expose the host's loopback resolver.
- (a): the generated `resolv.conf` must name a resolver reachable through the veth (not `127.0.0.53`), for example a DNAT of the gateway's port 53 to the stub with `route_localnet`, or an upstream resolver.

## How it would be switched on

Proposal, smallest first:

1. A daemon flag `agora serve --sandbox-network host|isolated` (default `host` at first), replacing `AGORA_EXPERIMENTAL_NETNS`. It applies to sandboxed workspaces only.
2. A workspace config field `"network": "host" | "isolated"`. The admin can choose either; a cursor can only tighten (`isolated`), like the sandbox default (`docs/SANDBOX.md`, "Default"), and a workspace created by a cursor in an isolated workspace is isolated.
3. Later, if it holds up, `isolated` becomes the default for sandboxed workspaces.

Like the sandbox probe at startup, the daemon should check once that it can create a namespace and open the proxy listener in it, log why not, and refuse to start isolated workspaces rather than silently running them on the host network.

## Limits

Even with isolation:

- Launched agents and unsandboxed workspaces keep the host network.
- With (c), the proxy is a new piece of root-run code parsing untrusted HTTP from pages. It must use the standard library server with timeouts and size limits, and never follow redirects or interpret content.
- The allowed worktree ports are reachable from every tab of that workspace, not only the tab of the subagent that owns them (cursors of one workspace are not isolated from each other).
- Whatever the proxy (or pasta) allows is reachable: the internet, including anything else on the host's public address if the policy does not exclude the host's own addresses. The policy must list the host's interface addresses as refused destinations, not only loopback and private ranges.
- The X server and the kernel are still shared; isolation of the network does not change "Same user, same kernel" in `docs/SANDBOX.md`.
- IPv6 must be covered by the same policy (`::1`, `fc00::/7`, `fe80::/10`, IPv4-mapped addresses).

## Open questions for the maintainer

1. **Default or opt-in?** Recommendation: opt-in (`--sandbox-network isolated` or the workspace field) for one release, then default for sandboxed workspaces once the proxy has been used in practice.
2. **Which egress mechanism?** Recommendation: the forward proxy in the daemon (c). Alternative: pasta (b) as an optional dependency, if UDP or non-proxy apps matter. Not recommended: host firewall configuration by the daemon (a).
3. **Block LAN and link-local along with loopback?** Recommendation: yes, by default, including `169.254.169.254` and the host's own interface addresses, with an admin-only allow-list for specific LAN destinations.
4. **Forward worktree ports automatically?** Recommendation: yes, the ports of live cursors in the same workspace, computed per request by the proxy. Without it the documented "test them in their own tabs" workflow breaks for isolated workspaces.
5. **Must UDP inside pages keep working?** Recommendation: no for the first version (Chrome falls back to TCP for HTTP; WebRTC inside pages needs TURN over TCP). If yes, (c) is out and (b) is the answer.
6. **Fix the systemd-resolved DNS breakage now?** Independent of the above, sandboxed workspaces cannot resolve names on such hosts today. Recommendation: fix it in the helper first, as its own change.

## Work breakdown

In order, each its own plan:

1. **Sandbox DNS fix.** Generate the resolver file inside the sandbox's `/run` when `/etc/resolv.conf` points there. Test: an app in a sandboxed workspace resolves `localhost` and a tab loads a name served by a test resolver. Independent of isolation.
2. **Namespace lifecycle without a holder process.** Daemon-created namespace kept as a descriptor in the `Runtime`, passed to the helper as an inherited descriptor and closed before `supervise` forks; startup probe; replace the environment variable with `--sandbox-network`. Keep `TestHelperJoinsNetworkNamespace` and `TestExperimentalNetns`, adapted.
3. **Forward proxy.** Listener inside the namespace, HTTP and `CONNECT`, resolve-then-check-then-dial, refused ranges (loopback, private, link-local, multicast, the host's own addresses, IPv6 equivalents), timeouts and limits; Chrome launched with `--proxy-server` and apps with `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`. Unit tests for the policy; e2e for a public-like site served by the harness on a non-loopback test address.
4. **Worktree ports through the proxy.** Allow-list from live cursors of the workspace; e2e: a subagent's dev server is reachable from the tab, another workspace's is not.
5. **Workspace field and docs.** `"network"` in the workspace config with the admin/cursor rules, `docs/SANDBOX.md` "Limits" updated, `CHANGELOG.md`, the site's sandbox page.
6. **Optional: pasta backend**, only if question 5 is answered "UDP must work".

If the direction is rejected: remove the `-netns` flag, `NetNS`, `joinNetNS`, `loopbackUp`, the holder in `Runtime`, the switch and the two tests in one commit; keep this document as the record of why.
