import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { LoaderCircle, Maximize2, Minimize2, Minus, PanelLeft, PanelRight, Plus, X } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { AgentPanelLayout } from './agent-panel-layout'
import styles from './agent-panel.module.css'

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
  'aria-label'?: string
}) {
  const { t } = useI18n()
  const [dockState, setDockState] = useState<AgentPanelDock>(dockProp ?? readDock)
  const [widthState, setWidthState] = useState(widthProp ?? readWidth)
  const [minimizedState, setMinimizedState] = useState(false)
  const [fullscreenState, setFullscreenState] = useState(false)
  const [resizing, setResizing] = useState(false)
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null)

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

  return (
    <AgentPanelLayout name="AgentPanel" onRequestClose={onRequestClose}>
      <section
        aria-label={ariaLabel ?? t('Flow Agent chat')}
        aria-modal={variant === 'floating' ? 'false' : undefined}
        className={styles.panel}
        data-dock={dock}
        data-fullscreen={fullscreen || undefined}
        data-minimized={minimized || undefined}
        data-variant={variant}
        role={variant === 'floating' ? 'dialog' : 'complementary'}
        style={variant === 'sidebar' ? { width } : undefined}
      >
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
          <strong data-i18n-ignore={title !== t('New chat') || undefined}>{title}</strong>
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
              {minimized ? <Minimize2 /> : <Minus />}
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
              {fullscreen && !onOpenFullPage ? <Minimize2 /> : <Maximize2 />}
            </button>
          )}
          <button aria-label={t('Close chat')} onClick={onRequestClose} type="button">
            <X />
          </button>
        </header>
        {!minimized && (
          <div className={styles.body}>
            {loading ? (
              <div className={styles.loading} role="status">
                <LoaderCircle />
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
      {working ? <LoaderCircle size={14} /> : open ? <PanelRight size={15} /> : <PanelRight size={15} />}
      <span>{t('Chat')}</span>
    </button>
  )
}
