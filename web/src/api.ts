export type WSState = 'created' | 'active' | 'idle' | 'frozen' | 'suspended' | 'destroyed'

export interface Usage {
  pss_mb: number
  rss_mb: number
  cpu_percent: number
  procs: number
  pss_by_role_mb: Record<string, number>
  top: { pid: number; comm: string; role: string; pss_kb: number; cpu_percent: number }[]
}

export interface StreamStats {
  running: boolean
  sinks: number
  frames: number
  bytes: number
  fps: number
  encode_ms_avg: number
  capture_ms_avg: number
  mode: string
  starts: number
}

export interface Lock {
  surface: string
  holder: string
  priority: number
  reason?: string
  expires?: string
}

export interface Workspace {
  id: string
  name: string
  state: WSState
  config: { width: number; height: number; profile: string; layout: string }
  created: string
  last_active: string
  display?: string
  browser: boolean
  viewers: number
  seats: number
  processes: number
  cursors: number
  stream?: StreamStats
  usage?: Usage
  locks: Lock[]
  fork_of?: string
}

export interface Cursor {
  id: string
  name: string
  kind: string
  parent?: string
  root: string
  depth: number
  goal?: string
  status: string
  detail?: string
  workspace?: string
  surface?: string
  color: string
  x: number
  y: number
  result?: unknown
  error?: string
  progress?: { time: string; message: string; percent?: number }[]
  actions: number
  last_action?: string
  created: string
  updated: string
  ended?: string
  worktree?: string
  usage?: { input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_write_tokens: number; cost_usd: number; turns: number; duration_ms: number }
}

export interface AgoraEvent {
  seq: number
  time: string
  type: string
  workspace?: string
  cursor?: string
  data?: Record<string, unknown>
}

export interface Tab {
  target: string
  url: string
  title: string
  owner: string
}

export interface SystemInfo {
  version: string
  uptime: string
  daemon: { pss_kb: number; rss_kb: number }
  goroutines: number
  heap_mb: number
  workspaces: Record<string, number>
  cursors: Record<string, number>
  total_pss_mb: number
  cgroups: boolean
  loadavg: string
}

const KEY = 'agora.token'

export function getToken(): string {
  return localStorage.getItem(KEY) || ''
}

export function setToken(t: string) {
  if (t) localStorage.setItem(KEY, t)
  else localStorage.removeItem(KEY)
}

export class ApiError extends Error {
  status: number
  constructor(status: number, msg: string) {
    super(msg)
    this.status = status
  }
}

export async function api<T = unknown>(method: string, path: string, body?: unknown, token = getToken()): Promise<T> {
  const r = await fetch(path, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await r.text()
  if (!r.ok) {
    let msg = text
    try {
      msg = JSON.parse(text).error || text
    } catch {}
    throw new ApiError(r.status, msg)
  }
  return (text ? JSON.parse(text) : undefined) as T
}

export async function mintViewer(ws: string, control = true): Promise<string> {
  const r = await api<{ token: string }>('POST', '/v1/viewer-tokens', { workspace: ws, control, ttl_seconds: 3600 })
  return r.token
}

export function subscribe(params: Record<string, string>, onEvent: (e: AgoraEvent) => void, token = getToken()): () => void {
  const ctl = new AbortController()
  const qs = new URLSearchParams(params).toString()
  let stopped = false
  const run = async () => {
    while (!stopped) {
      try {
        const r = await fetch(`/v1/events/stream?${qs}`, { headers: { Authorization: `Bearer ${token}` }, signal: ctl.signal })
        if (!r.ok || !r.body) throw new Error('events ' + r.status)
        const reader = r.body.getReader()
        const dec = new TextDecoder()
        let buf = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += dec.decode(value, { stream: true })
          let i
          while ((i = buf.indexOf('\n\n')) >= 0) {
            const chunk = buf.slice(0, i)
            buf = buf.slice(i + 2)
            const line = chunk.split('\n').find((l) => l.startsWith('data: '))
            if (line) {
              try {
                onEvent(JSON.parse(line.slice(6)))
              } catch {}
            }
          }
        }
      } catch {
        if (stopped) return
      }
      await new Promise((r) => setTimeout(r, 1500))
    }
  }
  run()
  return () => {
    stopped = true
    ctl.abort()
  }
}

export function ago(iso?: string): string {
  if (!iso) return ''
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 60) return `${Math.max(0, Math.floor(s))}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}
