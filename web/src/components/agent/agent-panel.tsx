import { useCallback, useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Minimize2, PanelLeft, PanelRight, Plus } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { AgentPanelLayout } from './agent-panel-layout'
import { clampFloatSize, FLOAT_DEFAULT_SIZE } from './agent-float-size'
import styles from './agent-panel.module.css'
import { GridLoader } from '@/components/ui/grid-loader'
import { AgentCloseGlyph, AgentCursorGlyph, AgentFullPageGlyph, AgentMinimizeGlyph } from '@/components/ui/agent-glyph'

export type AgentPanelDock = 'left' | 'right'
export type AgentPanelVariant = 'floating' | 'sidebar'

const WIDTH_KEY = 'flow:agent-panel:width'
const DOCK_KEY = 'flow:agent-panel:dock'
const MIN_WIDTH = 300
const MAX_WIDTH = 560
const DEFAULT_WIDTH = 400

function readWidth() {
  try {
    const value = Number(localStorage.getItem(WIDTH_KEY))
    if (Number.isFinite(value) && value >= MIN_WIDTH && value <= MAX_WIDTH) return value
  } catch {
    /* ignore */
  }
  return DEFAULT_WIDTH
}

const FLOAT_SIZE_KEY = 'flow:agent-float-size'

function readFloatSize() {
  try {
    const value = JSON.parse(localStorage.getItem(FLOAT_SIZE_KEY) ?? 'null') as { width?: unknown; height?: unknown } | null
    if (value && typeof value.width === 'number' && typeof value.height === 'number') return { width: value.width, height: value.height }
  } catch {
    /* ignore */
  }
  return FLOAT_DEFAULT_SIZE
}

function readDock(): AgentPanelDock {
  try {
    return localStorage.getItem(DOCK_KEY) === 'left' ? 'left' : 'right'
  } catch {
    return 'right'
  }
}

