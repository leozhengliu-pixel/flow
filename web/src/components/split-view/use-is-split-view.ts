import { useEffect, useState } from 'react'

const SPLIT_VIEW_QUERY = '(min-width: 801px)'

/**
 * LS-0748 — whether the current viewport should render list+detail SplitView.
 * Linear gates on screen size (+ store); Flow mirrors the viewport half here.
 */
export function useIsSplitView(enabled = true) {
  const [matches, setMatches] = useState(() => {
    if (typeof window === 'undefined' || !enabled) return false
    return window.matchMedia(SPLIT_VIEW_QUERY).matches
  })

  useEffect(() => {
    if (!enabled) {
      setMatches(false)
      return
    }
    const media = window.matchMedia(SPLIT_VIEW_QUERY)
    const update = () => setMatches(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [enabled])

  return enabled && matches
}
