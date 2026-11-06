# Carbon: decisión de arquitectura (Fase 0)

## Decisión

Un daemon Go (monolito modular) que gestiona **un servidor X virtual (Xvfb) por workspace activo**, actúa él mismo como window manager EWMH mínimo, y multiplexa dentro de ese escritorio a todos los cursores (agentes) del workspace:

- **Input concurrente real con MPX (XInput 2.2).** Cada cursor que necesita input gráfico recibe, bajo demanda, su propio par *master pointer + master keyboard* con su dispositivo XTEST y su propio foco de teclado. Dos agentes escriben a la vez en dos ventanas distintas sin mezclar teclas ni modificadores (prueba C). Los asientos son un recurso escaso (≈6 por display por el límite de dispositivos del servidor X) y se reciclan tras inactividad.
- **Input semántico por CDP** para pestañas de Chromium: clicks, teclas e inserción de texto van directo al target, sin foco del sistema operativo, así que los cursores en pestañas distintas no compiten por nada.
- **Un Chromium por workspace**, controlado por `--remote-debugging-pipe` (sin puerto TCP), corriendo como usuario sin privilegios `carbon` con el sandbox de Chrome activo. Cada cursor abre ventanas propias; `isolated=true` usa un BrowserContext separado.
- **Captura bajo demanda**: XShm del root o, para ventanas tapadas, Composite. El damage tracking (XDamage) despierta al encoder solo cuando cambian píxeles.
- **Streaming**: H.264 por x264 (cgo, `ultrafast/zerolatency`, IDR a pedido) con fan-out a todos los espectadores. Transporte WebRTC (Pion) cuando hay UDP, WebSocket + WebCodecs como alternativa que atraviesa túneles HTTP, y JPEG por daño para clientes livianos (panel de Paseo, móvil). Sin espectadores no se crea ni el capturador ni el encoder.
- **Persistencia desacoplada**: el perfil de Chrome vive en disco por nombre; los checkpoints registran pestañas (con dueño), apps a relanzar y cursores. *Freeze* congela todos los procesos (cgroup freezer o SIGSTOP) conservando RAM; *Suspend* hace checkpoint, cierra el navegador ordenadamente y libera todo; *Resume* reconstruye y restaura pestañas a sus dueños.

## Alternativas evaluadas

| Opción | Por qué no (por ahora) |
|---|---|
| Wayland headless (wlroots/sway, Weston) | Multi-seat nativo y captura por toplevel son atractivos, pero no hay cliente Go maduro para virtual-pointer/keyboard por seat ni screencopy; Chromium y las apps X11 igual terminan en Xwayland. MPX en Xvfb dio input concurrente real con mucho menos riesgo. El backend gráfico está aislado en `internal/x11` para poder agregar uno Wayland. |
| Xpra | Remoting por ventana muy completo, pero es un stack Python entero con su propio protocolo y cliente; duplica lo que Carbon necesita controlar (damage, encoding, input por agente). |
| VNC/noVNC | Es el baseline: un servidor por escritorio, poll del framebuffer, sin noción de agentes ni de múltiples punteros. |
| Un Chromium por agente / contenedor por cursor | Lo que la especificación prohíbe; el benchmark muestra el costo (≈3,7× memoria con 5 agentes). |
| Playwright | No es necesario: CDP por pipe cubre lo que se usa, y la capa semántica propia es más compacta para LLMs. |

## Límites conocidos

- El aislamiento entre cursores del mismo workspace es lógico (locks, dueños de pestañas, tokens con alcance), no una frontera de seguridad. Para agentes no confiables: workspaces separados (procesos, perfil y display distintos) y, como paso siguiente, sandbox con bubblewrap.
- *Hibernated* (checkpoint de memoria) no está implementado; no se promete restaurar RAM tras *Suspend*.
- Xvfb no tiene GPU; Chromium usa rasterizado por software.
- WebRTC necesita UDP hasta el host; detrás del túnel de Cloudflare se usa WebSocket.
