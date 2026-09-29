import { useEffect, useRef } from 'react'
import type { TriageActionKind } from './triage-model'

const EDITABLE = 'input,textarea,select,[contenteditable=true],[contenteditable=""],[role=textbox],[role=dialog],[role=menu],[role=listbox]'
/** Window for the second key of the `M M` chord. */
const CHORD_MS = 900

/**
 * Linear's triage keyboard mapping: `1` Accept, `2` Decline, `3` or `M M` Mark as duplicate, `H` Snooze.
 * Ignored while typing, inside dialogs/menus, or with a modifier held.
 */
export function useTriageShortcuts(enabled: boolean, onAction: (action: TriageActionKind) => void) {
  const handler = useRef(onAction)
  useEffect(() => { handler.current = onAction })
  useEffect(() => {
    if (!enabled) return
    let last = { key: '', at: 0 }
    const onKey = (event: KeyboardEvent) => {
      const target = event.target
      if (event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented) return
      if (target instanceof Element && target.closest(EDITABLE)) return
      const key = event.key.toLowerCase()
      const previous = last
      last = { key, at: event.timeStamp || Date.now() }
      const action: TriageActionKind | undefined =
        key === '1' ? 'accept'
          : key === '2' ? 'decline'
            : key === '3' ? 'duplicate'
              : key === 'h' && !event.shiftKey ? 'snooze'
                : key === 'm' && previous.key === 'm' && last.at - previous.at < CHORD_MS ? 'duplicate'
                  : undefined
      if (!action) return
      if (key === 'm') last = { key: '', at: 0 }
      event.preventDefault()
      handler.current(action)
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [enabled])
}
