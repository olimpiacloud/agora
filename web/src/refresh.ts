import type { AgoraEvent, Cursor } from './api'

export type CursorFetch = 'none' | 'active' | 'all'

export interface Need {
  workspaces: boolean
  cursors: CursorFetch
}

export const FULL: Need = { workspaces: true, cursors: 'all' }

const QUIET = new Set(['cursor.pointer', 'stream.stats'])
const TREE = new Set(['cursor.created', 'cursor.ended', 'cursor.deleted', 'cursor.transferred'])
const RANK: Record<CursorFetch, number> = { none: 0, active: 1, all: 2 }

export function quiet(type: string): boolean {
  return QUIET.has(type)
}

export function needFor(type: string): Need {
  if (quiet(type) || type === 'cursor.action') return { workspaces: false, cursors: 'none' }
  if (type === 'cursor.ended' || type === 'cursor.deleted') return { workspaces: true, cursors: 'all' }
  if (TREE.has(type)) return { workspaces: true, cursors: 'active' }
  if (type.startsWith('cursor.') || type.startsWith('agent.')) return { workspaces: false, cursors: 'active' }
  return { workspaces: true, cursors: 'none' }
}

export function mergeNeed(a: Need, b: Need): Need {
  return { workspaces: a.workspaces || b.workspaces, cursors: RANK[a.cursors] >= RANK[b.cursors] ? a.cursors : b.cursors }
}

export function mergeCursors(prev: Cursor[], active: Cursor[]): Cursor[] {
  const fresh = new Map(active.map((c) => [c.id, c]))
  const known = new Set(prev.map((c) => c.id))
  return [...prev.map((c) => fresh.get(c.id) ?? c), ...active.filter((c) => !known.has(c.id))]
}

export function affectsTabs(e: AgoraEvent): boolean {
  if (e.type === 'cursor.action') return String(e.data?.action ?? '').startsWith('tab.')
  return e.type.startsWith('browser.tab') || TREE.has(e.type) || e.type === 'scheduler.tab_closed' || e.type === 'scheduler.browser_stopped'
}

export function createRefresher(run: (need: Need) => void, delayMs = 400) {
  let pending: Need | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  return {
    push(type: string) {
      const n = needFor(type)
      if (!n.workspaces && n.cursors === 'none') return
      pending = pending ? mergeNeed(pending, n) : n
      if (timer) return
      timer = setTimeout(() => {
        timer = null
        const p = pending
        pending = null
        if (p) run(p)
      }, delayMs)
    },
    cancel() {
      if (timer) clearTimeout(timer)
      timer = null
      pending = null
    },
  }
}
