import { useCallback, useLayoutEffect, useRef, useState, type RefObject } from 'react'

const STEP = 128

/**
 * One layout read per frame, with stable windows during small scrolls. `measured` turns true in the
 * mount layout effect, before the first paint, so callers can skip windowed content on the first
 * (unmeasured) render instead of building and styling it for a guessed viewport and then again.
 */
export function useTimelineViewport(ref: RefObject<HTMLElement | null>) {
  const [viewport, setViewport] = useState({ left: 0, top: 0, width: 1600, height: 900 })
  const [measured, setMeasured] = useState(false)
  const frame = useRef(0)
  const measure = useCallback(() => {
    const node = ref.current
    if (!node) return
    const { clientWidth: width, clientHeight: height, scrollLeft, scrollTop } = node
    if (!width || !height) return
    const next = {
      left: Math.max(0, Math.floor(scrollLeft / STEP) * STEP),
      top: Math.max(0, Math.floor(scrollTop / STEP) * STEP),
      width,
      height,
    }
    setViewport(current => current.left === next.left && current.top === next.top && current.width === next.width && current.height === next.height ? current : next)
  }, [ref])

  useLayoutEffect(() => {
    const node = ref.current
    if (!node) return
    const schedule = () => {
      if (!frame.current) frame.current = requestAnimationFrame(() => {
        frame.current = 0
        measure()
      })
    }
    measure()
    setMeasured(true)
    node.addEventListener('scroll', schedule, { passive: true })
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(schedule)
    observer?.observe(node)
    return () => {
      node.removeEventListener('scroll', schedule)
      observer?.disconnect()
      cancelAnimationFrame(frame.current)
      frame.current = 0
    }
  }, [measure, ref])

  return { viewport, measure, measured }
}
