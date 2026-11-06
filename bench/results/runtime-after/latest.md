# Carbon vs Xvfb+x11vnc+noVNC+Chrome

Host: AMD Ryzen 5 3400G with Radeon Vega Graphics, 8 threads; Google Chrome 157.0.8103.38; carbon 0.1.0

Window per CPU sample: 10s. PSS sums every process of each stack (Chromium included).

| Scenario | Metric | Carbon | Baseline | Unit | Notes |
|---|---|---:|---:|---|---|
| 1 idle daemon | memory PSS | 11.8 | — | MB | baseline has no daemon |
| 1 idle daemon | CPU | 0.0 | — | % |  |
| 2 empty desktop | memory PSS | 36.3 | 81.7 | MB | carbon: daemon+Xvfb+WM; baseline: Xvfb+openbox+x11vnc+websockify |
| 2 empty desktop | CPU idle | 0.1 | 0.1 | % |  |
| 2 empty desktop | start time | 98.0 | 246.0 | ms |  |
| 3 desktop + Chromium | cold start to page loaded | 550.0 | 426.0 | ms |  |
| 3 desktop + Chromium | memory PSS | 281.8 | 325.7 | MB | Chromium included on both sides |
| 3 desktop + Chromium | CPU idle, no viewer | 0.5 | — | % |  |
| 4 agent capture | full screenshot latency p50 | 30.3 | 39.0 | ms | carbon: X shm grab of whole desktop + JPEG; baseline: CDP Page.captureScreenshot |
| 4 agent capture | tab screenshot via API p50 | 37.2 | 39.0 | ms | carbon adds HTTP+auth+arbitration over raw CDP |
| 4 agent capture | semantic snapshot p50 | 1.2 | — | ms | no baseline equivalent; replaces screenshots for agents |
| 5 input | click → DOM mousedown p50 (X11 seat) | 1.1 | 3.9 | ms | carbon: API→own MPX seat via XTEST; baseline: xdotool process |
| 5 input | click → DOM mousedown p50 (CDP) | 13.8 | — | ms | carbon semantic click through API |
| 6 five agents with a browser | new agent ready (cursor+window vs new stack) | 156.0 | 521.0 | ms |  |
| 6 five agents with a browser | memory PSS | 326.4 | 1453.8 | MB | carbon: 1 desktop, 1 Chromium, 5 windows; baseline: 5 full stacks |
| 6 five agents with a browser | CPU idle, no viewer | 0.2 | 0.2 | % |  |
| 7 idle cursors | memory per idle cursor | 1.3 | — | KB | 100 logical cursors without graphics; baseline would need a stack each |
| 8 animated page, no viewer | CPU | 16.4 | 13.3 | % | page animation cost on both; carbon encodes nothing |
| 9 streaming, 1 viewer | server CPU (desktop+browser+stream) | 20.8 | 14.1 | % | baseline client requests raw RFB through websockify (cheapest server encoding) |
| 9 streaming, 1 viewer | frames/s delivered | 14.9 | 14.7 | fps |  |
| 9 streaming, 1 viewer | bandwidth | 302.7 | 5861.6 | kbit/s | H.264 vs raw RFB |
| 9b streaming, viewer interacting | server CPU | 31.9 | 14.1 | % | viewer sends pointer moves every 100 ms; baseline row repeats scenario 9 |
| 9b streaming, viewer interacting | frames/s delivered | 29.3 | 14.7 | fps |  |
| 10 streaming, 3 viewers | server CPU | 22.5 | 19.2 | % | carbon encodes once and fans out |
| 11 suspended workspace | memory PSS (whole carbon) | 41.1 | 331.0 | MB | baseline cannot suspend without losing the live session |
| 11 suspended workspace | suspend time | 189.0 | — | ms | checkpoint + graceful browser close |
| 11 suspended workspace | resume until 5 tabs restored | 1030.0 | 426.0 | ms | baseline: cold start of one stack |
| 12 teardown | destroy workspace | 248.0 | — | ms |  |
