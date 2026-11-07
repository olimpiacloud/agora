# Optimization: before and after (2026-11-06)

## Subagent tokens

Three reproducible tasks (`bench/agent_bench.py`: checkout on a local store, a lookup on olimpia.dev, a visual review), 2 runs each, Claude Sonnet 5.5. Cost computed from the Claude Code transcripts, deduplicated by message id, at API prices.

| Configuration | Correct | Cost (6 tasks) | Turns | Cache reads | Total time |
|---|---:|---:|---:|---:|---:|
| Before | 6/6 | $2.009 | 40 | 2.47 M | 175 s |
| Round 1 | 6/6 | $0.308 | 29 | 0.27 M | 69 s |
| Round 2 | 6/6 | $0.246 | 31 | 0.25 M | 60 s |
| Round 2, effort low | 6/6 | $0.227 | 29 | 0.24 M | 54 s |

Initial context per subagent: 66,700 → 7,500 tokens.

A real audit (the olimpia.dev landing page, same prompt): $0.927 → $0.299, 21 → 17 turns, same number of screenshots.

What changed:

- Lean Claude Code launcher: `--strict-mcp-config`, `--disable-slash-commands`, `--tools ""`, `--setting-sources ""` and Agora's own system prompt. This drops the default system prompt, skills, other MCP servers, the global CLAUDE.md and the system tools.
- MCP tool profiles by permission (`AGORA_TOOLS`), short descriptions (`AGORA_LEAN`), and no `workspace` parameter for cursors. Tools go from 5.7k to 2.9k tokens.
- Incremental observation after each action (`open`, `navigate`, `click`, `fill`, `press`, `select`, `scroll`): only what is visible and only what changed since the previous view of that tab.
- Agent screenshots at 1024 px and JPEG quality 60 by default.
- Real usage per cursor (`--output-format json`): tokens, turns, cost and duration in `cursor.usage`.

The prompts were in Spanish when these numbers were measured; they are in English now.

## Runtime

`go run ./cmd/agora-bench` with the previous binary (`0e804b1`) and the new one, same machine. Full results in `runtime-before/` and `runtime-after/`.

| Scenario | Before | After | noVNC |
|---|---:|---:|---:|
| Streaming, 1 viewer watching (CPU) | 30.4% at 29 fps | 20.8% at 15 fps | 14.1% at 15 fps |
| Streaming, 3 viewers (CPU) | 31.9% | 22.5% | 19.2% |
| Streaming, viewer interacting | 32.5% at 29 fps | 31.9% at 29 fps | — |
| Bandwidth, 1 viewer | 425 kbit/s | 303 kbit/s | 5,862 kbit/s |
| Resume with 5 tabs | 1,020 ms | parallel restore | — |

Adaptive frame rate: 30 fps while a human interacts (2 s since the last input), 15 fps when they only watch in interactive mode, 8 fps in observation mode.
