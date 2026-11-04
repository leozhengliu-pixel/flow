import { useSyncExternalStore } from 'react'
import { DESKTOP_LAYOUT_QUERY } from '@/lib/layout-breakpoints'

const query = DESKTOP_LAYOUT_QUERY
const subscribe = (notify: () => void) => {
  const media = window.matchMedia(query)
  media.addEventListener('change', notify)
  return () => media.removeEventListener('change', notify)
}

export function useDesktopSettings() {
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}
