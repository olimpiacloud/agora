import { keyName, type KeyLike } from './transport'

export type InputMsg =
  | { t: 'move'; x: number; y: number }
  | { t: 'down' | 'up'; x: number; y: number; b: number }
  | { t: 'key'; key: string; down: boolean }
  | { t: 'text'; s: string }

export function createSeat() {
  const buttons = new Set<number>()
  const keys = new Set<string>()
  let x = 0
  let y = 0
  const releaseButtons = (): InputMsg[] => {
    const out = [...buttons].map((b): InputMsg => ({ t: 'up', x, y, b }))
    buttons.clear()
    return out
  }
  const releaseKeys = (): InputMsg[] => {
    const out = [...keys].reverse().map((key): InputMsg => ({ t: 'key', key, down: false }))
    keys.clear()
    return out
  }
  return {
    move(px: number, py: number): InputMsg[] {
      x = px
      y = py
      return [{ t: 'move', x, y }]
    },
    down(px: number, py: number, b: number): InputMsg[] {
      x = px
      y = py
      if (b < 0 || buttons.has(b)) return []
      buttons.add(b)
      return [{ t: 'down', x, y, b }]
    },
    up(px: number, py: number, b: number): InputMsg[] {
      x = px
      y = py
      if (!buttons.delete(b)) return []
      return [{ t: 'up', x, y, b }]
    },
    key(e: KeyLike, down: boolean): InputMsg[] {
      const k = keyName(e)
      if (down && k.text) return [{ t: 'text', s: k.text }]
      if (!k.key || k.key.length > 32) return []
      if (down) {
        keys.add(k.key)
        return [{ t: 'key', key: k.key, down: true }]
      }
      if (!keys.delete(k.key)) return []
      return [{ t: 'key', key: k.key, down: false }]
    },
    releaseButtons,
    releaseKeys,
    releaseAll(): InputMsg[] {
      return [...releaseButtons(), ...releaseKeys()]
    },
    held(): number {
      return buttons.size + keys.size
    },
  }
}
