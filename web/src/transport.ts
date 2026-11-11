export type Transport = 'webrtc' | 'ws'
export type Mode = 'interactive' | 'observation'

export interface SessionStats {
  transport: Transport | 'connecting'
  fps: number
  kbps: number
  rtt: number
  width: number
  height: number
}

type Msg = Record<string, unknown>

export interface SessionCallbacks {
  onMessage: (m: Msg) => void
  onStatus: (s: string) => void
  onSize: (w: number, h: number) => void
}

export class DesktopSession {
  private ws?: WebSocket
  private pc?: RTCPeerConnection
  private input?: RTCDataChannel
  private decoder?: VideoDecoder
  private failure = ''
  private decodeErrors = 0
  private closed = false
  private frames = 0
  private bytes = 0
  private lastStat = performance.now()
  private needKey = true
  private pingT = 0
  private pingTimer?: number
  private retry = 0
  private tokenAt = Date.now()
  private connected = false
  private asleep = false
  stats: SessionStats = { transport: 'connecting', fps: 0, kbps: 0, rtt: 0, width: 0, height: 0 }

  constructor(
    private wsId: string,
    private token: string,
    private canvas: HTMLCanvasElement,
    private video: HTMLVideoElement,
    private cb: SessionCallbacks,
    private prefer: Transport | 'auto',
    private mode: Mode,
    private refresh?: () => Promise<string>,
  ) {}

  async start() {
    if (this.closed) return
    if (this.refresh && (this.retry > 0 || Date.now() - this.tokenAt > 50 * 60 * 1000)) {
      try {
        this.token = await this.refresh()
        this.tokenAt = Date.now()
      } catch {}
      if (this.closed) return
    }
    if (this.prefer !== 'ws') {
      try {
        await this.startRTC()
        if (this.closed) this.cleanupRTC()
        return
      } catch (e) {
        this.cleanupRTC()
        if (this.closed) return
        if (this.asleep) {
          this.cb.onStatus('suspended')
          return
        }
        if (this.prefer === 'webrtc') {
          this.cb.onStatus('WebRTC failed: ' + (e as Error).message)
          this.scheduleRetry()
          return
        }
      }
    }
    this.startWS()
  }

  wake() {
    this.asleep = false
    this.connected = false
    this.retry = 0
    this.cb.onStatus('resuming…')
    this.start()
  }

  private wakeParam() {
    return this.connected ? '&wake=0' : ''
  }

  private scheduleRetry() {
    if (this.closed) return
    if (this.asleep) {
      this.cb.onStatus('suspended')
      return
    }
    const d = Math.min(8000, 500 * 2 ** this.retry++)
    const why = this.failure ? `${this.failure}, ` : ''
    this.cb.onStatus(`${why}reconnecting in ${Math.round(d / 1000)}s`)
    setTimeout(() => !this.closed && this.start(), d)
  }

  private startPing(send: (m: Msg) => void) {
    clearInterval(this.pingTimer)
    this.pingTimer = window.setInterval(() => {
      this.pingT = performance.now()
      send({ t: 'ping', s: String(this.pingT) })
    }, 2000)
  }

  private handleText(data: string) {
    let m: Msg
    try {
      m = JSON.parse(data)
    } catch {
      return
    }
    if (m.type === 'pong') {
      this.stats.rtt = Math.round(performance.now() - Number(m.s))
      return
    }
    if (m.type === 'hello') this.connected = true
    if (m.type === 'hello' && m.width) {
      this.cb.onSize(m.width as number, m.height as number)
    }
    this.cb.onMessage(m)
    if (m.type === 'bye') this.onBye(m.reason)
  }

  private onBye(reason: unknown) {
    if (reason === 'suspended' || reason === 'workspace frozen') this.asleep = true
    if (this.pc) {
      this.cleanupRTC()
      if (!this.closed) this.scheduleRetry()
    } else if (this.ws && this.ws.readyState <= WebSocket.OPEN) {
      this.ws.close()
    }
  }

