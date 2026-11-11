export interface Size {
  w: number
  h: number
}

export interface Box {
  left: number
  top: number
  width: number
  height: number
}

export function deskPoint(r: Box, size: Size, clientX: number, clientY: number) {
  const scale = Math.min(r.width / size.w, r.height / size.h)
  const ox = r.left + (r.width - size.w * scale) / 2
  const oy = r.top + (r.height - size.h * scale) / 2
  return { x: Math.round((clientX - ox) / scale), y: Math.round((clientY - oy) / scale), scale, ox: ox - r.left, oy: oy - r.top }
}

export function resizedTo(e: { type: string; data?: Record<string, unknown> }): Size | null {
  if (e.type !== 'desktop.resized') return null
  const w = Number(e.data?.width)
  const h = Number(e.data?.height)
  return w > 0 && h > 0 ? { w, h } : null
}
