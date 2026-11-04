import { useSyncExternalStore } from 'react'

const subscribe = (notify: () => void) => {
  addEventListener('online', notify)
  addEventListener('offline', notify)
  return () => {
    removeEventListener('online', notify)
    removeEventListener('offline', notify)
  }
}

/** Whether the browser reports a network connection (`navigator.onLine`, kept current by the online/offline events). */
export function useOnlineStatus() {
  return useSyncExternalStore(subscribe, () => navigator.onLine, () => true)
}
