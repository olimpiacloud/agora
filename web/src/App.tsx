import { useCallback, useEffect, useMemo, useState } from 'react'
import { ago, api, ApiError, getToken, mintViewer, setToken, subscribe, type AgoraEvent, type Cursor, type SystemInfo, type Tab, type Workspace } from './api'
import { Viewer } from './Viewer'

function useRoute() {
  const [path, setPath] = useState(location.pathname)
  useEffect(() => {
    const f = () => setPath(location.pathname)
    window.addEventListener('popstate', f)
    return () => window.removeEventListener('popstate', f)
  }, [])
  const go = useCallback((p: string) => {
    history.pushState(null, '', p)
    setPath(p)
  }, [])
  return { path, go }
}

export function App() {
  const { path, go } = useRoute()
  if (path.startsWith('/view/')) {
    const ws = path.split('/')[2]
    const vt = new URLSearchParams(location.hash.slice(1)).get('vt') || ''
    return <StandaloneView ws={ws} vt={vt} />
  }
  return <Dashboard path={path} go={go} />
}

function StandaloneView({ ws, vt }: { ws: string; vt: string }) {
  const [token, setTok] = useState(vt)
  useEffect(() => {
    if (!vt && getToken()) mintViewer(ws).then(setTok).catch(() => {})
  }, [ws, vt])
  if (!token) return <div className="center muted">Missing viewer token</div>
  return (
    <div className="standalone">
      <Viewer ws={ws} token={token} control={true} />
    </div>
  )
}

function Login({ onOk }: { onOk: () => void }) {
  const [t, setT] = useState('')
  const [err, setErr] = useState('')
  return (
    <div className="center">
      <form
        className="login"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            await api('GET', '/v1/me', undefined, t.trim())
            setToken(t.trim())
            onOk()
          } catch (er) {
            setErr((er as Error).message)
          }
        }}
      >
        <div className="logo big">agora</div>
        <p className="muted">Paste the admin token (<code>agora token</code>).</p>
        <input type="password" value={t} onChange={(e) => setT(e.target.value)} placeholder="token" autoFocus />
        <button className="primary">Enter</button>
        {err && <div className="err">{err}</div>}
      </form>
    </div>
  )
}

function Dashboard({ path, go }: { path: string; go: (p: string) => void }) {
  const [authed, setAuthed] = useState(!!getToken())
  const [wss, setWss] = useState<Workspace[]>([])
  const [cursors, setCursors] = useState<Cursor[]>([])
  const [sys, setSys] = useState<SystemInfo | null>(null)
  const [events, setEvents] = useState<AgoraEvent[]>([])
  const [err, setErr] = useState('')

  const refresh = useCallback(async () => {
    try {
      const [w, c, s] = await Promise.all([api<Workspace[]>('GET', '/v1/workspaces'), api<Cursor[]>('GET', '/v1/cursors'), api<SystemInfo>('GET', '/v1/system')])
      setWss(w)
      setCursors(c)
      setSys(s)
      setErr('')
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setToken('')
        setAuthed(false)
      } else setErr((e as Error).message)
    }
  }, [])

  useEffect(() => {
    if (!authed) return
    refresh()
    const t = setInterval(refresh, 5000)
    let pending = false
    const unsub = subscribe({}, (e) => {
      setEvents((ev) => [e, ...ev].slice(0, 300))
      if (!pending && !e.type.startsWith('cursor.action') && e.type !== 'cursor.pointer' && e.type !== 'stream.stats') {
        pending = true
        setTimeout(() => {
          pending = false
          refresh()
        }, 400)
      }
    })
    return () => {
      clearInterval(t)
      unsub()
    }
  }, [authed, refresh])

  if (!authed) return <Login onOk={() => setAuthed(true)} />

  const seg = path.split('/').filter(Boolean)
  const view = seg[0] || 'workspaces'
  const wsId = view === 'workspaces' ? seg[1] : undefined

  return (
    <div className="app">
      <aside>
        <div className="logo" onClick={() => go('/')}>agora</div>
        <nav>
          <a className={view === 'workspaces' ? 'on' : ''} onClick={() => go('/workspaces')}>Workspaces</a>
          <a className={view === 'agents' ? 'on' : ''} onClick={() => go('/agents')}>Agents</a>
          <a className={view === 'events' ? 'on' : ''} onClick={() => go('/events')}>Events</a>
          <a className={view === 'system' ? 'on' : ''} onClick={() => go('/system')}>System</a>
        </nav>
        <div className="wslist">
          {wss.filter((w) => w.state !== 'destroyed').map((w) => (
            <a key={w.id} className={wsId === w.id ? 'on' : ''} onClick={() => go('/workspaces/' + w.id)}>
              <i className={'dot ' + w.state} />
              <span className="name">{w.name}</span>
              {w.cursors > 0 && <span className="count">{w.cursors}</span>}
            </a>
          ))}
        </div>
        <div className="foot muted">
          {sys && (
            <>
              daemon {(sys.daemon.pss_kb / 1024).toFixed(0)} MB · total {sys.total_pss_mb.toFixed(0)} MB
              <br />
              load {sys.loadavg.split(' ').slice(0, 3).join(' ')}
            </>
          )}
          <a onClick={() => { setToken(''); setAuthed(false) }}>sign out</a>
        </div>
      </aside>
      <main>
        {err && <div className="err">{err}</div>}
        {view === 'workspaces' && !wsId && <WorkspaceList wss={wss} go={go} refresh={refresh} />}
        {view === 'workspaces' && wsId && <WorkspaceView ws={wss.find((w) => w.id === wsId)} id={wsId} cursors={cursors.filter((c) => c.workspace === wsId)} events={events.filter((e) => e.workspace === wsId)} refresh={refresh} go={go} />}
        {view === 'agents' && <AgentTree cursors={cursors} wss={wss} go={go} refresh={refresh} />}
        {view === 'events' && <EventList events={events} />}
        {view === 'system' && <SystemView sys={sys} wss={wss} />}
      </main>
    </div>
  )
}

