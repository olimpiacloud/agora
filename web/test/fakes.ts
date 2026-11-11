import { DesktopSession } from '../src/transport'

type Handler = ((ev: { data?: unknown }) => void) | null

export class FakeSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  static all: FakeSocket[] = []
  readyState = 0
  binaryType = ''
  sent: string[] = []
  onopen: Handler = null
  onmessage: Handler = null
  onclose: Handler = null
  constructor(public url: string) {
    FakeSocket.all.push(this)
  }
  open() {
    this.readyState = 1
    this.onopen?.({})
  }
  text(m: unknown) {
    this.onmessage?.({ data: JSON.stringify(m) })
  }
  send(s: string) {
    this.sent.push(s)
  }
  close() {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.({})
  }
}

export class FakeDecoder {
  state = 'configured'
  decodeQueueSize = 0
  constructor(_init?: unknown) {}
  configure() {}
  decode() {}
  close() {
    this.state = 'closed'
  }
}

export function installFakes(): () => void {
  const g = globalThis as Record<string, unknown>
  const values: Record<string, unknown> = {
    WebSocket: FakeSocket,
    VideoDecoder: FakeDecoder,
    location: { protocol: 'http:', host: 'agora.test' },
  }
  if (typeof window === 'undefined') values.window = globalThis
  const saved = Object.keys(values).map((name) => ({ name, desc: Object.getOwnPropertyDescriptor(g, name) }))
  for (const [name, value] of Object.entries(values)) {
    Object.defineProperty(g, name, { value, configurable: true, writable: true })
  }
  return () => {
    for (const { name, desc } of saved) {
      if (desc) Object.defineProperty(g, name, desc)
      else delete g[name]
    }
    FakeSocket.all.length = 0
  }
}

export function makeSession(prefer: 'ws' | 'webrtc' | 'auto' = 'ws') {
  const log: string[] = []
  const canvas = { style: { display: '' }, width: 0, height: 0, getContext: () => ({ drawImage() {} }) } as unknown as HTMLCanvasElement
  const video = { style: { display: '' }, srcObject: null, play: async () => {} } as unknown as HTMLVideoElement
  const s = new DesktopSession(
    'ws_1',
    'tok',
    canvas,
    video,
    {
      onMessage: (m) => log.push('msg:' + String(m.type)),
      onStatus: (t) => log.push('status:' + t),
      onSize: (w, h) => log.push(`size:${w}x${h}`),
    },
    prefer,
    'interactive',
  )
  return { s, log }
}