  private tick(n: number, decoded: boolean) {
    if (decoded) this.frames++
    this.bytes += n
    const now = performance.now()
    if (now - this.lastStat > 1000) {
      const el = (now - this.lastStat) / 1000
      this.stats.fps = Math.round(this.frames / el)
      this.stats.kbps = Math.round((this.bytes * 8) / 1000 / el)
      this.frames = 0
      this.bytes = 0
      this.lastStat = now
    }
  }

  private newDecoder(ws: WebSocket, ctx: CanvasRenderingContext2D) {
    const dec: VideoDecoder = new VideoDecoder({
      output: (f) => {
        this.decodeErrors = 0
        if (this.canvas.width !== f.displayWidth || this.canvas.height !== f.displayHeight) {
          this.canvas.width = f.displayWidth
          this.canvas.height = f.displayHeight
          this.cb.onSize(f.displayWidth, f.displayHeight)
        }
        ctx.drawImage(f, 0, 0)
        f.close()
      },
      error: () => {
        if (this.decoder !== dec) return
        this.decoder = undefined
        if (this.closed || this.ws !== ws || ws.readyState >= WebSocket.CLOSING) return
        if (++this.decodeErrors > 5) {
          this.failure = 'video decoder keeps failing'
          this.cb.onStatus(this.failure)
          ws.close()
          return
        }
        this.newDecoder(ws, ctx)
        this.needKey = true
        this.send({ t: 'key_frame' })
      },
    })
    this.decoder = dec
    dec.configure({ codec: 'avc1.42e02a', optimizeForLatency: true, hardwareAcceleration: 'no-preference' })
  }

