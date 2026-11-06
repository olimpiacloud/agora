# Agora vs Xvfb+x11vnc+noVNC+Chrome

Host: AMD Ryzen 5 3400G with Radeon Vega Graphics, 8 threads; Google Chrome 157.0.8103.38; agora 0.1.0

Window per CPU sample: 10s. PSS sums every process of each stack (Chromium included).

| Scenario | Metric | Agora | Baseline | Unit | Notes |
|---|---|---:|---:|---|---|
| 1 idle daemon | memory PSS | 11.7 | — | MB | baseline has no daemon |
| 1 idle daemon | CPU | 0.0 | — | % |  |
| 2 empty desktop | memory PSS | 36.7 | 81.3 | MB | agora: daemon+Xvfb+WM; baseline: Xvfb+openbox+x11vnc+websockify |
| 2 empty desktop | CPU idle | 0.2 | — | % |  |
| 2 empty desktop | start time | 113.0 | 308.0 | ms |  |
| 3 desktop + Chromium | cold start to page loaded | 623.0 | 486.0 | ms |  |
| 3 desktop + Chromium | memory PSS | 284.5 | 326.9 | MB | Chromium included on both sides |
| 3 desktop + Chromium | CPU idle, no viewer | 0.3 | — | % |  |
| 4 agent capture | full screenshot latency p50 | 30.2 | 33.9 | ms | agora: X shm grab of whole desktop + JPEG; baseline: CDP Page.captureScreenshot |
| 4 agent capture | tab screenshot via API p50 | 44.3 | 33.9 | ms | agora adds HTTP+auth+arbitration over raw CDP |
| 4 agent capture | semantic snapshot p50 | 1.2 | — | ms | no baseline equivalent; replaces screenshots for agents |
| 5 input | click → DOM mousedown p50 (X11 seat) | 1.2 | 3.7 | ms | agora: API→own MPX seat via XTEST; baseline: xdotool process |
| 5 input | click → DOM mousedown p50 (CDP) | 13.6 | — | ms | agora semantic click through API |
| 6 five agents with a browser | new agent ready (cursor+window vs new stack) | 125.0 | 503.0 | ms |  |
| 6 five agents with a browser | memory PSS | 326.8 | 1437.8 | MB | agora: 1 desktop, 1 Chromium, 5 windows; baseline: 5 full stacks |
| 6 five agents with a browser | CPU idle, no viewer | 0.4 | 0.1 | % |  |
| 7 idle cursors | memory per idle cursor | 2.2 | — | KB | 100 logical cursors without graphics; baseline would need a stack each |
| 8 animated page, no viewer | CPU | 17.2 | 13.0 | % | page animation cost on both; agora encodes nothing |
| 9 streaming, 1 viewer | server CPU (desktop+browser+stream) | 30.4 | 15.1 | % | baseline client requests raw RFB through websockify (cheapest server encoding) |
| 9 streaming, 1 viewer | frames/s delivered | 29.2 | 15.8 | fps |  |
| 9 streaming, 1 viewer | bandwidth | 425.1 | 5865.0 | kbit/s | H.264 vs raw RFB |
| 9b streaming, viewer interacting | server CPU | 32.5 | 15.1 | % | viewer sends pointer moves every 100 ms; baseline row repeats scenario 9 |
| 9b streaming, viewer interacting | frames/s delivered | 29.3 | 15.8 | fps |  |
| 10 streaming, 3 viewers | server CPU | 31.9 | 20.8 | % | agora encodes once and fans out |
| 11 suspended workspace | memory PSS (whole agora) | 43.2 | 328.9 | MB | baseline cannot suspend without losing the live session |
| 11 suspended workspace | suspend time | 195.0 | — | ms | checkpoint + graceful browser close |
| 11 suspended workspace | resume until 5 tabs restored | 1020.0 | 486.0 | ms | baseline: cold start of one stack |
| 12 teardown | destroy workspace | 255.0 | — | ms |  |
