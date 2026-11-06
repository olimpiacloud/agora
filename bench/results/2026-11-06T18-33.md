# Carbon vs Xvfb+x11vnc+noVNC+Chrome

Host: AMD Ryzen 5 3400G with Radeon Vega Graphics, 8 threads; Google Chrome 157.0.8103.38; carbon 0.1.0

Window per CPU sample: 10s. PSS sums every process of each stack (Chromium included).

| Scenario | Metric | Carbon | Baseline | Unit | Notes |
|---|---|---:|---:|---|---|
| 1 idle daemon | memory PSS | 12.1 | — | MB | baseline has no daemon |
| 1 idle daemon | CPU | 0.0 | — | % |  |
| 2 empty desktop | memory PSS | 49.1 | 89.3 | MB | carbon: daemon+Xvfb+WM; baseline: Xvfb+openbox+x11vnc+websockify |
| 2 empty desktop | CPU idle | 0.1 | 0.1 | % |  |
| 2 empty desktop | start time | 103.0 | 307.0 | ms |  |
| 3 desktop + Chromium | cold start to page loaded | 588.0 | 453.0 | ms |  |
| 3 desktop + Chromium | memory PSS | 315.0 | 361.8 | MB | Chromium included on both sides |
| 3 desktop + Chromium | CPU idle, no viewer | 0.4 | 0.1 | % |  |
| 4 agent capture | full screenshot latency p50 | 34.7 | 34.9 | ms | carbon: X shm grab of whole desktop + JPEG; baseline: CDP Page.captureScreenshot |
| 4 agent capture | tab screenshot via API p50 | 35.5 | 34.9 | ms | carbon adds HTTP+auth+arbitration over raw CDP |
| 4 agent capture | semantic snapshot p50 | 1.1 | — | ms | no baseline equivalent; replaces screenshots for agents |
| 5 input | click → DOM mousedown p50 (X11 seat) | 1.2 | 3.8 | ms | carbon: API→own MPX seat via XTEST; baseline: xdotool process |
| 5 input | click → DOM mousedown p50 (CDP) | 12.6 | — | ms | carbon semantic click through API |
| 6 five agents with a browser | new agent ready (cursor+window vs new stack) | 100.0 | 482.0 | ms |  |
| 6 five agents with a browser | memory PSS | 314.2 | 1503.6 | MB | carbon: 1 desktop, 1 Chromium, 5 windows; baseline: 5 full stacks |
| 6 five agents with a browser | CPU idle, no viewer | 0.2 | 0.4 | % |  |
| 7 idle cursors | memory per idle cursor | 1.4 | — | KB | 100 logical cursors without graphics; baseline would need a stack each |
| 8 animated page, no viewer | CPU | 14.6 | 12.9 | % | page animation cost on both; carbon encodes nothing |
| 9 streaming, 1 viewer | server CPU (desktop+browser+stream) | 30.4 | 14.1 | % | baseline client requests raw RFB through websockify (cheapest server encoding) |
| 9 streaming, 1 viewer | frames/s delivered | 29.2 | 15.5 | fps |  |
| 9 streaming, 1 viewer | bandwidth | 424.0 | 5870.7 | kbit/s | H.264 vs raw RFB |
| 10 streaming, 3 viewers | server CPU | 31.2 | 19.0 | % | carbon encodes once and fans out |
| 11 suspended workspace | memory PSS (whole carbon) | 41.5 | 393.1 | MB | baseline cannot suspend without losing the live session |
| 11 suspended workspace | suspend time | 185.0 | — | ms | checkpoint + graceful browser close |
| 11 suspended workspace | resume until 5 tabs restored | 1026.0 | 453.0 | ms | baseline: cold start of one stack |
| 12 teardown | destroy workspace | 216.0 | — | ms |  |