function WorkspaceList({ wss, go, refresh }: { wss: Workspace[]; go: (p: string) => void; refresh: () => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <div className="page">
      <header>
        <h1>Workspaces</h1>
        <form
          className="row"
          onSubmit={async (e) => {
            e.preventDefault()
            setBusy(true)
            try {
              const w = await api<Workspace>('POST', '/v1/workspaces', { name: name || undefined, start: true })
              setName('')
              refresh()
              go('/workspaces/' + w.id)
            } finally {
              setBusy(false)
            }
          }}
        >
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="new workspace name" />
          <button className="primary" disabled={busy}>Create</button>
        </form>
      </header>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>State</th>
            <th>Cursors</th>
            <th>Viewers</th>
            <th>Memory</th>
            <th>CPU</th>
            <th>Stream</th>
            <th>Active</th>
          </tr>
        </thead>
        <tbody>
          {wss.map((w) => (
            <tr key={w.id} onClick={() => go('/workspaces/' + w.id)} className="click">
              <td>
                <b>{w.name}</b> <span className="muted mono">{w.id}</span>
              </td>
              <td><span className={'pill ' + w.state}>{w.state}</span></td>
              <td>{w.cursors}</td>
              <td>{w.viewers}</td>
              <td className="mono">{w.usage ? w.usage.pss_mb.toFixed(0) + ' MB' : '—'}</td>
              <td className="mono">{w.usage ? w.usage.cpu_percent.toFixed(1) + '%' : '—'}</td>
              <td className="mono">{w.stream?.running ? `encoding ${w.stream.fps}fps` : 'dormant'}</td>
              <td className="muted">{ago(w.last_active)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const statusClass = (s: string) => (['done'].includes(s) ? 'ok' : ['failed', 'cancelled'].includes(s) ? 'bad' : ['needs_human', 'blocked'].includes(s) ? 'warn' : s === 'paused' ? 'muted' : 'run')

const affectsTabs = (e: AgoraEvent) => {
  if (e.type === 'cursor.action') return String(e.data?.action ?? '').startsWith('tab.')
  if (e.type === 'cursor.pointer') return false
  return e.type.startsWith('browser.tab') || e.type.startsWith('cursor.') || e.type === 'scheduler.tab_closed' || e.type === 'scheduler.browser_stopped'
}

function WorkspaceView({ ws, id, cursors, events, refresh, go }: { ws?: Workspace; id: string; cursors: Cursor[]; events: AgoraEvent[]; refresh: () => void; go: (p: string) => void }) {
  const [vt, setVt] = useState('')
  const [tabs, setTabs] = useState<Tab[]>([])
  const [show, setShow] = useState(true)
  const [tab, setTab] = useState<'agents' | 'tabs' | 'events' | 'usage'>('agents')
  const live = ws && ws.state !== 'suspended' && ws.state !== 'destroyed' && ws.state !== 'created'
  useEffect(() => {
    setVt('')
    mintViewer(id).then(setVt).catch(() => {})
  }, [id])
  const tabSeq = events.find(affectsTabs)?.seq
  useEffect(() => {
    api<Tab[]>('GET', `/v1/workspaces/${id}/tabs`).then(setTabs).catch(() => {})
  }, [id, tabSeq])
  const op = async (o: string, body?: unknown) => {
    try {
      const r = await api<{ id?: string }>('POST', `/v1/workspaces/${id}/${o}`, body ?? {})
      refresh()
      if (o === 'fork' && r?.id) go('/workspaces/' + r.id)
    } catch (e) {
      alert((e as Error).message)
    }
  }
  if (!ws) return <div className="page muted">loading…</div>
  const active = cursors.filter((c) => !['done', 'failed', 'cancelled'].includes(c.status))
  return (
    <div className="wsview">
      <header className="wshead">
        <h1>{ws.name}</h1>
        <span className={'pill ' + ws.state}>{ws.state}</span>
        <span className="muted mono">{ws.id} · {ws.display || 'no display'} · profile {ws.config.profile === ws.id ? 'dedicated' : ws.config.profile}</span>
        <div className="grow" />
        {(ws.state === 'suspended' || ws.state === 'created') && <button className="primary" onClick={() => op('resume')}>Resume</button>}
        {ws.state === 'frozen' && <button className="primary" onClick={() => op('thaw')}>Thaw</button>}
        {live && <button onClick={() => op('freeze')} title="Stop all CPU, keep RAM">Freeze</button>}
        {live && <button onClick={() => op('suspend')} title="Checkpoint, free processes">Suspend</button>}
        <button onClick={() => op('checkpoint', { snapshot: true })}>Checkpoint</button>
        <button onClick={() => op('fork')}>Fork</button>
        <select value={ws.config.layout} onChange={(e) => op('layout', { layout: e.target.value })}>
          <option value="tile">tile</option>
          <option value="max">max</option>
          <option value="float">float</option>
        </select>
        <button onClick={() => setShow(!show)}>{show ? 'Hide desktop' : 'Show desktop'}</button>
        <button
          className="danger"
          onClick={async () => {
            if (!confirm('Destroy this workspace and delete its data?')) return
            await api('DELETE', `/v1/workspaces/${id}?purge=1`)
            refresh()
            go('/workspaces')
          }}
        >
          Delete
        </button>
      </header>
      <div className="wsbody">
        <section className="desk">
          {show && vt ? (
            <Viewer ws={id} token={vt} control={true} />
          ) : (
            <div className="center muted">
              {ws.state === 'suspended' ? 'Suspended — opening the desktop will resume it.' : 'Desktop hidden: no frames are captured or encoded.'}
              {!show && <button onClick={() => setShow(true)}>Show desktop</button>}
            </div>
          )}
        </section>
        <section className="side">
          <div className="tabs">
            {(['agents', 'tabs', 'events', 'usage'] as const).map((t) => (
              <a key={t} className={tab === t ? 'on' : ''} onClick={() => setTab(t)}>
                {t}
                {t === 'agents' && active.length > 0 ? ` ${active.length}` : ''}
              </a>
            ))}
          </div>
          {tab === 'agents' && (
            <div className="list">
              {cursors.length === 0 && <div className="muted pad">No cursors yet. Agents create them through the API or MCP.</div>}
              {buildTree(cursors).map(({ c, depth }) => (
                <CursorRow key={c.id} c={c} depth={depth} refresh={refresh} />
              ))}
            </div>
          )}
          {tab === 'tabs' && (
            <div className="list">
              {tabs.map((t) => (
                <div key={t.target} className="item">
                  <div className="ellipsis"><b>{t.title || '(untitled)'}</b></div>
                  <div className="muted ellipsis mono">{t.url}</div>
                  <div className="muted">owner {cursors.find((c) => c.id === t.owner)?.name || t.owner || '—'}</div>
                </div>
              ))}
            </div>
          )}
          {tab === 'events' && <EventList events={events} compact />}
          {tab === 'usage' && <UsageView ws={ws} />}
        </section>
      </div>
    </div>
  )
}

function buildTree(cs: Cursor[]) {
  const by = new Map<string, Cursor[]>()
  const ids = new Set(cs.map((c) => c.id))
  for (const c of cs) {
    const p = c.parent && ids.has(c.parent) ? c.parent : ''
    by.set(p, [...(by.get(p) || []), c])
  }
  const out: { c: Cursor; depth: number }[] = []
  const walk = (p: string, d: number) => {
    for (const c of by.get(p) || []) {
      out.push({ c, depth: d })
      walk(c.id, d + 1)
    }
  }
  walk('', 0)
  return out
}

function CursorRow({ c, depth, refresh }: { c: Cursor; depth: number; refresh: () => void }) {
  const [open, setOpen] = useState(false)
  const terminal = ['done', 'failed', 'cancelled'].includes(c.status)
  const act = async (op: string) => {
    await api('POST', `/v1/cursors/${c.id}/${op}`, {}).catch((e) => alert(e.message))
    refresh()
  }
  const last = c.progress?.[c.progress.length - 1]
  return (
    <div className="item cursor" style={{ marginLeft: depth * 16 }}>
      <div className="row" onClick={() => setOpen(!open)}>
        <i className="swatch" style={{ background: c.color }} />
        <b className="ellipsis">{c.name}</b>
        <span className={'pill ' + statusClass(c.status)}>{c.status}</span>
        <div className="grow" />
        <span className="muted">{ago(c.updated)}</span>
      </div>
      {c.goal && <div className="muted ellipsis2">{c.goal}</div>}
      {c.detail && <div className="detail">{c.detail}</div>}
      {last && <div className="muted ellipsis">↳ {last.message}{last.percent ? ` (${last.percent}%)` : ''}</div>}
      {open && (
        <div className="more">
          <div className="mono muted">{c.id} · {c.actions} actions{c.last_action ? ` · last ${c.last_action}` : ''}{c.surface ? ` · ${c.surface}` : ''}</div>
          {c.usage && <div className="mono muted">${c.usage.cost_usd.toFixed(3)} · {c.usage.turns} turns · {Math.round((c.usage.input_tokens + c.usage.cache_read_tokens + c.usage.cache_write_tokens) / 1000)}k in · {Math.round(c.usage.output_tokens / 1000)}k out · {(c.usage.duration_ms / 1000).toFixed(0)}s</div>}
          {c.result !== undefined && c.result !== null && <pre>{JSON.stringify(c.result, null, 2).slice(0, 2000)}</pre>}
          {c.error && <pre className="err">{c.error}</pre>}
          {!terminal && (
            <div className="row">
              {c.status === 'paused' ? <button onClick={() => act('resume')}>Resume</button> : <button onClick={() => act('pause')}>Pause</button>}
              <button className="danger" onClick={() => act('cancel')}>Cancel</button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function AgentTree({ cursors, wss, go, refresh }: { cursors: Cursor[]; wss: Workspace[]; go: (p: string) => void; refresh: () => void }) {
  const [onlyActive, setOnlyActive] = useState(false)
  const list = onlyActive ? cursors.filter((c) => !['done', 'failed', 'cancelled'].includes(c.status)) : cursors
  const roots = useMemo(() => {
    const ids = new Set(list.map((c) => c.id))
    return list.filter((c) => !c.parent || !ids.has(c.parent))
  }, [list])
  const counts = list.reduce<Record<string, number>>((a, c) => ((a[c.status] = (a[c.status] || 0) + 1), a), {})
  return (
    <div className="page">
      <header>
        <h1>Agents</h1>
        <div className="row muted">
          {Object.entries(counts).map(([k, v]) => (
            <span key={k} className={'pill ' + statusClass(k)}>{k} {v}</span>
          ))}
          <label className="row"><input type="checkbox" checked={onlyActive} onChange={(e) => setOnlyActive(e.target.checked)} /> active only</label>
        </div>
      </header>
      {roots.length === 0 && <div className="muted">No agents.</div>}
      {roots.map((r) => {
        const tree = buildTree(list.filter((c) => c.root === r.root))
        const w = wss.find((x) => x.id === r.workspace)
        return (
          <div key={r.id} className="card">
            <div className="row">
              <b>{r.name}</b>
              <span className="muted">in</span>
              <a onClick={() => w && go('/workspaces/' + w.id)}>{w?.name || r.workspace || 'no workspace'}</a>
              <div className="grow" />
              <span className="muted">{tree.length} cursors</span>
            </div>
            {tree.map(({ c, depth }) => (
              <CursorRow key={c.id} c={c} depth={depth} refresh={refresh} />
            ))}
          </div>
        )
      })}
    </div>
  )
}

function EventList({ events, compact }: { events: AgoraEvent[]; compact?: boolean }) {
  const [filter, setFilter] = useState('')
  const list = events.filter((e) => !filter || e.type.includes(filter) || (e.cursor || '').includes(filter))
  return (
    <div className={compact ? 'list' : 'page'}>
      {!compact && (
        <header>
          <h1>Events</h1>
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="filter" />
        </header>
      )}
      <div className="events mono">
        {list.map((e) => (
          <div key={e.seq} className="ev">
            <span className="muted">{new Date(e.time).toLocaleTimeString()}</span> <b>{e.type}</b>
            {!compact && e.workspace && <span className="muted"> {e.workspace}</span>}
            {e.cursor && <span className="muted"> {e.cursor}</span>}
            {e.data && <span className="muted"> {JSON.stringify(e.data).slice(0, 160)}</span>}
          </div>
        ))}
      </div>
    </div>
  )
}

function UsageView({ ws }: { ws: Workspace }) {
  const [detail, setDetail] = useState<Workspace['usage']>()
  useEffect(() => {
    const f = () => api<Workspace>('GET', `/v1/workspaces/${ws.id}?detail=1`).then((w) => setDetail(w.usage)).catch(() => {})
    f()
    const t = setInterval(f, 30000)
    return () => clearInterval(t)
  }, [ws.id])
  const u = detail || ws.usage
  return (
    <div className="list pad">
      {!u && <div className="muted">No runtime (nothing is consuming resources).</div>}
      {u && (
        <>
          <div className="kv"><span>Memory (PSS)</span><b>{u.pss_mb.toFixed(0)} MB</b></div>
          <div className="kv"><span>CPU</span><b>{u.cpu_percent.toFixed(1)}%</b></div>
          <div className="kv"><span>Processes</span><b>{u.procs}</b></div>
          <h4>By role</h4>
          {Object.entries(u.pss_by_role_mb).sort((a, b) => b[1] - a[1]).map(([k, v]) => (
            <div key={k} className="kv"><span>{k}</span><span className="mono">{v.toFixed(0)} MB</span></div>
          ))}
        </>
      )}
      {ws.stream && (
        <>
          <h4>Stream</h4>
          <div className="kv"><span>State</span><b>{ws.stream.running ? 'encoding' : 'dormant'}</b></div>
          <div className="kv"><span>Viewers</span><b>{ws.viewers}</b></div>
          <div className="kv"><span>Frames</span><span className="mono">{ws.stream.frames}</span></div>
          <div className="kv"><span>Encode avg</span><span className="mono">{ws.stream.encode_ms_avg.toFixed(1)} ms</span></div>
          <div className="kv"><span>Capture avg</span><span className="mono">{ws.stream.capture_ms_avg.toFixed(1)} ms</span></div>
        </>
      )}
      <h4>Input</h4>
      <div className="kv"><span>Seats in use</span><b>{ws.seats}</b></div>
      <div className="kv"><span>Locks</span><span className="mono">{ws.locks.map((l) => `${l.surface}→${l.holder}`).join(', ') || 'none'}</span></div>
    </div>
  )
}

function SystemView({ sys, wss }: { sys: SystemInfo | null; wss: Workspace[] }) {
  if (!sys) return <div className="page muted">loading…</div>
  return (
    <div className="page">
      <header><h1>System</h1></header>
      <div className="grid">
        <div className="card"><div className="muted">Daemon</div><b className="big">{(sys.daemon.pss_kb / 1024).toFixed(1)} MB</b><div className="muted">heap {sys.heap_mb.toFixed(1)} MB · {sys.goroutines} goroutines</div></div>
        <div className="card"><div className="muted">All workspaces + daemon</div><b className="big">{sys.total_pss_mb.toFixed(0)} MB</b><div className="muted">PSS, Chromium included</div></div>
        <div className="card"><div className="muted">Workspaces</div><b className="big">{wss.length}</b><div className="muted">{Object.entries(sys.workspaces).map(([k, v]) => `${v} ${k}`).join(' · ')}</div></div>
        <div className="card"><div className="muted">Cursors</div><b className="big">{Object.values(sys.cursors).reduce((a, b) => a + b, 0)}</b><div className="muted">{Object.entries(sys.cursors).map(([k, v]) => `${v} ${k}`).join(' · ')}</div></div>
      </div>
      <p className="muted">version {sys.version} · up {sys.uptime} · cgroups {sys.cgroups ? 'on' : 'off'} · load {sys.loadavg}</p>
    </div>
  )
}