  private startWS() {
    if (this.closed) return
    this.stats.transport = 'ws'
    this.video.style.display = 'none'
    this.canvas.style.display = 'block'
    if (typeof VideoDecoder === 'undefined') {
      this.cb.onStatus('This browser lacks WebCodecs (needs HTTPS or localhost). Use WebRTC.')
      return
    }
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/v1/workspaces/${this.wsId}/stream/ws?mode=${this.mode}&token=${encodeURIComponent(this.token)}${this.wakeParam()}`)
    ws.binaryType = 'arraybuffer'
    this.ws = ws
    const ctx = this.canvas.getContext('2d', { alpha: false, desynchronized: true })!
    this.decodeErrors = 0
    this.newDecoder(ws, ctx)
    this.needKey = true
    ws.onopen = () => {
      this.retry = 0
      this.failure = ''
      this.cb.onStatus('')
      this.startPing((m) => this.send(m))
    }
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') return this.handleText(ev.data)
      const buf = new Uint8Array(ev.data as ArrayBuffer)
      const key = (buf[0] & 1) === 1
      const dv = new DataView(buf.buffer)
      const pts = Number(dv.getBigUint64(1))
      this.stats.width = dv.getUint16(9)
      this.stats.height = dv.getUint16(11)
      const dec = this.decoder
      if ((this.needKey && !key) || !dec || dec.state !== 'configured') return this.tick(buf.length, false)
      if (dec.decodeQueueSize > 6) {
        this.tick(buf.length, false)
        this.needKey = true
        this.send({ t: 'key_frame' })
        return
      }
      this.tick(buf.length, true)
      this.needKey = false
      dec.decode(new EncodedVideoChunk({ type: key ? 'key' : 'delta', timestamp: pts * 1000, data: buf.subarray(13) }))
    }
    ws.onclose = () => {
      clearInterval(this.pingTimer)
      if (this.ws === ws) {
        try {
          this.decoder?.close()
        } catch {}
        this.decoder = undefined
      }
      if (!this.closed) this.scheduleRetry()
    }
  }

  private async startRTC() {
    this.stats.transport = 'connecting'
    const pc = new RTCPeerConnection({ iceServers: [] })
    this.pc = pc
    pc.addTransceiver('video', { direction: 'recvonly' })
    this.input = pc.createDataChannel('input', { ordered: true })
    const events = pc.createDataChannel('events', { ordered: true })
    events.onmessage = (e) => this.handleText(typeof e.data === 'string' ? e.data : '')
    pc.ontrack = (e) => {
      const r = e.receiver as RTCRtpReceiver & { playoutDelayHint?: number; jitterBufferTarget?: number }
      try {
        r.jitterBufferTarget = 0
      } catch {}
      try {
        r.playoutDelayHint = 0
      } catch {}
      this.video.srcObject = new MediaStream([e.track])
      this.video.play().catch(() => {})
    }
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    await new Promise<void>((res) => {
      if (pc.iceGatheringState === 'complete') return res()
      const t = setTimeout(res, 1500)
      pc.onicegatheringstatechange = () => {
        if (pc.iceGatheringState === 'complete') {
          clearTimeout(t)
          res()
        }
      }
    })
    const r = await fetch(`/v1/workspaces/${this.wsId}/stream/webrtc?token=${encodeURIComponent(this.token)}${this.wakeParam()}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sdp: pc.localDescription!.sdp, mode: this.mode }),
    })
    if (r.status === 409) this.asleep = true
    if (!r.ok) throw new Error(await r.text())
    const ans = await r.json()
    await pc.setRemoteDescription({ type: 'answer', sdp: ans.sdp })
    await new Promise<void>((res, rej) => {
      const t = setTimeout(() => rej(new Error('ICE timeout (UDP blocked?)')), 4000)
      const check = () => {
        if (pc.connectionState === 'connected') {
          clearTimeout(t)
          res()
        } else if (pc.connectionState === 'failed') {
          clearTimeout(t)
          rej(new Error('ICE failed'))
        }
      }
      pc.onconnectionstatechange = () => {
        check()
        if (this.pc === pc && this.stats.transport === 'webrtc' && ['failed', 'disconnected', 'closed'].includes(pc.connectionState)) {
          this.cleanupRTC()
          if (!this.closed) this.scheduleRetry()
        }
      }
      check()
    })
    this.stats.transport = 'webrtc'
    this.retry = 0
    this.connected = true
    this.video.style.display = 'block'
    this.canvas.style.display = 'none'
    this.cb.onStatus('')
    this.startPing((m) => this.send(m))
    const statLoop = async () => {
      let lastBytes = 0
      let lastFrames = 0
      while (this.pc === pc && !this.closed) {
        await new Promise((r) => setTimeout(r, 1000))
        const st = await pc.getStats().catch(() => null)
        st?.forEach((s) => {
          if (s.type === 'inbound-rtp' && s.kind === 'video') {
            this.stats.kbps = Math.round(((s.bytesReceived - lastBytes) * 8) / 1000)
            this.stats.fps = (s.framesDecoded || 0) - lastFrames
            lastBytes = s.bytesReceived
            lastFrames = s.framesDecoded || 0
            this.stats.width = s.frameWidth || 0
            this.stats.height = s.frameHeight || 0
          }
        })
      }
    }
    statLoop()
  }

  private cleanupRTC() {
    try {
      this.pc?.close()
    } catch {}
    this.pc = undefined
    this.input = undefined
  }

  send(m: Msg) {
    const s = JSON.stringify(m)
    if (this.input && this.input.readyState === 'open') this.input.send(s)
    else if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(s)
  }

  setMode(m: Mode) {
    this.mode = m
    this.send({ t: 'mode', mode: m })
  }

  close() {
    this.closed = true
    clearInterval(this.pingTimer)
    this.cleanupRTC()
    try {
      this.ws?.close()
    } catch {}
    try {
      this.decoder?.close()
    } catch {}
    this.decoder = undefined
  }
}

const codeMap: Record<string, string> = {
  Minus: '-', Equal: '=', BracketLeft: '[', BracketRight: ']', Backslash: '\\', Semicolon: ';', Quote: "'",
  Comma: ',', Period: '.', Slash: '/', Backquote: '`', Space: 'space', IntlBackslash: '<',
}

export type KeyLike = Pick<KeyboardEvent, 'code' | 'key' | 'ctrlKey' | 'metaKey'>

export function keyName(e: KeyLike): { key?: string; text?: string } {
  const c = e.code
  if (c.startsWith('Key') && c.length === 4) return { key: c.slice(3).toLowerCase() }
  if (c.startsWith('Digit')) return { key: c.slice(5) }
  if (codeMap[c]) return { key: codeMap[c] }
  if (c.startsWith('Numpad') && /^\d$/.test(c.slice(6))) return { key: c.slice(6) }
  if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) return { text: e.key }
  return { key: e.key }
}
