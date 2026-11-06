# Carbon

Runtime de escritorios virtuales para agentes de IA. Un daemon Go que le da a cada agente una computadora Linux real (Chromium, apps X11, terminal) sin levantar un escritorio ni un navegador por agente: los agentes son **cursores** con identidad, permisos, asiento de input propio, pestañas propias, jerarquía padre-hijo y resultados estructurados.

Decisiones técnicas: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Benchmark contra Xvfb + x11vnc + noVNC + Chrome: [bench/results/latest.md](bench/results/latest.md).

## Qué hace

- **Workspaces** persistentes con estados `created → active → idle → frozen → suspended → destroyed`, fork copy-on-write del perfil, checkpoints, TTL y presupuestos (memoria, CPU, cursores, pestañas). Cuesta cero hasta que se usa.
- **Cursores**: subagentes con token propio, permisos que solo se reducen al delegar, límites de profundidad e hijos, mensajería, progreso, `wait`, pausa, cancelación en cascada y liberación automática de asientos, locks y pestañas.
- **Input concurrente real**: cada cursor recibe su propio puntero y teclado MPX bajo demanda; en Chromium usa CDP por pestaña. Locks de superficie y toma de control humana con prioridad.
- **Control híbrido**: snapshot semántico compacto con refs (`[e3] button "Sign in"`), clicks por ref o coordenadas, fill, select, upload, descargas, diálogos, drag HTML5, eval; y control visual por píxeles para cualquier app.
- **CAPTCHAs y logins**: detección de desafíos, el cursor pasa a `needs_human`, un humano toma el control desde el viewer sin reiniciar nada y lo devuelve; el agente sigue con la misma pestaña y sesión.
- **Streaming**: WebRTC (Pion) o WebSocket + WebCodecs con H.264; JPEG por daño para clientes livianos. Sin espectadores no se captura ni se codifica nada.
- **Integraciones**: API HTTP, servidor MCP (`carbon mcp`, 48 tools), lanzador de agentes Claude Code / Codex / comandos, git worktrees con rango de puertos, dashboard React embebido y plugin de Paseo.

## Instalación (Ubuntu)

```bash
sudo apt install xvfb libx264-dev pkg-config google-chrome-stable
sudo useradd --system --create-home --home-dir /var/lib/carbon-apps --shell /usr/sbin/nologin carbon
go build -o /usr/local/bin/carbon ./cmd/carbon
sudo cp deploy/carbon.service /etc/systemd/system/ && sudo systemctl enable --now carbon
carbon token
```

El daemon escucha solo en `127.0.0.1:7788` y se niega a escuchar en otra interfaz. Para acceso remoto, publicarlo detrás de un túnel con autenticación (Cloudflare Tunnel + Access). Apps y navegadores corren como el usuario `carbon`, con el sandbox de Chrome activo, sin acceso a los archivos de root.

Dashboard: `http://127.0.0.1:7788` (pegar el token). Viewer directo con token de corta duración: `carbon open <workspace>`.

## Uso rápido

```bash
WS=$(carbon api POST /v1/workspaces '{"name":"demo","start":true}' | jq -r .id)
carbon api POST /v1/cursors "{\"name\":\"main\",\"workspace\":\"$WS\"}"
carbon api POST /v1/workspaces/$WS/tabs '{"url":"https://example.com"}'
carbon api POST /v1/workspaces/$WS/tabs/<target> '{"action":"snapshot"}'
```

Subagente real lanzado por Carbon:

```bash
carbon api POST /v1/cursors '{"name":"reader","parent":"<cursor>","goal":"Abrí example.com y devolvé el título","launch":{"adapter":"claude","model":"claude-haiku-5-5"}}'
carbon api POST /v1/wait '{"ids":["<child>"]}'
```

MCP para cualquier harness:

```json
{ "mcpServers": { "carbon": { "command": "carbon", "args": ["mcp"], "env": { "CARBON_TOKEN": "<token de cursor o admin>" } } } }
```

## Paseo

`paseo-plugin/` agrega:

- Panel **Carbon** en cada workspace: escritorio en vivo (JPEG por daño a través de la conexión de Paseo, funciona en móvil), tocar para hacer click, tipeo, teclas, scroll, tomar y devolver control, cursores con estado.
- Tarjetas en el chat para cada captura que hacen las tools de Carbon, con botón a la vista en vivo.
- Cada agente Claude/Codex de Paseo recibe el MCP de Carbon con su propio cursor y un workspace de Carbon vinculado al workspace de Paseo. Desactivable con la etiqueta `carbon/enabled=false`.

```bash
paseo plugin install /ruta/a/carbon/paseo-plugin
```

## Pruebas

```bash
go test ./internal/... ./e2e/ -count=1
go build -o bin/carbon ./cmd/carbon && go run ./cmd/carbon-bench
```

`e2e/` cubre los criterios de aceptación A (sesión persistente), B (cinco subagentes), C (multicursor), D (CAPTCHA y takeover), E (freeze y suspensión), F y G (sin espectadores, desconexión y reconexión), más upload, download, diálogos y drag.
