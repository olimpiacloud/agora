# Streaming: after vs before

Host: AMD Ryzen 5 3400G with Radeon Vega Graphics, 8 threads; Google Chrome 157.0.8103.38; agora 0.1.0

after: `/tmp/engine/agora` (AGORA_ENCODER=""), before: `/tmp/engine/agora-before`. 2 alternating rounds per binary, window 10s per CPU sample (first and last second excluded). CPU is % of one core. Other agents share this machine: treat CPU as noisy.

| Scenario | Metric | After | Before |
|---|---|---:|---:|
| 10 streaming, 3 viewers | bandwidth per viewer | 185.66 | 297.42 |
| 10 streaming, 3 viewers | capture ms/frame | 2.53 | 3.06 |
| 10 streaming, 3 viewers | daemon CPU | 3.44 | 13.75 |
| 10 streaming, 3 viewers | encode ms/frame | 13.17 | 29.38 |
| 10 streaming, 3 viewers | fps per viewer | 16.52 | 15.65 |
| 10 streaming, 3 viewers | glass-to-encode ms | 54.29 | — |
| 10 streaming, 3 viewers | tree CPU | 21.88 | 30.62 |
| 9 streaming, 1 viewer | bandwidth per viewer | 140.95 | 254.26 |
| 9 streaming, 1 viewer | capture ms/frame | 2.88 | 2.53 |
| 9 streaming, 1 viewer | daemon CPU | 3.19 | 13.25 |
| 9 streaming, 1 viewer | encode ms/frame | 13.81 | 27.15 |
| 9 streaming, 1 viewer | fps per viewer | 14.15 | 14.55 |
| 9 streaming, 1 viewer | glass-to-encode ms | 59.07 | — |
| 9 streaming, 1 viewer | tree CPU | 20.00 | 30.06 |
| 9b streaming, viewer interacting | bandwidth per viewer | 246.75 | 360.01 |
| 9b streaming, viewer interacting | capture ms/frame | 2.33 | 2.58 |
| 9b streaming, viewer interacting | daemon CPU | 6.12 | 22.56 |
| 9b streaming, viewer interacting | encode ms/frame | 14.14 | 28.17 |
| 9b streaming, viewer interacting | fps per viewer | 26.95 | 24.45 |
| 9b streaming, viewer interacting | glass-to-encode ms | 27.35 | — |
| 9b streaming, viewer interacting | tree CPU | 28.94 | 43.62 |
| 9c full-screen animation, 1 viewer | bandwidth per viewer | 4535.43 | 6147.03 |
| 9c full-screen animation, 1 viewer | capture ms/frame | 8.97 | 14.64 |
| 9c full-screen animation, 1 viewer | daemon CPU | 6.50 | 27.00 |
| 9c full-screen animation, 1 viewer | encode ms/frame | 18.03 | 72.86 |
| 9c full-screen animation, 1 viewer | fps per viewer | 14.35 | 10.85 |
| 9c full-screen animation, 1 viewer | glass-to-encode ms | 66.19 | — |
| 9c full-screen animation, 1 viewer | tree CPU | 64.50 | 66.94 |
| L page change to first frame | median ms | 25.13 | 32.86 |
| L page change to first frame | p95 ms | 67.40 | 69.10 |

Encoder reported by the after daemon: vaapi
