import { useEffect, useRef } from 'react'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'

/**
 * Keyboard hints for Linear-style menus. Hints are written in Linear's macOS notation —
 * "P then S", "S", "⌥ F", "⇧ H", "Ctrl ⌥ D", "⌘ ⇧ ," — and rendered per platform
 * (⌘/⌥/⇧ read Ctrl/Alt/Shift elsewhere, as Linear does).
 */
export type ShortcutChord = { key: string; meta: boolean; ctrl: boolean; alt: boolean; shift: boolean }

const MODIFIERS = new Set(['⌘', 'Ctrl', '⌥', '⇧'])
const CODE_KEYS: Record<string, string> = { Comma: ',', Period: '.', Quote: "'", Slash: '/', Semicolon: ';', Minus: '-', Equal: '=', BracketLeft: '[', BracketRight: ']', Backquote: '`', ArrowDown: '↓', ArrowUp: '↑', ArrowLeft: '←', ArrowRight: '→', Enter: '↵', Backspace: '⌫' }

/** Splits a hint into its sequential steps; each step is one chord. */
export function parseShortcut(spec: string): ShortcutChord[] {
  return spec.split(/\s+then\s+/i).map(step => {
    const tokens = step.trim().split(/\s+/)
    const key = tokens.filter(token => !MODIFIERS.has(token)).join(' ').toLowerCase()
    return { key, meta: tokens.includes('⌘'), ctrl: tokens.includes('Ctrl'), alt: tokens.includes('⌥'), shift: tokens.includes('⇧') }
  })
}

/** The glyphs to print for each step on this platform. */
export function shortcutSteps(spec: string, mac: boolean): string[][] {
  return spec.split(/\s+then\s+/i).map(step => step.trim().split(/\s+/).map(token => mac ? token : ({ '⌘': 'Ctrl', '⌥': 'Alt', '⇧': 'Shift' } as Record<string, string>)[token] ?? token))
}

/** The key an event produced, layout-independent where it matters (⌥ changes the character on macOS). */
export function eventKey(event: Pick<KeyboardEvent, 'key' | 'code'>): string {
  if (/^Key[A-Z]$/.test(event.code)) return event.code.slice(3).toLowerCase()
  if (/^Digit\d$/.test(event.code)) return event.code.slice(5)
  if (/^Numpad\d$/.test(event.code)) return event.code.slice(6)
  return CODE_KEYS[event.code] ?? CODE_KEYS[event.key] ?? event.key.toLowerCase()
}

/** Whether an event is exactly this chord; ⌘ is Ctrl off macOS. */
export function chordMatches(event: Pick<KeyboardEvent, 'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>, chord: ShortcutChord, mac: boolean): boolean {
  const wantMeta = mac && chord.meta
  const wantCtrl = chord.ctrl || (!mac && chord.meta)
  return eventKey(event) === chord.key && event.metaKey === wantMeta && event.ctrlKey === wantCtrl && event.altKey === chord.alt && event.shiftKey === chord.shift
}

/** Open menus and modal dialogs swallow page hotkeys; a non-modal dialog (the floating agent chat) does not. */
const BLOCKING_OVERLAYS = '[role="menu"],[role="dialog"]:not([aria-modal="false"]),[role="alertdialog"]'

export const SHORTCUT_SEQUENCE_TIMEOUT = 1100

/**
 * Tracks "X then Y" sequences across key presses. `match` returns the hint an event completes,
 * `'pending'` when it starts a sequence one of the hints continues, or undefined.
 */
export function createShortcutMatcher(mac: boolean, now: () => number = () => Date.now()) {
  let pending: { chord: ShortcutChord; at: number } | null = null
  return {
    match(event: Pick<KeyboardEvent, 'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>, specs: Iterable<string>): string | 'pending' | undefined {
      if (['Shift', 'Meta', 'Control', 'Alt'].includes(event.key)) return undefined
      const parsed = [...specs].map(spec => ({ spec, steps: parseShortcut(spec) }))
      const armed = pending && now() - pending.at < SHORTCUT_SEQUENCE_TIMEOUT ? pending : null
      pending = null
      if (armed) {
        const done = parsed.find(({ steps }) => steps.length === 2 && sameChord(steps[0], armed.chord) && chordMatches(event, steps[1], mac))
        if (done) return done.spec
      }
      const single = parsed.find(({ steps }) => steps.length === 1 && chordMatches(event, steps[0], mac))
      if (single) return single.spec
      const starts = parsed.find(({ steps }) => steps.length === 2 && chordMatches(event, steps[0], mac))
      if (starts) { pending = { chord: starts.steps[0], at: now() }; return 'pending' }
      return undefined
    },
    reset() { pending = null },
  }
}

