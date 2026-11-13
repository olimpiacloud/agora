import { describe, expect, test } from 'bun:test'
import { createSeat } from '../src/seat'

const key = (code: string, k: string, ctrlKey = false) => ({ code, key: k, ctrlKey, metaKey: false })

describe('seat', () => {
  test('sends up only for buttons that are down', () => {
    const s = createSeat()
    expect(s.up(1, 2, 0)).toEqual([])
    expect(s.down(1, 2, 0)).toEqual([{ t: 'down', x: 1, y: 2, b: 0 }])
    expect(s.down(1, 2, 0)).toEqual([])
    expect(s.up(3, 4, 0)).toEqual([{ t: 'up', x: 3, y: 4, b: 0 }])
    expect(s.held()).toBe(0)
  })

  test('releases pressed buttons and held keys at the last position', () => {
    const s = createSeat()
    s.down(10, 10, 0)
    s.down(10, 10, 2)
    s.move(50, 60)
    s.key(key('ControlLeft', 'Control', true), true)
    s.key(key('KeyC', 'c', true), true)
    expect(s.releaseAll()).toEqual([
      { t: 'up', x: 50, y: 60, b: 0 },
      { t: 'up', x: 50, y: 60, b: 2 },
      { t: 'key', key: 'c', down: false },
      { t: 'key', key: 'Control', down: false },
    ])
    expect(s.releaseAll()).toEqual([])
  })

  test('lost pointer capture releases buttons but keeps keys', () => {
    const s = createSeat()
    s.down(5, 5, 0)
    s.key(key('ShiftLeft', 'Shift'), true)
    expect(s.releaseButtons()).toEqual([{ t: 'up', x: 5, y: 5, b: 0 }])
    expect(s.held()).toBe(1)
  })

  test('key up is sent only for keys sent down, text never holds', () => {
    const s = createSeat()
    expect(s.key(key('', 'é'), true)).toEqual([{ t: 'text', s: 'é' }])
    expect(s.key(key('', 'é'), false)).toEqual([])
    expect(s.key(key('KeyV', 'v'), false)).toEqual([])
    expect(s.key(key('KeyA', 'a'), true)).toEqual([{ t: 'key', key: 'a', down: true }])
    expect(s.key(key('KeyA', 'a'), false)).toEqual([{ t: 'key', key: 'a', down: false }])
  })
})
