import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from 'react'
import { cn } from '@/lib/utils'
import {
  persistSplitViewListWidth,
  readSplitViewListWidth,
  SPLIT_VIEW_MIN_DETAIL_WIDTH,
  SPLIT_VIEW_MIN_LIST_WIDTH,
  type SplitViewSurface,
} from './split-view-widths'
import styles from './split-view.module.css'

export interface SplitViewProps {
  surface: SplitViewSurface
  list: ReactNode
  detail?: ReactNode
  className?: string
  style?: CSSProperties
  minListWidth?: number
  minDetailWidth?: number
  defaultListWidth?: number
  'aria-label'?: string
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(maximum, Math.max(minimum, value))
}

/**
 * LS-0560 — shared list/detail SplitView shell with persisted list-width keys.
 * Inbox is the reference consumer for resize chrome + keyboard.
 */
export function SplitView({
  surface,
  list,
  detail,
  className,
  style,
  minListWidth = SPLIT_VIEW_MIN_LIST_WIDTH,
  minDetailWidth = SPLIT_VIEW_MIN_DETAIL_WIDTH,
  defaultListWidth,
  'aria-label': ariaLabel = 'Split view',
}: SplitViewProps) {
  const rootRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef({ active: false, pointerId: -1 })
  const [preferredListWidth, setPreferredListWidth] = useState(() =>
    readSplitViewListWidth(surface, defaultListWidth),
  )
  const [shellWidth, setShellWidth] = useState(0)
  const [resizing, setResizing] = useState(false)
  const detailOpen = detail !== undefined && detail !== null
  const maximumListWidth = Math.max(minListWidth, shellWidth - minDetailWidth)
  const listWidth = clamp(preferredListWidth, minListWidth, maximumListWidth)

  useEffect(() => {
    const shell = rootRef.current
    if (!shell) return
    const update = () => setShellWidth(shell.getBoundingClientRect().width)
    update()
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', update)
      return () => window.removeEventListener('resize', update)
    }
    const observer = new ResizeObserver(([entry]) => setShellWidth(entry.contentRect.width))
    observer.observe(shell)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    if (!resizing) return
    document.documentElement.classList.add('flow-split-view-resizing')
    return () => document.documentElement.classList.remove('flow-split-view-resizing')
  }, [resizing])

  const updateListWidth = (width: number) => {
    const next = clamp(width, minListWidth, maximumListWidth)
    setPreferredListWidth(next)
    persistSplitViewListWidth(surface, next)
  }

  const startResize = (event: ReactPointerEvent<HTMLButtonElement>) => {
    dragRef.current = { active: true, pointerId: event.pointerId }
    event.currentTarget.setPointerCapture(event.pointerId)
    setResizing(true)
    event.preventDefault()
  }
  const resize = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (!dragRef.current.active || dragRef.current.pointerId !== event.pointerId) return
    const shell = rootRef.current
    if (!shell) return
    updateListWidth(event.clientX - shell.getBoundingClientRect().left)
  }
  const stopResize = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    dragRef.current = { active: false, pointerId: -1 }
    setResizing(false)
  }
  const resizeWithKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const step = event.shiftKey ? 50 : 10
    const next =
      event.key === 'ArrowLeft' ? listWidth - step
      : event.key === 'ArrowRight' ? listWidth + step
      : event.key === 'Home' ? minListWidth
      : event.key === 'End' ? maximumListWidth
      : undefined
    if (next === undefined) return
    event.preventDefault()
    updateListWidth(next)
  }

  return (
    <div
      ref={rootRef}
      className={cn(styles.root, className)}
      aria-label={ariaLabel}
      data-split-view={surface}
      data-detail-open={detailOpen}
      data-resizing={resizing || undefined}
      style={{ ...style, '--split-list-width': `${listWidth}px` } as CSSProperties}
    >
      <section className={styles.list} data-split-view-list="">
        {list}
      </section>
      {detailOpen ? (
        <>
          <button
            className={styles.resizer}
            type="button"
            role="separator"
            aria-label="Resize list"
            aria-orientation="vertical"
            aria-valuemin={minListWidth}
            aria-valuemax={Math.round(maximumListWidth)}
            aria-valuenow={Math.round(listWidth)}
            onKeyDown={resizeWithKeyboard}
            onPointerDown={startResize}
            onPointerMove={resize}
            onPointerUp={stopResize}
            onPointerCancel={stopResize}
          />
          <section className={styles.detail} data-split-view-detail="">
            {detail}
          </section>
        </>
      ) : null}
    </div>
  )
}
