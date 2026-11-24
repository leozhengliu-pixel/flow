import { useEffect } from 'react'
import { updateUserSettings } from '@/lib/api'

export function browserTimeZone() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone ?? '' } catch { return '' }
}

export function saveBrowserTimeZone(workspaceKey: string, viewerId: string, storedTimeZone: string | undefined) {
  const zone = browserTimeZone()
  if (!zone || zone === storedTimeZone) return
  const key = `flow.timezone-saved:${workspaceKey}:${viewerId}`
  try {
    if (sessionStorage.getItem(key)) return
    sessionStorage.setItem(key, zone)
  } catch { /* Storage is unavailable. The server also checks for no-op saves. */ }
  return updateUserSettings({ timezone: zone }, workspaceKey)
}

/**
 * Pulse summaries arrive at 06:00 in the user's time zone: save the browser's
 * zone when the stored one is missing (including users without a settings
 * record yet) or differs — at most once per session per workspace and user.
 */
export function useSaveBrowserTimeZone(workspaceKey: string | undefined, viewerId: string | undefined, storedTimeZone: string | undefined) {
  useEffect(() => {
    if (!workspaceKey || !viewerId) return
    void saveBrowserTimeZone(workspaceKey, viewerId, storedTimeZone)?.catch(() => undefined)
  }, [storedTimeZone, viewerId, workspaceKey])
}
