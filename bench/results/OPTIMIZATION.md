# Optimización: antes y después (2026-11-06)

## Tokens de subagentes

Tres tareas reproducibles (`bench/agent_bench.py`: compra en una tienda local, búsqueda en olimpia.dev, revisión visual), 2 repeticiones cada una, Claude Sonnet 5.5. Costo calculado desde las transcripciones de Claude Code, deduplicando por id de mensaje, a precios de API.

| Configuración | Correctas | Costo (6 tareas) | Turnos | Lecturas de caché | Tiempo total |
|---|---:|---:|---:|---:|---:|
| Antes | 6/6 | $2,009 | 40 | 2,47 M | 175 s |
| Ronda 1 | 6/6 | $0,308 | 29 | 0,27 M | 69 s |
| Ronda 2 | 6/6 | $0,246 | 31 | 0,25 M | 60 s |
| Ronda 2, effort low | 6/6 | $0,227 | 29 | 0,24 M | 54 s |

Contexto inicial por subagente: 66.700 → 7.500 tokens.

Auditoría real (landing de olimpia.dev, mismo prompt): $0,927 → $0,299, 21 → 17 turnos, igual cantidad de capturas.

Qué cambió:

- Lanzador lean de Claude Code: `--strict-mcp-config`, `--disable-slash-commands`, `--tools ""`, `--setting-sources ""` y un prompt de sistema propio. Esto saca el prompt por defecto, las skills, otros MCP, el CLAUDE.md global y las herramientas de sistema.
- Perfiles de herramientas MCP según permisos (`AGORA_TOOLS`), descripciones cortas (`AGORA_LEAN`), sin el parámetro `workspace` para cursores. Las herramientas pasan de 5,7k a 2,9k tokens.
- Observación incremental tras cada acción (`open`, `navigate`, `click`, `fill`, `press`, `select`, `scroll`): solo lo visible y solo lo que cambió desde la vista anterior de esa pestaña.
- Capturas para agentes a 1024 px y JPEG 60 por defecto.
- Uso real por cursor (`--output-format json`): tokens, turnos, costo y duración en `cursor.usage`.

## Runtime

`go run ./cmd/agora-bench` con el binario anterior (`0e804b1`) y el nuevo, misma máquina. Resultados completos en `runtime-before/` y `runtime-after/`.

| Escenario | Antes | Después | noVNC |
|---|---:|---:|---:|
| Streaming, 1 espectador mirando (CPU) | 30,4% a 29 fps | 20,8% a 15 fps | 14,1% a 15 fps |
| Streaming, 3 espectadores (CPU) | 31,9% | 22,5% | 19,2% |
| Streaming, espectador interactuando | 32,5% a 29 fps | 31,9% a 29 fps | — |
| Ancho de banda, 1 espectador | 425 kbit/s | 303 kbit/s | 5.862 kbit/s |
| Resume con 5 pestañas | 1.020 ms | restauración en paralelo | — |

FPS adaptativos: 30 fps mientras el humano interactúa (2 s desde el último input), 15 fps si solo mira en modo interactivo, 8 fps en observación.
