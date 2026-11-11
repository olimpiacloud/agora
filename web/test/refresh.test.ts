import { describe, expect, test } from 'bun:test'
import type { AgoraEvent, Cursor } from '../src/api'
import { affectsTabs, createRefresher, mergeCursors, mergeNeed, needFor, quiet, type Need } from '../src/refresh'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

const cur = (id: string, status: string) => ({ id, status }) as Cursor

const ev = (type: string, data?: Record<string, unknown>) => ({ type, data }) as AgoraEvent

describe('quiet', () => {
  test('pointer and stream statistics are quiet', () => {
    expect(quiet('cursor.pointer')).toBe(true)
    expect(quiet('stream.stats')).toBe(true)
    expect(quiet('cursor.status')).toBe(false)
  })
})

describe('needFor', () => {
  test('quiet events and actions need nothing', () => {
    expect(needFor('cursor.pointer')).toEqual({ workspaces: false, cursors: 'none' })
    expect(needFor('stream.stats')).toEqual({ workspaces: false, cursors: 'none' })
    expect(needFor('cursor.action')).toEqual({ workspaces: false, cursors: 'none' })
  })

  test('a cursor leaving the active list needs the full list', () => {
    expect(needFor('cursor.ended')).toEqual({ workspaces: true, cursors: 'all' })
    expect(needFor('cursor.deleted')).toEqual({ workspaces: true, cursors: 'all' })
  })

  test('a new or moved cursor needs workspaces and the active list', () => {
    expect(needFor('cursor.created')).toEqual({ workspaces: true, cursors: 'active' })
    expect(needFor('cursor.transferred')).toEqual({ workspaces: true, cursors: 'active' })
  })

  test('other cursor and agent events need the active list', () => {
    expect(needFor('cursor.status')).toEqual({ workspaces: false, cursors: 'active' })
    expect(needFor('cursor.progress')).toEqual({ workspaces: false, cursors: 'active' })
    expect(needFor('agent.usage')).toEqual({ workspaces: false, cursors: 'active' })
  })

  test('everything else needs workspaces', () => {
    expect(needFor('workspace.state')).toEqual({ workspaces: true, cursors: 'none' })
    expect(needFor('browser.tab.opened')).toEqual({ workspaces: true, cursors: 'none' })
    expect(needFor('scheduler.tab_closed')).toEqual({ workspaces: true, cursors: 'none' })
  })
})

describe('mergeNeed', () => {
  test('takes the union', () => {
    expect(mergeNeed({ workspaces: false, cursors: 'active' }, { workspaces: true, cursors: 'none' })).toEqual({ workspaces: true, cursors: 'active' })
  })

  test('all wins over active in either order', () => {
    expect(mergeNeed({ workspaces: false, cursors: 'all' }, { workspaces: false, cursors: 'active' }).cursors).toBe('all')
    expect(mergeNeed({ workspaces: false, cursors: 'active' }, { workspaces: false, cursors: 'all' }).cursors).toBe('all')
  })
})

describe('mergeCursors', () => {
  test('replaces active cursors, keeps finished ones and appends new ones', () => {
    const prev = [cur('a', 'running'), cur('b', 'done'), cur('c', 'running')]
    const active = [cur('a', 'thinking'), cur('d', 'running')]
    expect(mergeCursors(prev, active)).toEqual([cur('a', 'thinking'), cur('b', 'done'), cur('c', 'running'), cur('d', 'running')])
  })
})

describe('affectsTabs', () => {
  test('tab actions, tab events and ownership changes affect tabs', () => {
    expect(affectsTabs(ev('cursor.action', { action: 'tab.click' }))).toBe(true)
    expect(affectsTabs(ev('browser.tab.opened'))).toBe(true)
    expect(affectsTabs(ev('cursor.ended'))).toBe(true)
    expect(affectsTabs(ev('cursor.transferred'))).toBe(true)
    expect(affectsTabs(ev('scheduler.tab_closed'))).toBe(true)
  })

  test('other events do not', () => {
    expect(affectsTabs(ev('cursor.action', { action: 'desktop.click' }))).toBe(false)
    expect(affectsTabs(ev('cursor.progress'))).toBe(false)
    expect(affectsTabs(ev('cursor.status'))).toBe(false)
    expect(affectsTabs(ev('cursor.pointer'))).toBe(false)
    expect(affectsTabs(ev('workspace.state'))).toBe(false)
  })
})

describe('createRefresher', () => {
  const setup = () => {
    const runs: Need[] = []
    const r = createRefresher((n) => runs.push(n), 20)
    return { runs, r }
  }

  test('a burst of progress, pointer and action events makes one request for active cursors', async () => {
    const { runs, r } = setup()
    for (let i = 0; i < 50; i++) r.push('cursor.progress')
    for (let i = 0; i < 50; i++) r.push('cursor.pointer')
    for (let i = 0; i < 10; i++) r.push('cursor.action')
    await sleep(60)
    expect(runs).toEqual([{ workspaces: false, cursors: 'active' }])
  })

  test('quiet events make no request', async () => {
    const { runs, r } = setup()
    r.push('cursor.pointer')
    r.push('stream.stats')
    await sleep(60)
    expect(runs).toEqual([])
  })

  test('needs in one window are merged', async () => {
    const { runs, r } = setup()
    r.push('cursor.status')
    r.push('workspace.state')
    await sleep(60)
    expect(runs).toEqual([{ workspaces: true, cursors: 'active' }])
  })

  test('an ended cursor upgrades the window to the full list', async () => {
    const { runs, r } = setup()
    r.push('cursor.progress')
    r.push('cursor.ended')
    await sleep(60)
    expect(runs).toEqual([{ workspaces: true, cursors: 'all' }])
  })

  test('separate bursts make separate requests', async () => {
    const { runs, r } = setup()
    r.push('cursor.progress')
    await sleep(60)
    r.push('cursor.progress')
    await sleep(60)
    expect(runs.length).toBe(2)
  })

  test('cancel drops the pending request', async () => {
    const { runs, r } = setup()
    r.push('workspace.state')
    r.cancel()
    await sleep(60)
    expect(runs).toEqual([])
  })
})
