# Contributing

Thanks for your interest. Issues and PRs are welcome; code, docs, error messages and commits are in English.

## Requirements

- Linux x86_64 or arm64. Agora drives Xvfb, MPX and cgroups, so it does not run on macOS or Windows (a Linux VM works).
- Go (the version in `go.mod`), a C compiler, `pkg-config`, `libx264-dev` and `libva-dev` (the encoders are cgo).
- `xvfb` and Google Chrome or Chromium for the e2e tests and for running the daemon.
- [Bun](https://bun.sh) for the dashboard (`web/`) and the Paseo plugin (`paseo-plugin/`).

On Ubuntu or Debian:

```
sudo apt install build-essential pkg-config libx264-dev libva-dev xvfb
```

## Build and test

```
make build                # bin/agora with version, commit and build date
make vet
go test ./internal/... -count=1
go test ./e2e/ -count=1   # starts real Xvfb and Chrome in-process
make web                  # rebuilds web/dist, which the binary embeds
cd paseo-plugin && bun install && bun run typecheck
```

Run your own daemon without touching a system install:

```
bin/agora serve --listen 127.0.0.1:7799 --home /tmp/agora-dev
AGORA_URL=http://127.0.0.1:7799 AGORA_TOKEN=$(cat /tmp/agora-dev/admin.token) bin/agora api GET /v1/system
```

`gofmt -l cmd internal e2e web` must print nothing. CI runs vet, gofmt, the unit tests, the e2e suite, the dashboard build and the plugin typecheck.

`e2e/` covers the acceptance criteria: persistent sessions, five subagents, concurrent multi-cursor input, CAPTCHA and human takeover, freeze and suspend, streaming without viewers and reconnection, plus upload, download, dialogs and drag. If you change behavior, add or adjust a test there.

## Benchmarks

`go run ./cmd/agora-bench` compares Agora with Xvfb + x11vnc + noVNC + Chrome and writes `bench/results/`. Run it on an otherwise idle machine and mention the host in the PR when you report numbers. `bench/agent_bench.py` measures subagent token cost with Claude Code.

## Pull requests

- One change per PR, with a test when possible.
- The PR title follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) (`feat(mcp): ...`, `fix(stream): ...`, `docs: ...`, `refactor!: ...` for breaking changes). PRs are squash-merged and the title becomes the commit message. A CI check validates it.
- Add a line under `## [Unreleased]` in `CHANGELOG.md` for user-visible changes.
- No code comments: names and small functions carry the meaning.
- You don't need to sign commits or add `Signed-off-by`.

## Releases

Versions follow [semver](https://semver.org/). To release, move the `Unreleased` entries in `CHANGELOG.md` under a new `## [X.Y.Z] - YYYY-MM-DD` heading, merge, then push a `vX.Y.Z` tag. The `release` workflow builds `linux/amd64` and `linux/arm64` binaries on Debian 12 with `scripts/release-build.sh` (x264 linked statically, libva loaded at runtime when present; they need glibc 2.36 or newer), embeds the version, and publishes a GitHub release with the tarballs, `SHA256SUMS` and that `CHANGELOG.md` section as notes.

## Security

Vulnerabilities are not reported in issues: see [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contribution is published under the project's dual license, MIT or Apache-2.0, at the user's option.
