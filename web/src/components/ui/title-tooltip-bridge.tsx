import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

/**
 * Renders native `title` attributes as Flow tooltips app-wide, so every hover hint shares the
 * FlowTooltip look, delay and motion instead of the browser's own tooltip. While an element is
 * hovered or keyboard-focused its `title` is parked in `data-flow-title` (suppressing the native
 * tooltip) and restored afterwards; an element whose only accessible name was the title gets it as
 * `aria-label` in the meantime. Opt out with `data-native-title` on the element or an ancestor.
 */
export function TitleTooltipBridge({ delay = 450, skipDelay = 300 }: { delay?: number; skipDelay?: number }) {
  const [tip, setTip] = useState<{ text: string; rect: DOMRect } | null>(null)
  const active = useRef<HTMLElement | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const lastHidden = useRef(0)

  useEffect(() => {
    const hide = () => {
      window.clearTimeout(timer.current)
      setTip(current => { if (current) lastHidden.current = Date.now(); return null })
    }
    const release = () => {
      const element = active.current
      active.current = null
      hide()
      if (!element) return
      const text = element.getAttribute('data-flow-title')
      if (text != null && !element.hasAttribute('title')) element.setAttribute('title', text)
      element.removeAttribute('data-flow-title')
      if (element.hasAttribute('data-flow-title-label')) { element.removeAttribute('aria-label'); element.removeAttribute('data-flow-title-label') }
    }
    const capture = (target: EventTarget | null) => {
      const element = target instanceof Element ? target.closest<HTMLElement>('[title]') : null
      if (!element || element === document.documentElement || element === document.body || element.closest('[data-native-title]')) return null
      const text = element.getAttribute('title')?.trim()
      return text ? { element, text } : null
    }
    const activate = (element: HTMLElement, text: string, immediate: boolean) => {
      if (active.current === element) return
      release()
      active.current = element
      element.setAttribute('data-flow-title', element.getAttribute('title') ?? text)
      element.removeAttribute('title')
      if (!element.hasAttribute('aria-label') && !element.hasAttribute('aria-labelledby') && !element.textContent?.trim()) {
        element.setAttribute('aria-label', text)
        element.setAttribute('data-flow-title-label', '')
      }
      const show = () => { if (active.current === element && element.isConnected) setTip({ text, rect: element.getBoundingClientRect() }) }
      window.clearTimeout(timer.current)
      if (immediate || Date.now() - lastHidden.current < skipDelay) show()
      else timer.current = window.setTimeout(show, delay)
    }
    const onOver = (event: PointerEvent) => {
      if (event.pointerType === 'touch') return
      const found = capture(event.target)
      if (found) activate(found.element, found.text, false)
    }
    const onOut = (event: PointerEvent) => {
      const element = active.current
      if (element && !(event.relatedTarget instanceof Node && element.contains(event.relatedTarget))) release()
    }
    const onFocus = (event: FocusEvent) => {
      const target = event.target
      if (!(target instanceof HTMLElement) || !target.matches(':focus-visible')) return
      const found = capture(target)
      if (found && found.element === target) activate(target, found.text, false)
    }
    const onBlur = (event: FocusEvent) => { if (event.target === active.current) release() }
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') hide() }
    document.addEventListener('pointerover', onOver, true)
    document.addEventListener('pointerout', onOut, true)
    document.addEventListener('pointerdown', hide, true)
    document.addEventListener('focusin', onFocus, true)
    document.addEventListener('focusout', onBlur, true)
    document.addEventListener('keydown', onKey, true)
    window.addEventListener('scroll', hide, true)
    window.addEventListener('blur', release)
    return () => {
      release()
      document.removeEventListener('pointerover', onOver, true)
      document.removeEventListener('pointerout', onOut, true)
      document.removeEventListener('pointerdown', hide, true)
      document.removeEventListener('focusin', onFocus, true)
      document.removeEventListener('focusout', onBlur, true)
      document.removeEventListener('keydown', onKey, true)
      window.removeEventListener('scroll', hide, true)
      window.removeEventListener('blur', release)
    }
  }, [delay, skipDelay])

  return tip ? createPortal(<TitleTooltip text={tip.text} rect={tip.rect}/>, document.body) : null
}

function TitleTooltip({ text, rect }: { text: string; rect: DOMRect }) {
  const ref = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number; side: 'top' | 'bottom' } | null>(null)
  useLayoutEffect(() => {
    const node = ref.current
    if (!node) return
    const width = node.offsetWidth, height = node.offsetHeight, gap = 6, pad = 8
    const below = rect.bottom + gap + height <= window.innerHeight - pad
    const left = Math.min(Math.max(rect.left + rect.width / 2 - width / 2, pad), Math.max(pad, window.innerWidth - width - pad))
    setPosition({ top: below ? rect.bottom + gap : Math.max(pad, rect.top - gap - height), left, side: below ? 'bottom' : 'top' })
  }, [rect, text])
  return <div
    ref={ref}
    role="tooltip"
    className="flow-tooltip-content flow-tooltip-content--title"
    data-flow-motion="tooltip"
    data-side={position?.side ?? 'bottom'}
    style={{ position: 'fixed', top: position?.top ?? rect.bottom + 6, left: position?.left ?? rect.left, visibility: position ? 'visible' : 'hidden', pointerEvents: 'none' }}
  ><span className="flow-tooltip-copy">{text}</span></div>
}
