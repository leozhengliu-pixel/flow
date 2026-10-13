/** Helpers for the app-wide tooltip (components/ui/tooltip.tsx). */

const MODIFIERS = /^([⌘⇧⌥⌃]+)(.+)$/u

/** Splits a shortcut into steps of keys: "⌘ ⇧ ," / "⌘⇧," → [["⌘","⇧",","]], "G then I" → [["G"],["I"]], "Ctrl+Enter" → [["Ctrl","Enter"]]. */
export function tooltipShortcutKeys(value: string): string[][] {
  return value.split(/,? then /).map(step => step.trim().split(/\s+/).filter(Boolean).flatMap(key => {
    if (key.length > 1 && key.includes('+')) return key.split('+').filter(Boolean)
    const modifiers = MODIFIERS.exec(key)
    return modifiers ? [...modifiers[1], modifiers[2]] : [key]
  })).filter(step => step.length > 0)
}

/** Whether a tooltip for this trigger sits over an elevated surface. */
export function isElevatedTrigger(element: Element | null | undefined) {
  return Boolean(element?.closest('[data-tooltip-elevated]'))
}
