import { afterAll, beforeEach, describe, expect, test } from 'bun:test'
import { keyName } from '../src/transport'
import { FakeSocket, installFakes, makeSession } from './fakes'

const restore = installFakes()

afterAll(() => restore())

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

const key = (code: string, k: string, ctrlKey = false) => ({ code, key: k, ctrlKey, metaKey: false }) as KeyboardEvent

describe('keyName', () => {
  test('maps letters and digits by physical code', () => {
    expect(keyName(key('KeyA', 'a'))).toEqual({ key: 'a' })
    expect(keyName(key('KeyQ', 'a'))).toEqual({ key: 'q' })
    expect(keyName(key('Digit1', '!'))).toEqual({ key: '1' })
    expect(keyName(key('Numpad7', '7'))).toEqual({ key: '7' })
  })

  test('maps punctuation codes', () => {
    expect(keyName(key('Slash', '/'))).toEqual({ key: '/' })
    expect(keyName(key('Space', ' '))).toEqual({ key: 'space' })
  })

  test('falls back to text for unmapped printable keys and to the key name otherwise', () => {
    expect(keyName(key('', 'é'))).toEqual({ text: 'é' })
    expect(keyName(key('', 'é', true))).toEqual({ key: 'é' })
    expect(keyName(key('Enter', 'Enter'))).toEqual({ key: 'Enter' })
  })
})

describe('DesktopSession over WebSocket', () => {
  beforeEach(() => {
    FakeSocket.all.length = 0
  })

  test('connects without wake=0 and clears the status when the socket opens', async () => {
    const { s, log } = makeSession()
    await s.start()
    expect(FakeSocket.all.length).toBe(1)
    const sock = FakeSocket.all[0]
    expect(sock.url).toContain('/v1/workspaces/ws_1/stream/ws?mode=interactive&token=tok')
    expect(sock.url).not.toContain('wake=0')
    sock.open()
    expect(log[log.length - 1]).toBe('status:')
    s.close()
  })

  test('hello reports the size and makes the next reconnect passive', async () => {
    const { s, log } = makeSession()
    await s.start()
    const sock = FakeSocket.all[0]
    sock.open()
    sock.text({ type: 'hello', viewer: 'v1', width: 1280, height: 800 })
    const i = log.indexOf('size:1280x800')
    expect(i).toBeGreaterThanOrEqual(0)
    expect(log[i + 1]).toBe('msg:hello')
    sock.close()
    expect(log[log.length - 1]).toMatch(/^status:reconnecting in \d+s$/)
    await sleep(700)
    expect(FakeSocket.all.length).toBe(2)
    expect(FakeSocket.all[1].url).toContain('&wake=0')
    s.close()
  })

  test('a suspended bye followed by a close stops retrying, and wake() reconnects actively', async () => {
    const { s, log } = makeSession()
    await s.start()
    const sock = FakeSocket.all[0]
    sock.open()
    sock.text({ type: 'hello', viewer: 'v1', width: 1280, height: 800 })
    sock.text({ type: 'bye', reason: 'suspended' })
    sock.close()
    expect(log).toContain('msg:bye')
    expect(log[log.length - 1]).toBe('status:suspended')
    await sleep(700)
    expect(FakeSocket.all.length).toBe(1)
    s.wake()
    expect(log).toContain('status:resuming…')
    expect(FakeSocket.all.length).toBe(2)
    expect(FakeSocket.all[1].url).not.toContain('wake=0')
    s.close()
  })

  test('send writes JSON to the open socket', async () => {
    const { s } = makeSession()
    await s.start()
    const sock = FakeSocket.all[0]
    sock.open()
    s.send({ t: 'noop' })
    expect(sock.sent).toContain('{"t":"noop"}')
    s.close()
  })

  test('close() stops everything', async () => {
    const { s, log } = makeSession()
    await s.start()
    const sock = FakeSocket.all[0]
    sock.open()
    const n = log.length
    s.close()
    expect(sock.readyState).toBe(3)
    expect(log.slice(n).some((e) => e.startsWith('status:'))).toBe(false)
    await sleep(700)
    expect(FakeSocket.all.length).toBe(1)
  })
})
