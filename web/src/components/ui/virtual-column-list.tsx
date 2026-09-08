import { Fragment, useCallback, useLayoutEffect, useRef, useState, type Key, type ReactNode } from 'react'
import { Virtuoso } from 'react-virtuoso'

/** Keep column headings aligned with the actual scroll viewport, including its gutter. */
export function VirtualColumnList<T>({ header, className, scrollerClassName, data, computeItemKey, itemContent, increaseViewportBy, ariaLabel, virtualize = true }: {
  header: ReactNode
  className?: string
  scrollerClassName?: string
  data: readonly T[]
  computeItemKey: (index: number, item: T) => Key
  itemContent: (index: number, item: T) => ReactNode
  increaseViewportBy?: number | { top: number; bottom: number }
  ariaLabel?: string
  virtualize?: boolean
}) {
  const headerOuter = useRef<HTMLDivElement>(null)
  const headerViewport = useRef<HTMLDivElement>(null)
  const headerExtent = useRef<HTMLDivElement>(null)
  const [scroller, setScroller] = useState<HTMLElement | null>(null)
  const attachScroller = useCallback((element: HTMLElement | Window | null) => setScroller(element instanceof HTMLElement ? element : null), [])
  useLayoutEffect(() => {
    if (!scroller) return
    let syncedHeaderLeft = 0
    const syncScroll = () => {
      // Empty header cells do not always contribute the same overflow as row controls.
      if (headerExtent.current) headerExtent.current.style.width = `${scroller.scrollWidth}px`
      if (headerViewport.current) {
        headerViewport.current.scrollLeft = scroller.scrollLeft
        syncedHeaderLeft = headerViewport.current.scrollLeft
      }
    }
    const syncSize = () => {
      if (headerOuter.current) headerOuter.current.style.paddingRight = `${scroller.offsetWidth - scroller.clientWidth}px`
      syncScroll()
    }
    syncSize()
    const viewport = headerViewport.current
    const syncHeaderScroll = () => {
      // Keyboard focus can scroll an offscreen heading into view too.
      if (viewport && viewport.scrollLeft !== syncedHeaderLeft && viewport.scrollLeft !== scroller.scrollLeft) scroller.scrollLeft = viewport.scrollLeft
    }
    const wheel = (event: WheelEvent) => {
      const delta = event.deltaX || (event.shiftKey ? event.deltaY : 0)
      if (!delta) return
      const previous = scroller.scrollLeft
      scroller.scrollLeft += delta
      if (scroller.scrollLeft !== previous) event.preventDefault()
    }
    viewport?.addEventListener('wheel', wheel, { passive: false })
    viewport?.addEventListener('scroll', syncHeaderScroll, { passive: true })
    scroller.addEventListener('scroll', syncScroll, { passive: true })
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(syncSize)
    observer?.observe(scroller)
    return () => { observer?.disconnect(); scroller.removeEventListener('scroll', syncScroll); viewport?.removeEventListener('wheel', wheel); viewport?.removeEventListener('scroll', syncHeaderScroll) }
  }, [scroller])
  return <div className={className} data-virtual-columns style={{ display: 'flex', flexDirection: 'column', flex: '1 1 auto', minWidth: 0, minHeight: 0, height: '100%', overflow: 'hidden' }}>
    <div ref={headerOuter} style={{ flex: '0 0 auto', minWidth: 0, boxSizing: 'border-box' }}>
      <div ref={headerViewport} data-virtual-column-header style={{ overflow: 'hidden', position: 'relative' }}>
        <div ref={headerExtent} aria-hidden style={{ position: 'absolute', left: 0, top: 0, height: 1, visibility: 'hidden', pointerEvents: 'none' }}/>
        {header}
      </div>
    </div>
    {virtualize ? <Virtuoso className={scrollerClassName} data={data} computeItemKey={computeItemKey} itemContent={itemContent} increaseViewportBy={increaseViewportBy}
      aria-label={ariaLabel} role={ariaLabel ? 'list' : undefined} scrollerRef={attachScroller}
      style={{ flex: '1 1 auto', minHeight: 0, width: '100%', scrollbarGutter: 'stable' }}/>
      : <div ref={setScroller} className={scrollerClassName} aria-label={ariaLabel} role="list" style={{ flex: '1 1 auto', minHeight: 0, overflow: 'auto', scrollbarGutter: 'stable' }}>{data.map((item, index) => <Fragment key={computeItemKey(index, item)}>{itemContent(index, item)}</Fragment>)}</div>}
  </div>
}
