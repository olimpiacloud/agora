import { describe, expect, test } from 'bun:test'
import { deskPoint, resizedTo } from '../src/desk'

describe('deskPoint', () => {
  test('maps an element that shows the desktop at its own size', () => {
    const r = { left: 100, top: 50, width: 1280, height: 800 }
    const size = { w: 1280, h: 800 }
    expect(deskPoint(r, size, 100, 50)).toMatchObject({ x: 0, y: 0 })
    expect(deskPoint(r, size, 1380, 850)).toMatchObject({ x: 1280, y: 800 })
  })

  test('accounts for letterboxing', () => {
    const p = deskPoint({ left: 0, top: 0, width: 1000, height: 1000 }, { w: 1000, h: 500 }, 500, 500)
    expect(p).toMatchObject({ x: 500, y: 250, oy: 250 })
  })
})

describe('resizedTo', () => {
  test('reads the new size from desktop.resized', () => {
    expect(resizedTo({ type: 'desktop.resized', data: { width: 1920, height: 1080 } })).toEqual({ w: 1920, h: 1080 })
  })

  test('ignores other events and missing or zero sizes', () => {
    expect(resizedTo({ type: 'cursor.pointer', data: { width: 1920, height: 1080 } })).toBeNull()
    expect(resizedTo({ type: 'desktop.resized' })).toBeNull()
    expect(resizedTo({ type: 'desktop.resized', data: { width: 0, height: 1080 } })).toBeNull()
  })
})
