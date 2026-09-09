import { animate, useMotionValue, useMotionValueEvent, useReducedMotion } from 'motion/react'
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { motionPresets } from '@/lib/motion-presets'

const compactQuery = '(max-width: 1024px)'
const widthKey = 'flow.sidebar.width'
const collapsedKey = 'flow.sidebar.collapsed'
const triggerSelector = '[data-sidebar-trigger]'
const portalSelector = '[data-radix-popper-content-wrapper], [role="menu"], [role="dialog"]'

function readPreferences() {
  try {
    const stored = localStorage.getItem(widthKey)
    const width = stored === null ? 244 : Number(stored)
    // The old 52px rail also resulted from Number(null). Never migrate that
    // ambiguous value into an intentional collapsed preference.
    return { width: Number.isFinite(width) && width > 64 ? clampWidth(width) : 244, collapsed: localStorage.getItem(collapsedKey) === 'true' }
  } catch { return { width: 244, collapsed: false } }
}

function clampWidth(width: number) { return Math.max(220, Math.min(330, width)) }
function editable(target: EventTarget | null) {
  return target instanceof HTMLElement && Boolean(target.closest('input,textarea,select,[contenteditable="true"],[role="textbox"],[role="searchbox"]'))
}

export function useSidebarLayout(open: boolean, onOpenChange?: (open: boolean) => void) {
  const [initial] = useState(readPreferences)
  const [width, setWidth] = useState(initial.width)
  const [collapsed, setCollapsed] = useState(initial.collapsed)
  const [compact, setCompact] = useState(() => window.matchMedia(compactQuery).matches)
  const [floatingOpen, setFloatingOpen] = useState(false)
  const [resizing, setResizing] = useState(false)
  const surfaceRef = useRef<HTMLElement>(null)
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const manualOpen = useRef(false)
  const suppressTriggerHover = useRef(false)
  const returnFocus = useRef<HTMLElement | null>(null)
  const lastOffset = useRef(0)
  const resize = useRef<{ x: number; width: number; originalWidth: number; moved: boolean; hidden: boolean } | null>(null)
  const skipClick = useRef(false)
  const onOpenChangeRef = useRef(onOpenChange)
  onOpenChangeRef.current = onOpenChange
  const reduced = useReducedMotion()
  const floating = compact || collapsed
  const visible = !floating || floatingOpen
  const surfaceWidth = compact ? Math.min(330, window.innerWidth) : width
  const offset = floating && !compact ? floatingOpen ? 41.5 : lastOffset.current : 0
  lastOffset.current = offset
  const targetWidth = compact ? 0 : collapsed ? 8 : width
  const railWidth = useMotionValue(targetWidth)
  const current = useRef({ compact, collapsed, floating, floatingOpen, width })
  current.current = { compact, collapsed, floating, floatingOpen, width }

  const clearHover = useCallback(() => { clearTimeout(hoverTimer.current); hoverTimer.current = undefined }, [])
  const close = useCallback(() => {
    clearHover()
    if (manualOpen.current && surfaceRef.current?.contains(document.activeElement)) returnFocus.current?.focus({ preventScroll: true })
    manualOpen.current = false
    setFloatingOpen(false)
    onOpenChangeRef.current?.(false)
  }, [clearHover])
  const reveal = useCallback((manual = false) => {
    if (!current.current.floating) return
    clearHover()
    manualOpen.current = manual
    if (manual) returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setFloatingOpen(true)
  }, [clearHover])
  const toggle = useCallback(() => {
    if (current.current.compact) {
      if (current.current.floatingOpen) close()
      else reveal(true)
    } else {
      close()
      setCollapsed(value => !value)
    }
  }, [close, reveal])
  const onEdgeEnter = useCallback(() => {
    if (current.current.compact || !current.current.collapsed || current.current.floatingOpen || resize.current) return
    clearHover()
    hoverTimer.current = setTimeout(() => reveal(), 250)
  }, [clearHover, reveal])

  useEffect(() => {
    const media = window.matchMedia(compactQuery)
    const change = () => { setCompact(media.matches); close() }
    media.addEventListener('change', change)
    return () => media.removeEventListener('change', change)
  }, [close])
  useEffect(() => { if (open) reveal(true) }, [open, reveal])
  useEffect(() => { if (!floating) close() }, [floating, close])
  useEffect(() => {
    if (floatingOpen && manualOpen.current) surfaceRef.current?.querySelector<HTMLElement>('button,a[href]')?.focus({ preventScroll: true })
  }, [floatingOpen])
  useEffect(() => {
    if (resizing) return
    try { localStorage.setItem(widthKey, String(width)); localStorage.setItem(collapsedKey, String(collapsed)) } catch { /* Optional local preference. */ }
  }, [width, collapsed, resizing])
  useMotionValueEvent(railWidth, 'change', value => document.documentElement.style.setProperty('--sidebar', `${value}px`))
  useLayoutEffect(() => {
    const root = document.documentElement
    root.dataset.sidebarMode = compact ? 'compact' : collapsed ? 'collapsed' : 'expanded'
    root.dataset.sidebarFloating = String(floating && floatingOpen)
    root.style.setProperty('--sidebar', `${railWidth.get()}px`)
    if (reduced || resizing) { railWidth.jump(targetWidth); return }
    if (railWidth.get() === targetWidth) return
    const animation = animate(railWidth, targetWidth, motionPresets.sidebar)
    return () => animation.stop()
  }, [compact, collapsed, floating, floatingOpen, railWidth, targetWidth, reduced, resizing])
  useLayoutEffect(() => () => {
    clearHover()
    delete document.documentElement.dataset.sidebarMode
    delete document.documentElement.dataset.sidebarFloating
    document.documentElement.style.removeProperty('--sidebar')
  }, [clearHover])

  useEffect(() => {
    const isPortal = (target: Element) => Boolean(target.closest(portalSelector))
    const pointerMove = (event: PointerEvent) => {
      if (event.target instanceof Element && !event.target.closest(triggerSelector)) suppressTriggerHover.current = false
      if (!current.current.floatingOpen || current.current.compact || manualOpen.current || resize.current) return
      const target = event.target
      if (!(target instanceof Element) || target.closest(triggerSelector) || surfaceRef.current?.contains(target) || isPortal(target)) return
      close()
    }
    const pointerOver = (event: PointerEvent) => {
      if (suppressTriggerHover.current || event.pointerType === 'touch' || current.current.compact || !current.current.collapsed) return
      if (event.target instanceof Element && event.target.closest(triggerSelector)) reveal()
    }
    const pointerDown = (event: PointerEvent) => {
      if (!current.current.floatingOpen) return
      const target = event.target
      if (!(target instanceof Element) || surfaceRef.current?.contains(target) || target.closest(triggerSelector) || isPortal(target)) return
      close()
    }
    const triggerClick = (event: MouseEvent) => {
      if (current.current.compact || !current.current.collapsed || !(event.target instanceof Element) || !event.target.closest(triggerSelector)) return
      event.preventDefault()
      event.stopPropagation()
      if (current.current.floatingOpen) { suppressTriggerHover.current = true; close() }
      else reveal(event.detail === 0 || ('pointerType' in event && event.pointerType === 'touch'))
    }
    let exitTimer: ReturnType<typeof setTimeout> | undefined
    const windowEnter = () => clearTimeout(exitTimer)
    const windowLeave = () => {
      if (!current.current.compact && current.current.floatingOpen) exitTimer = setTimeout(close, 500)
    }
    const keyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || editable(event.target)) return
      if (event.key === '[' && !event.metaKey && !event.ctrlKey && !event.altKey) { event.preventDefault(); toggle() }
      else if (event.key === '\\' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); if (current.current.floatingOpen) close(); else reveal(true) }
      else if (event.key === 'Escape' && current.current.floatingOpen && !document.querySelector(portalSelector)) { event.preventDefault(); close() }
    }
    const openSidebar = () => reveal(true)
    document.addEventListener('pointerover', pointerOver)
    document.addEventListener('pointermove', pointerMove)
    document.addEventListener('pointerdown', pointerDown)
    document.addEventListener('click', triggerClick, true)
    document.documentElement.addEventListener('pointerenter', windowEnter)
    document.documentElement.addEventListener('pointerleave', windowLeave)
    window.addEventListener('keydown', keyDown)
    window.addEventListener('flow:open-sidebar', openSidebar)
    return () => {
      document.removeEventListener('pointerover', pointerOver)
      document.removeEventListener('pointermove', pointerMove)
      document.removeEventListener('pointerdown', pointerDown)
      document.removeEventListener('click', triggerClick, true)
      document.documentElement.removeEventListener('pointerenter', windowEnter)
      document.documentElement.removeEventListener('pointerleave', windowLeave)
      clearTimeout(exitTimer)
      window.removeEventListener('keydown', keyDown)
      window.removeEventListener('flow:open-sidebar', openSidebar)
    }
  }, [close, reveal, toggle])

  useEffect(() => {
    if (!resizing) return
    const move = (event: PointerEvent) => {
      const start = resize.current
      if (!start) return
      const delta = event.clientX - start.x
      start.moved ||= Math.abs(delta) > 3
      if (!start.moved) return
      const raw = start.width + delta
      if (raw < 55) { current.current.collapsed = true; setCollapsed(true); close() }
      else {
        if (start.hidden || !current.current.floatingOpen) { current.current.collapsed = false; setCollapsed(false) }
        setWidth(clampWidth(raw))
      }
    }
    const finish = () => {
      const start = resize.current
      if (start?.moved && current.current.collapsed && !current.current.floatingOpen) setWidth(start.originalWidth)
      skipClick.current = Boolean(start?.moved)
      setTimeout(() => { skipClick.current = false }, 0)
      resize.current = null
      setResizing(false)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', finish)
    window.addEventListener('pointercancel', finish)
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', finish); window.removeEventListener('pointercancel', finish) }
  }, [resizing, close])
  const onResizeStart = (event: ReactPointerEvent) => {
    if (event.button !== 0 || compact) return
    event.preventDefault()
    clearHover()
    event.currentTarget.setPointerCapture(event.pointerId)
    resize.current = { x: event.clientX, width: visible ? width : 8, originalWidth: width, moved: false, hidden: !visible }
    setResizing(true)
  }
  const onResizeClick = () => { if (skipClick.current) { skipClick.current = false; return }; toggle() }

  return { width, collapsed, compact, floating, floatingOpen, visible, resizing, surfaceWidth, offset, surfaceRef, close, toggle, onEdgeEnter, clearHover,
    onNavigate: () => { if (compact) close() }, onResizeStart, onResizeClick,
    resetWidth: () => setWidth(244), resizeBy: (delta: number) => setWidth(value => clampWidth(value + delta)),
    transition: reduced || resizing ? { duration: 0 } : motionPresets.sidebar }
}
