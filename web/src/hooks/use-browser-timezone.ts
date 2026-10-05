import { useEffect } from 'react'
import { updateUserSettings } from '@/lib/api'

export function browserTimeZone() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone ?? '' } catch { return '' }
}

/**
 * Pulse summaries arrive at 06:00 in the user's time zone: save the browser's
 * zone when the stored one is missing (including users without a settings
 * record yet) or differs — at most once per session per workspace and user.
 */
export function useSaveBrowserTimeZone(workspaceKey: string | undefined, viewerId: string | undefined, storedTimeZone: string | undefined) {
  useEffect(() => {
    if (!workspaceKey || !viewerId) return
    const zone = browserTimeZone()
    if (!zone || zone === storedTimeZone) return
    const key = `flow.timezone-saved:${workspaceKey}:${viewerId}`
    try {
      if (sessionStorage.getItem(key)) return
      sessionStorage.setItem(key, zone)
    } catch { /* still save once below */ }
    void updateUserSettings({ timezone: zone }, workspaceKey).catch(() => undefined)
  }, [storedTimeZone, viewerId, workspaceKey])
}
