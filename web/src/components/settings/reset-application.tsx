/**
 * LS-0530 ResetApplication — wipe local client state then hard-navigate home.
 * Support / dev escape hatch. Mirrors Linear ResetApplication.deleteAllDatabases → `/`.
 */
import { useEffect, useState } from 'react'
import { ClientStorage } from '@/lib/client-storage'
import { clearViewPreferences } from '@/lib/view-preferences'

const KNOWN_DATABASES = ['flow-realtime-cache'] as const

export async function deleteAllDatabases(): Promise<void> {
  // Clear local / session prefs first (sync).
  try {
    ClientStorage.clear('local')
    ClientStorage.clear('session')
  } catch {
    /* ignore */
  }
  clearViewPreferences()

  // Drop IndexedDB databases Flow owns.
  if (typeof indexedDB === 'undefined') return
  const listed =
    typeof indexedDB.databases === 'function'
      ? await indexedDB.databases().catch(() => [] as IDBDatabaseInfo[])
      : []
  const names = new Set<string>(KNOWN_DATABASES)
  for (const info of listed) {
    if (info.name) names.add(info.name)
  }
  await Promise.all(
    [...names].map(
      name =>
        new Promise<void>(resolve => {
          try {
            const request = indexedDB.deleteDatabase(name)
            request.onsuccess = request.onerror = request.onblocked = () => resolve()
          } catch {
            resolve()
          }
        }),
    ),
  )
}

export type ResetApplicationProps = {
  /** Override navigation (tests). Defaults to window.location.replace('/'). */
  onComplete?: () => void
  /** When false, do not auto-run on mount (manual trigger UI). Default true. */
  auto?: boolean
}

/**
 * Route component: on mount, wipe client state and replace location with `/`.
 */
export function ResetApplication({ onComplete, auto = true }: ResetApplicationProps = {}) {
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!auto) return
    let cancelled = false
    void deleteAllDatabases()
      .then(() => {
        if (cancelled) return
        if (onComplete) onComplete()
        else if (typeof window !== 'undefined') window.location.replace('/')
      })
      .catch(err => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [auto, onComplete])

  if (error) {
    return (
      <div className="reset-application reset-application-error" role="alert">
        Could not reset application: {error}
      </div>
    )
  }
  return <div className="reset-application" aria-busy="true" />
}

ResetApplication.displayName = 'ResetApplication'

/**
 * Preferences danger-zone control: confirm then navigate to `/reset`.
 */
export function ResetApplicationControl({
  onNavigate,
}: {
  onNavigate?: (path: string) => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  const run = async () => {
    setBusy(true)
    try {
      if (onNavigate) {
        await deleteAllDatabases()
        onNavigate('/')
        return
      }
      if (typeof window !== 'undefined') {
        window.location.assign('/reset')
      }
    } finally {
      setBusy(false)
      setConfirming(false)
    }
  }

  return (
    <div className="reset-application-control">
      {!confirming ? (
        <button
          type="button"
          className="settings-action danger"
          onClick={() => setConfirming(true)}
        >
          Reset application
        </button>
      ) : (
        <div className="reset-application-confirm">
          <p>
            This clears local drafts, caches, and view preferences on this device, then reloads
            Flow. Server data is not deleted.
          </p>
          <div className="reset-application-confirm-actions">
            <button
              type="button"
              className="settings-action"
              disabled={busy}
              onClick={() => setConfirming(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              className="settings-action danger"
              disabled={busy}
              onClick={() => void run()}
            >
              Reset and reload
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
