import { useSyncExternalStore } from 'react'

const query = '(min-width: 1024px)'
const subscribe = (notify: () => void) => {
  const media = window.matchMedia(query)
  media.addEventListener('change', notify)
  return () => media.removeEventListener('change', notify)
}

export function useDesktopLayout() {
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}
