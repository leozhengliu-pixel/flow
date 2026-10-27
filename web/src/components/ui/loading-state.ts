import { useLayoutEffect, useRef, useState } from 'react'

/** Caret appear delay inside a page or list body. */
export const LOADING_CARET_DELAY = 1000
/** Caret appear delay in a list footer while the next page loads. */
export const LOADING_FOOTER_CARET_DELAY = 350
/** A load that ends this soon after it started hides the loading state at once. */
export const LOADING_HIDE_IMMEDIATELY_BEFORE = 900
/** Otherwise the loading state stays until this long after it started, so a caret that appeared never just blinks once. */
export const LOADING_MIN_VISIBLE_UNTIL = 1500

/** Whether a loading state should still be shown: on at once, off with the reference's minimum-display rule. */
export function useLoadingState(loading: boolean) {
  const [visible, setVisible] = useState(loading)
  const [mountedAt] = useState(() => Date.now())
  const startedAt = useRef(mountedAt)
  const previous = useRef(loading)
  // A load that starts is shown at once (adjusted during render, so there is no frame without it).
  if (loading && !visible) setVisible(true)
  useLayoutEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const elapsed = Date.now() - startedAt.current
    if (loading) {
      if (!previous.current) startedAt.current = Date.now()
    } else if (elapsed < LOADING_HIDE_IMMEDIATELY_BEFORE) setVisible(false)
    else if (elapsed < LOADING_MIN_VISIBLE_UNTIL) timer = setTimeout(() => setVisible(false), LOADING_MIN_VISIBLE_UNTIL - elapsed)
    else setVisible(false)
    previous.current = loading
    return () => clearTimeout(timer)
  }, [loading])
  return visible
}
