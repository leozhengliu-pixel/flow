import { useCallback, useSyncExternalStore } from 'react'

const SPLIT_VIEW_QUERY = '(min-width: 801px)'

/**
 * LS-0748 — whether the current viewport should render list+detail SplitView.
 * The reference app gates on screen size (+ store); Flow mirrors the viewport half here.
 */
export function useIsSplitView(enabled = true) {
  const subscribe = useCallback((onChange: () => void) => {
    if (!enabled) return () => {}
    const media = window.matchMedia(SPLIT_VIEW_QUERY)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [enabled])
  const getSnapshot = useCallback(() => enabled && window.matchMedia(SPLIT_VIEW_QUERY).matches, [enabled])
  const matches = useSyncExternalStore(subscribe, getSnapshot, () => false)

  return enabled && matches
}