function sameChord(left: ShortcutChord, right: ShortcutChord) {
  return left.key === right.key && left.meta === right.meta && left.ctrl === right.ctrl && left.alt === right.alt && left.shift === right.shift
}

let queued: { shortcut: string; at: number } | null = null
/** Runs `shortcut` in the next row menu that opens (used when a hint is pressed over a row). */
export function queueLinearMenuShortcut(shortcut: string) { queued = { shortcut, at: Date.now() } }
export function takeQueuedShortcut() {
  const value = queued && Date.now() - queued.at < 1000 ? queued.shortcut : undefined
  queued = null
  return value
}


/**
 * Opens a row's context menu when one of its key hints is pressed while the row is hovered or
 * focused (Linear's list behaviour). Rows opt in with `data-linear-menu-row`.
 */
export function useLinearRowShortcuts(shortcuts: readonly string[], enabled = true) {
  const key = shortcuts.join('|')
  useEffect(() => {
    if (!enabled) return
    const specs = key.split('|').filter(Boolean)
    const matcher = createShortcutMatcher(isMacPlatform())
    let pointer: { x: number; y: number } | null = null
    const onPointerMove = (event: PointerEvent) => { pointer = { x: event.clientX, y: event.clientY } }
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const target = event.target instanceof Element ? event.target : null
      if (event.defaultPrevented || target?.closest('input,textarea,select,[contenteditable="true"],[role="textbox"]')) return
      if (document.querySelector(BLOCKING_OVERLAYS)) return
      const focused = target?.closest<HTMLElement>('[data-linear-menu-row]')
      const hovered = [...document.querySelectorAll<HTMLElement>('[data-linear-menu-row]')].find(row => safeMatches(row, ':hover'))
      const row = focused ?? hovered
      if (!row) { matcher.reset(); return }
      const result = matcher.match(event, specs)
      if (!result || result === 'pending') return
      event.preventDefault()
      event.stopImmediatePropagation()
      queueLinearMenuShortcut(result)
      const box = row.getBoundingClientRect()
      const inside = !focused && pointer && pointer.y >= box.top && pointer.y <= box.bottom
      const x = inside && pointer ? pointer.x : box.left + Math.min(240, box.width / 3)
      const y = inside && pointer ? pointer.y : box.top + box.height / 2
      row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: x, clientY: y }))
    }
    window.addEventListener('pointermove', onPointerMove, { passive: true })
    window.addEventListener('keydown', onKeyDown, true)
    return () => { window.removeEventListener('pointermove', onPointerMove); window.removeEventListener('keydown', onKeyDown, true) }
  }, [enabled, key])
}

function safeMatches(element: Element, selector: string) {
  try { return element.matches(selector) } catch { return false }
}

/**
 * Page-level key hints (e.g. a detail page's "…" menu): runs the handler for a completed hint unless
 * focus is in a text field or a menu or dialog is already open.
 */
export function useLinearHotkeys(handlers: Record<string, () => void>, enabled = true) {
  const latest = useRef(handlers)
  useEffect(() => { latest.current = handlers })
  const key = Object.keys(handlers).join('|')
  useEffect(() => {
    if (!enabled) return
    const matcher = createShortcutMatcher(isMacPlatform())
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const target = event.target instanceof Element ? event.target : null
      if (event.defaultPrevented || target?.closest('input,textarea,select,[contenteditable="true"],[role="textbox"]')) return
      if (document.querySelector(BLOCKING_OVERLAYS)) return
      const result = matcher.match(event, key.split('|').filter(Boolean))
      if (!result || result === 'pending') return
      event.preventDefault()
      event.stopImmediatePropagation()
      latest.current[result]?.()
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [enabled, key])
}
