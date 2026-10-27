import { useInsertionEffect, useRef, type RefObject } from 'react'

/**
 * A ref that always holds the value from the latest committed render, for callbacks that outlive a render
 * (timers, listeners, observers, async continuations) and must not re-subscribe when the value changes.
 *
 * The ref is written in an insertion effect, so it is current before any layout or passive effect and any
 * event handler of the same commit reads it. Never read `ref.current` while rendering; read it in handlers/effects.
 */
export function useLatestRef<T>(value: T): RefObject<T> {
  const ref = useRef(value)
  useInsertionEffect(() => {
    ref.current = value
  })
  return ref
}
