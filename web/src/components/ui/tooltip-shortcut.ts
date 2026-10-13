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

/**
 * Which lighter tooltip palette a trigger's surroundings call for: `elevated` over the agent composer card
 * (`data-tooltip-elevated`), `raised` over dialogs and floating panels (`data-tooltip-raised`); the nearest marked
 * ancestor wins, and plain surfaces get the standard tooltip.
 */
export type TooltipTone = 'elevated' | 'raised'
export function tooltipTone(element: Element | null | undefined): TooltipTone | undefined {
  const surface = element?.closest('[data-tooltip-elevated],[data-tooltip-raised]')
  if (!surface) return undefined
  return surface.hasAttribute('data-tooltip-elevated') ? 'elevated' : 'raised'
}

/** Whether a tooltip for this trigger sits over an elevated surface. */
export function isElevatedTrigger(element: Element | null | undefined) {
  return tooltipTone(element) === 'elevated'
}