/** LS-0034 AgentPanel — dockable / resizable chrome factored out of the toolbar panel. */
export function AgentPanel({
  title,
  open,
  onRequestClose,
  onNewChat,
  onOpenFullPage,
  onToggleDock,
  variant = 'sidebar',
  dock: dockProp,
  width: widthProp,
  onWidthChange,
  minimized: minimizedProp,
  onMinimizedChange,
  fullscreen: fullscreenProp,
  onFullscreenChange,
  loading = false,
  children,
  headerExtra,
  titleExtra,
  unread = false,
  'aria-label': ariaLabel,
}: {
  title: string
  open: boolean
  onRequestClose: () => void
  onNewChat?: () => void
  onOpenFullPage?: () => void
  onToggleDock?: (dock: AgentPanelDock) => void
  variant?: AgentPanelVariant
  dock?: AgentPanelDock
  width?: number
  onWidthChange?: (width: number) => void
  minimized?: boolean
  onMinimizedChange?: (minimized: boolean) => void
  fullscreen?: boolean
  onFullscreenChange?: (fullscreen: boolean) => void
  loading?: boolean
  children?: ReactNode
  headerExtra?: ReactNode
  /** Controls right after the title (the reference app's "…" chat menu in the floating chat). */
  titleExtra?: ReactNode
  /** A reply arrived while the floating chat was minimized: its bottom-bar pill shows a dot. */
  unread?: boolean
  'aria-label'?: string
}) {
  const { t } = useI18n()
  const [dockState, setDockState] = useState<AgentPanelDock>(dockProp ?? readDock)
  const [widthState, setWidthState] = useState(widthProp ?? readWidth)
  const [minimizedState, setMinimizedState] = useState(false)
  const [fullscreenState, setFullscreenState] = useState(false)
  const [resizing, setResizing] = useState(false)
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null)
  const [floatSize, setFloatSize] = useState(readFloatSize)
  const [floatResize, setFloatResize] = useState<{ edge: 'top' | 'left' | 'corner'; x: number; y: number; width: number; height: number }>()
  const [viewportHeight, setViewportHeight] = useState(() => window.innerHeight)
  const [dockSlot, setDockSlot] = useState<Element | null>(null)

  const dock = dockProp ?? dockState
  const width = widthProp ?? widthState
  const minimized = minimizedProp ?? minimizedState
  const fullscreen = fullscreenProp ?? fullscreenState

  useEffect(() => {
    if (dockProp) setDockState(dockProp)
  }, [dockProp])
  useEffect(() => {
    if (widthProp != null) setWidthState(widthProp)
  }, [widthProp])

  const setDock = (next: AgentPanelDock) => {
    setDockState(next)
    try {
      localStorage.setItem(DOCK_KEY, next)
    } catch {
      /* ignore */
    }
    onToggleDock?.(next)
  }

  const setWidth = useCallback(
    (next: number) => {
      const clamped = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(next)))
      setWidthState(clamped)
      try {
        localStorage.setItem(WIDTH_KEY, String(clamped))
      } catch {
        /* ignore */
      }
      onWidthChange?.(clamped)
    },
    [onWidthChange],
  )

  const setMinimized = (next: boolean) => {
    setMinimizedState(next)
    onMinimizedChange?.(next)
  }
  const setFullscreen = (next: boolean) => {
    setFullscreenState(next)
    onFullscreenChange?.(next)
    if (next) setMinimized(false)
  }

  useEffect(() => {
    if (!resizing) return
    const onMove = (event: PointerEvent) => {
      const drag = dragRef.current
      if (!drag) return
      const delta = dock === 'right' ? drag.startX - event.clientX : event.clientX - drag.startX
      setWidth(drag.startWidth + delta)
    }
    const onUp = () => {
      dragRef.current = null
      setResizing(false)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
  }, [dock, resizing, setWidth])

  useEffect(() => {
    if (variant !== 'floating') return
    const onResize = () => setViewportHeight(window.innerHeight)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [variant])

  // The minimized chat lives in the bottom bar, next to the Agent button (the reference app's chat pill).
  useEffect(() => {
    if (variant !== 'floating' || !open) return
    setDockSlot(document.querySelector('[data-agent-dock-slot]'))
  }, [open, variant])

  useEffect(() => {
    if (!floatResize) return
    const onMove = (event: PointerEvent) => {
      const { edge, x, y, width, height } = floatResize
      setFloatSize(clampFloatSize({
        width: edge === 'top' ? width : width + x - event.clientX,
        height: edge === 'left' ? height : height + y - event.clientY,
      }))
    }
    const onUp = () => setFloatResize(undefined)
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
  }, [floatResize])

  useEffect(() => {
    if (variant !== 'floating' || floatResize) return
    try {
      localStorage.setItem(FLOAT_SIZE_KEY, JSON.stringify(floatSize))
    } catch {
      /* ignore */
    }
  }, [floatResize, floatSize, variant])

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault()
        onRequestClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onRequestClose])

  if (!open) return null

  const actionCount =
    1 + (onNewChat ? 1 : 0) + (variant === 'floating' || onOpenFullPage ? 1 : 0) + (variant === 'sidebar' ? 1 : 0) + (variant === 'floating' ? 1 : 0)
  const floating = variant === 'floating'
  const shownSize = clampFloatSize(floatSize, viewportHeight)
  const floatStyle = floating ? ({ '--agent-float-width': `${shownSize.width}px`, '--agent-float-height': `${shownSize.height}px` } as CSSProperties) : undefined
  const startFloatResize = (edge: 'top' | 'left' | 'corner') => (event: ReactPointerEvent) => {
    event.preventDefault()
    setFloatResize({ edge, x: event.clientX, y: event.clientY, ...shownSize })
  }

  // Minimized, the reference app keeps only a pill with the chat's title in the bottom bar; clicking it brings the chat back.
  if (floating && minimized && !fullscreen) {
    const pill = (
      <div className={styles.chatPill} data-unread={unread || undefined}>
        <button aria-label={t('Restore chat')} className={styles.chatPillOpen} onClick={() => setMinimized(false)} type="button">
          {unread && <span aria-hidden className={styles.chatPillDot}/>}
          <span data-i18n-ignore={title !== t('New chat') || undefined}>{title}</span>
        </button>
        <button aria-label={t('Close chat')} className={styles.chatPillClose} onClick={onRequestClose} type="button">
          <AgentCloseGlyph size={12} />
        </button>
      </div>
    )
    return dockSlot ? createPortal(pill, dockSlot) : <div className={styles.chatPillFallback}>{pill}</div>
  }

  return (
    <AgentPanelLayout name="AgentPanel" onRequestClose={onRequestClose}>
      <section
        aria-label={ariaLabel ?? t('Flow Agent chat')}
        aria-modal={variant === 'floating' ? 'false' : undefined}
        className={styles.panel}
        data-dock={dock}
        data-fullscreen={fullscreen || undefined}
        data-minimized={minimized || undefined}
        data-tooltip-raised={variant === 'floating' ? '' : undefined}
        data-variant={variant}
        role={variant === 'floating' ? 'dialog' : 'complementary'}
        style={variant === 'sidebar' ? { width } : floatStyle}
      >
        {floating && !fullscreen && (
          <>
            <div aria-hidden className={styles.floatResize} data-edge="top" onPointerDown={startFloatResize('top')} />
            <div aria-hidden className={styles.floatResize} data-edge="left" onPointerDown={startFloatResize('left')} />
            <div aria-hidden className={styles.floatResize} data-edge="corner" onPointerDown={startFloatResize('corner')} />
          </>
        )}
        {variant === 'sidebar' && (
          <div
            aria-hidden
            className={styles.resizeHandle}
            data-active={resizing || undefined}
            data-edge={dock === 'right' ? 'left' : 'right'}
            onPointerDown={event => {
              event.preventDefault()
              dragRef.current = { startX: event.clientX, startWidth: width }
              setResizing(true)
            }}
            title={t('Resize agent chat')}
          />
        )}
        <header className={styles.header} data-actions={String(Math.min(4, actionCount))}>
          {titleExtra ? (
            <span className={styles.titleRow}>
              <strong data-i18n-ignore={title !== t('New chat') || undefined}>{title}</strong>
              {titleExtra}
            </span>
          ) : <strong data-i18n-ignore={title !== t('New chat') || undefined}>{title}</strong>}
          <span />
          {headerExtra}
          {onNewChat && (
            <button aria-label={t('New chat')} onClick={onNewChat} type="button">
              <Plus />
            </button>
          )}
          {variant === 'sidebar' && (
            <button
              aria-label={t(dock === 'right' ? 'Dock agent to left' : 'Dock agent to right')}
              data-active={undefined}
              onClick={() => setDock(dock === 'right' ? 'left' : 'right')}
              type="button"
            >
              {dock === 'right' ? <PanelLeft /> : <PanelRight />}
            </button>
          )}
          {variant === 'floating' && (
            <button
              aria-label={t(minimized ? 'Restore chat' : 'Minimize chat')}
              onClick={() => setMinimized(!minimized)}
              type="button"
            >
              {minimized ? <Minimize2 /> : <AgentMinimizeGlyph size={14} />}
            </button>
          )}
          {(variant === 'floating' || onOpenFullPage) && (
            <button
              aria-label={t(fullscreen && !onOpenFullPage ? 'Exit full page' : 'Open full page')}
              onClick={() => {
                if (onOpenFullPage) {
                  onOpenFullPage()
                  return
                }
                setFullscreen(!fullscreen)
              }}
              type="button"
            >
              {fullscreen && !onOpenFullPage ? <Minimize2 /> : <AgentFullPageGlyph size={14} />}
            </button>
          )}
          <button aria-label={t('Close chat')} onClick={onRequestClose} type="button">
            <AgentCloseGlyph size={14} />
          </button>
        </header>
        {!minimized && (
          <div className={styles.body}>
            {loading ? (
              <div className={styles.loading} role="status">
                <GridLoader variant="agent" size={16} />
                {t('Loading conversation…')}
              </div>
            ) : (
              children
            )}
          </div>
        )}
      </section>
    </AgentPanelLayout>
  )
}

export function AgentPanelToggleButton({
  open,
  onToggle,
  working,
  className,
}: {
  open: boolean
  onToggle: () => void
  working?: boolean
  className?: string
}) {
  const { t } = useI18n()
  return (
    <button
      aria-expanded={open}
      aria-label={t(open ? 'Close chat' : 'Open chat')}
      className={className}
      data-active={open || undefined}
      onClick={onToggle}
      title={t(open ? 'Close chat' : 'Open chat')}
      type="button"
    >
      {working ? <GridLoader variant="agent" size={14} /> : <AgentCursorGlyph size={14} />}
      <span>{t('Chat')}</span>
    </button>
  )
}
