import { useEffect, useRef, useState } from 'react'
import { api, getToken, mintViewer, type Cursor, type Lock } from './api'
import { useSmoothPoint } from './smooth'
import { DesktopSession, keyName, type Mode, type Transport } from './transport'

interface Overlay {
  id: string
  name: string
  color: string
  x: number
  y: number
  action?: string
  status?: string
  t: number
}

interface Props {
  ws: string
  token: string
  control: boolean
  onEvent?: (e: { type: string; cursor?: string; data?: Record<string, unknown> }) => void
  compact?: boolean
}

export function Viewer({ ws, token, control, onEvent, compact }: Props) {
  const wrap = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const video = useRef<HTMLVideoElement>(null)
  const sess = useRef<DesktopSession | null>(null)
  const [size, setSize] = useState({ w: 1280, h: 800 })
  const [status, setStatus] = useState('connecting…')
  const [overlays, setOverlays] = useState<Record<string, Overlay>>({})
  const [locks, setLocks] = useState<Lock[]>([])
  const [viewerId, setViewerId] = useState('')
  const [transport, setTransport] = useState<Transport | 'auto'>((localStorage.getItem('agora.transport') as Transport) || 'auto')
  const [mode, setMode] = useState<Mode>('interactive')
  const [stats, setStats] = useState(sessStatsInit)
  const [needHuman, setNeedHuman] = useState<{ cursor?: string; reason: string } | null>(null)
  const [focused, setFocused] = useState(false)
  const lastMove = useRef(0)
  const wheelAcc = useRef(0)
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent

  useEffect(() => {
    if (!canvas.current || !video.current || !token) return
    const s = new DesktopSession(ws, token, canvas.current, video.current, {
      onStatus: setStatus,
      onSize: (w, h) => setSize({ w, h }),
      onMessage: (m) => {
        if (m.type === 'hello') {
          setViewerId(m.viewer as string)
          setLocks((m.locks as Lock[]) || [])
          const o: Record<string, Overlay> = {}
          for (const c of (m.cursors as Cursor[]) || []) {
            if (c.x || c.y) o[c.id] = { id: c.id, name: c.name, color: c.color, x: c.x, y: c.y, status: c.status, t: 0 }
            if (c.status === 'needs_human') setNeedHuman({ cursor: c.id, reason: c.detail || 'needs a human' })
          }
          setOverlays(o)
        } else if (m.type === 'event') {
          const e = m.event as { type: string; cursor?: string; data?: Record<string, unknown> }
          onEventRef.current?.(e)
          const d = e.data || {}
          if (e.type === 'cursor.pointer' && e.cursor && typeof d.x === 'number' && (d.x as number) >= 0) {
            setOverlays((o) => ({ ...o, [e.cursor!]: { ...(o[e.cursor!] || {}), id: e.cursor!, name: d.name as string, color: d.color as string, x: d.x as number, y: d.y as number, action: d.action as string, t: Date.now() } }))
          } else if (e.type === 'cursor.status' && e.cursor) {
            const to = d.to as string
            setOverlays((o) => (o[e.cursor!] ? { ...o, [e.cursor!]: { ...o[e.cursor!], status: to } } : o))
            if (to === 'needs_human') setNeedHuman({ cursor: e.cursor, reason: (d.detail as string) || 'needs a human' })
            else setNeedHuman((n) => (n?.cursor === e.cursor ? null : n))
          } else if (e.type === 'cursor.ended' && e.cursor) {
            setOverlays((o) => {
              const n = { ...o }
              delete n[e.cursor!]
              return n
            })
          } else if (e.type === 'human.requested') {
            setNeedHuman({ cursor: e.cursor, reason: (d.reason as string) || 'needs a human' })
          } else if (e.type.startsWith('control.')) {
            api<Lock[]>('GET', `/v1/workspaces/${ws}/locks`, undefined, getToken() || token)
              .then((l) => l && setLocks(l))
              .catch(() => {})
            if (e.type === 'control.taken') setLocks((l) => [...l.filter((x) => x.surface !== 'workspace'), { surface: 'workspace', holder: 'human:' + ((d.viewer as string) || ''), priority: 100 }])
            if (e.type === 'control.returned') setLocks((l) => l.filter((x) => x.surface !== 'workspace'))
          }
        } else if (m.type === 'clipboard') {
          navigator.clipboard?.writeText((m.text as string) || '').catch(() => {})
        } else if (m.type === 'bye') {
          setStatus('session ended: ' + m.reason)
        }
      },
    }, transport, mode, getToken() ? () => mintViewer(ws, control) : undefined)
    sess.current = s
    s.start()
    const t = setInterval(() => setStats({ ...s.stats }), 1000)
    return () => {
      clearInterval(t)
      s.close()
      sess.current = null
    }
  }, [ws, token, transport])

  const toDesk = (e: { clientX: number; clientY: number }) => {
    const el = (video.current?.style.display === 'block' ? video.current : canvas.current)!
    const r = el.getBoundingClientRect()
    const scale = Math.min(r.width / size.w, r.height / size.h)
    const ox = r.left + (r.width - size.w * scale) / 2
    const oy = r.top + (r.height - size.h * scale) / 2
    return { x: Math.round((e.clientX - ox) / scale), y: Math.round((e.clientY - oy) / scale), scale, ox: ox - r.left, oy: oy - r.top }
  }

  const geo = () => {
    const el = wrap.current
    if (!el) return { scale: 1, ox: 0, oy: 0 }
    const r = el.getBoundingClientRect()
    const scale = Math.min(r.width / size.w, r.height / size.h)
    return { scale, ox: (r.width - size.w * scale) / 2, oy: (r.height - size.h * scale) / 2 }
  }

  const send = (m: Record<string, unknown>) => control && sess.current?.send(m)

  const iHold = locks.some((l) => l.surface === 'workspace' && l.holder === 'human:' + viewerId && !l.expires)
  const otherHold = locks.find((l) => l.surface === 'workspace' && l.holder !== 'human:' + viewerId && !l.expires)

  const g = geo()

  return (
    <div className={'viewer' + (compact ? ' compact' : '')}>
      {!compact && (
        <div className="vbar">
          <span className={'pill ' + (stats.transport === 'connecting' ? 'warn' : 'ok')}>{stats.transport}</span>
          <span className="muted mono">
            {stats.fps}fps · {stats.kbps}kbps · {stats.rtt}ms · {size.w}×{size.h}
          </span>
          <div className="grow" />
          <select value={mode} onChange={(e) => { const m = e.target.value as Mode; setMode(m); sess.current?.setMode(m) }}>
            <option value="interactive">interactive</option>
            <option value="observation">observation</option>
          </select>
          <select value={transport} onChange={(e) => { const t = e.target.value as Transport | 'auto'; localStorage.setItem('agora.transport', t); setTransport(t) }}>
            <option value="auto">auto</option>
            <option value="webrtc">webrtc</option>
            <option value="ws">websocket</option>
          </select>
          {control && (
            <>
              <button onClick={() => send({ t: 'clipget' })} title="Copy remote clipboard to local">⇩ clip</button>
              <button
                onClick={async () => {
                  const t = await navigator.clipboard?.readText().catch(() => '')
                  if (t) send({ t: 'clip', s: t })
                }}
                title="Send local clipboard to remote"
              >
                ⇧ clip
              </button>
              <select
                value=""
                onChange={(e) => {
                  const [w, h] = e.target.value.split('x').map(Number)
                  if (w) send({ t: 'resize', w, h })
                }}
              >
                <option value="">resolution</option>
                {['1024x768', '1280x800', '1366x768', '1440x900', '1600x1000', '1920x1080', '1920x1200'].map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
              {iHold ? (
                <button className="primary" onClick={() => send({ t: 'release' })}>Return control to agents</button>
              ) : (
                <button className="danger" onClick={() => send({ t: 'take' })}>Take control</button>
              )}
            </>
          )}
        </div>
      )}
      {needHuman && control && !iHold && (
        <div className="banner">
          <b>Human needed</b> — {overlays[needHuman.cursor || '']?.name || needHuman.cursor || 'agent'}: {needHuman.reason}
          <button className="danger" onClick={() => send({ t: 'take' })}>Take control</button>
        </div>
      )}
      {iHold && !compact && <div className="banner mine">You control this workspace. Agents are paused on input until you return control.</div>}
      {otherHold && !iHold && !compact && <div className="banner">Controlled by {otherHold.holder}</div>}
      <div
        ref={wrap}
        className={'screen' + (focused ? ' focused' : '')}
        tabIndex={0}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onContextMenu={(e) => e.preventDefault()}
        onPointerMove={(e) => {
          const now = performance.now()
          if (now - lastMove.current < 12) return
          lastMove.current = now
          const p = toDesk(e)
          send({ t: 'move', x: p.x, y: p.y })
        }}
        onPointerDown={(e) => {
          wrap.current?.focus()
          ;(e.target as Element).setPointerCapture?.(e.pointerId)
          const p = toDesk(e)
          send({ t: 'down', x: p.x, y: p.y, b: e.button })
        }}
        onPointerUp={(e) => {
          const p = toDesk(e)
          send({ t: 'up', x: p.x, y: p.y, b: e.button })
        }}
        onPointerCancel={(e) => {
          const p = toDesk(e)
          send({ t: 'up', x: p.x, y: p.y, b: e.button < 0 ? 0 : e.button })
        }}
        onWheel={(e) => {
          wheelAcc.current += e.deltaMode === 1 ? e.deltaY * 40 : e.deltaY
          const p = toDesk(e)
          while (Math.abs(wheelAcc.current) >= 60) {
            const d = Math.sign(wheelAcc.current)
            send({ t: 'wheel', x: p.x, y: p.y, dy: d })
            wheelAcc.current -= d * 60
          }
          if (e.deltaX) send({ t: 'wheel', x: p.x, y: p.y, dx: Math.sign(e.deltaX) })
        }}
        onKeyDown={(e) => {
          if (!control) return
          const k = keyName(e.nativeEvent)
          if ((e.ctrlKey || e.metaKey) && e.code === 'KeyV') {
            e.preventDefault()
            navigator.clipboard?.readText().then((t) => {
              if (t) send({ t: 'clip', s: t })
              setTimeout(() => {
                send({ t: 'key', key: 'ctrl', down: true })
                send({ t: 'key', key: 'v', down: true })
                send({ t: 'key', key: 'v', down: false })
                send({ t: 'key', key: 'ctrl', down: false })
              }, 60)
            }).catch(() => {})
            return
          }
          e.preventDefault()
          if (k.text) send({ t: 'text', s: k.text })
          else send({ t: 'key', key: k.key, down: true })
        }}
        onKeyUp={(e) => {
          if (!control) return
          e.preventDefault()
          const k = keyName(e.nativeEvent)
          if (k.key) send({ t: 'key', key: k.key, down: false })
        }}
      >
        <canvas ref={canvas} />
        <video ref={video} muted playsInline autoPlay />
        {Object.values(overlays).map((o) => (
          <AgentCursor key={o.id} o={o} g={g} />
        ))}
        {status && (
          <div className="status">
            {status}
            {status === 'suspended' && (
              <button className="primary" onPointerDown={(e) => e.stopPropagation()} onClick={() => sess.current?.wake()}>
                Resume
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function AgentCursor({ o, g }: { o: Overlay; g: { scale: number; ox: number; oy: number } }) {
  const [x, y] = useSmoothPoint(o.x, o.y)
  return (
    <div
      className={'agent-cursor' + (Date.now() - o.t < 600 ? ' active' : '')}
      style={{ left: g.ox + x * g.scale, top: g.oy + y * g.scale, ['--c' as string]: o.color }}
    >
      <svg width="18" height="22" viewBox="0 0 18 22">
        <path d="M1 1 L1 17 L5.5 12.8 L8.6 20 L11.4 18.8 L8.4 11.8 L14.5 11.8 Z" fill="var(--c)" stroke="#0b0d10" strokeWidth="1.4" strokeLinejoin="round" />
      </svg>
      <span>
        {o.name}
        {o.status ? ` · ${o.status}` : ''}
      </span>
    </div>
  )
}

const sessStatsInit = { transport: 'connecting' as Transport | 'connecting', fps: 0, kbps: 0, rtt: 0, width: 0, height: 0 }
