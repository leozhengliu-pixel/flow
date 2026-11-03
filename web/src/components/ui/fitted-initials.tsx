import { useLayoutEffect, useRef, type ReactNode } from 'react'

export function FittedInitials({ children, className, wide }: { children: ReactNode; className: string; wide?: boolean }) {
  const ref = useRef<HTMLSpanElement>(null)
  useLayoutEffect(() => {
    const text = ref.current, slot = text?.parentElement
    if (!text || !slot || typeof Range.prototype.getBoundingClientRect !== 'function') return
    let disposed = false
    const fit = () => {
      if (disposed) return
      text.style.removeProperty('transform')
      const range = document.createRange()
      range.selectNodeContents(text)
      const bounds = range.getBoundingClientRect()
      // Compare in one coordinate space while a containing menu is animating.
      const slotBounds = slot.getBoundingClientRect()
      const width = slotBounds.width * slot.clientWidth / slot.offsetWidth
      const height = slotBounds.height * slot.clientHeight / slot.offsetHeight
      const scale = Math.min(1, width / bounds.width, height / bounds.height)
      if (Number.isFinite(scale) && scale > 0 && scale < 1) text.style.transform = `scale(${scale})`
    }
    fit()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(fit)
    observer?.observe(slot)
    observer?.observe(text)
    void document.fonts?.ready.then(fit)
    return () => { disposed = true; observer?.disconnect() }
  }, [children])
  return <span ref={ref} className={className} data-wide={wide ? 'true' : undefined}>{children}</span>
}
