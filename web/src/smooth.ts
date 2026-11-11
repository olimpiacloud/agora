import { PerfectCursor } from 'perfect-cursors'
import { useEffect, useRef, useState } from 'react'

export function useSmoothPoint(x: number, y: number): [number, number] {
  const [p, setP] = useState<[number, number]>([x, y])
  const pc = useRef<PerfectCursor | null>(null)
  useEffect(() => {
    const c = new PerfectCursor((q) => setP([q[0], q[1]]))
    pc.current = c
    return () => {
      c.dispose()
      cancelAnimationFrame(c.lastRequestId)
      pc.current = null
    }
  }, [])
  useEffect(() => {
    pc.current?.addPoint([x, y])
  }, [x, y])
  return p
}
