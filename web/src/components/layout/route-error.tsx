import { useEffect } from 'react'
import { useRouteError } from 'react-router-dom'
import { AppConnectionError } from '@/components/layout/app-startup'
import { startupRetryDelay } from '@/lib/startup'
import { isStaleChunkError, reloadForStaleChunk } from '@/lib/stale-chunk-reload'

/**
 * Replaces the router's own error screen. A file of the app that could not be loaded (the server unreachable, or a
 * build that is gone) reloads once as before; otherwise the app's can't-connect state shows and the page reloads by
 * itself once the server answers again after failing to.
 */
export function RouteError() {
  const error = useRouteError()
  const chunk = isStaleChunkError(error)
  useEffect(() => {
    if (!chunk || reloadForStaleChunk(error)) return
    let stopped = false
    let unreachable = false
    let attempt = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const probe = async () => {
      try {
        const response = await fetch('/api/health', { cache: 'no-store', credentials: 'same-origin' })
        // Reload only after an outage: a server that answers while the file is missing would loop.
        if (response.ok && unreachable && !stopped) { window.location.reload(); return }
        if (response.ok) return
      } catch { /* still unreachable */ }
      unreachable = true
      if (!stopped) timer = setTimeout(() => void probe(), startupRetryDelay(attempt++))
    }
    void probe()
    return () => { stopped = true; clearTimeout(timer) }
  }, [chunk, error])
  const reload = () => window.location.reload()
  return chunk
    ? <AppConnectionError retry={reload} detail="Part of Flow could not be loaded." action="Reload" />
    : <AppConnectionError retry={reload} title="Something went wrong." detail="Part of Flow could not be loaded." action="Reload" />
}
