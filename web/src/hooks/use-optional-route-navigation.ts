import { startTransition, useCallback, useContext, useRef } from 'react'
import { UNSAFE_LocationContext, UNSAFE_NavigationContext } from 'react-router-dom'
import { nextNavigationState } from '@/lib/navigation-context'
import { preloadRoute } from '@/lib/route-preload'

/**
 * In-app navigation for leaf components that may render outside a router (isolated previews and
 * unit tests): inside the app it records the navigation trail like `useRouteNavigation`, without
 * that hook's global click listeners; outside a router it falls back to a full page load.
 */
export function useOptionalRouteNavigation() {
  const navigation = useContext(UNSAFE_NavigationContext)
  const location = useContext(UNSAFE_LocationContext)?.location
  const current = useRef(location)
  current.current = location
  return useCallback((path: string) => {
    const navigator = navigation?.navigator
    if (!navigator) { window.location.assign(path); return }
    void preloadRoute(path).catch(() => undefined)
    const here = current.current
    const state = here && path.startsWith('/') && !path.startsWith('//') ? nextNavigationState(here, path) : undefined
    startTransition(() => navigator.push(path, state))
  }, [navigation])
}
