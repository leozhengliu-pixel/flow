/**
 * LS-0758 usePendingDeletedTeams — soft-delete grace for teams.
 * Client-side PendingDeletedTeamsProvider mirrors Linear organization.teamsPendingDelete.
 * Hard DELETE is deferred until grace expires (or purge); restore cancels the pending state.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { ClientStorage } from '@/lib/client-storage'
import { deleteTeam } from '@/lib/api'

export const TEAM_DELETE_GRACE_MS = 30_000

export type PendingDeletedTeam = {
  teamId: string
  workspaceKey: string
  name?: string
  pendingAt: string
  expiresAt: string
}

type PendingMap = Record<string, PendingDeletedTeam>

export type PendingDeletedTeamsApi = {
  teamsPendingDelete: ReadonlyMap<string, boolean>
  pendingTeams: PendingDeletedTeam[]
  isDeletePending: (teamId: string) => boolean
  onDeleteStateChange: (next: Map<string, boolean>) => void
  /** Mark a team pending delete and schedule hard DELETE after grace. */
  scheduleDelete: (args: {
    workspaceKey: string
    teamId: string
    name?: string
    graceMs?: number
  }) => void
  /** Cancel a pending delete (restore). */
  cancelPendingDelete: (teamId: string) => void
  /** Immediately purge (hard DELETE) a pending team. */
  purgePendingDelete: (teamId: string) => Promise<void>
}

const STORAGE_KEY = 'flow:teams-pending-delete'
const PendingDeletedTeamsContext = createContext<PendingDeletedTeamsApi | null>(null)

function readStored(): PendingMap {
  return ClientStorage.get<PendingMap>(STORAGE_KEY, { storageMechanism: 'local', logError: false }) ?? {}
}

function writeStored(map: PendingMap) {
  if (Object.keys(map).length === 0) {
    ClientStorage.remove(STORAGE_KEY, 'local')
    return
  }
  ClientStorage.set(STORAGE_KEY, map, 'local')
}

function toFlagMap(entries: PendingMap): Map<string, boolean> {
  const flags = new Map<string, boolean>()
  for (const teamId of Object.keys(entries)) flags.set(teamId, true)
  return flags
}

export function PendingDeletedTeamsProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<PendingMap>(() => readStored())

  const persist = useCallback((next: PendingMap) => {
    setEntries(next)
    writeStored(next)
  }, [])

  const onDeleteStateChange = useCallback(
    (next: Map<string, boolean>) => {
      const current = readStored()
      const updated: PendingMap = {}
      for (const [teamId, pending] of next) {
        if (!pending) continue
        updated[teamId] = current[teamId] ?? {
          teamId,
          workspaceKey: '',
          pendingAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + TEAM_DELETE_GRACE_MS).toISOString(),
        }
      }
      persist(updated)
    },
    [persist],
  )

  const isDeletePending = useCallback(
    (teamId: string) => Boolean(entries[teamId]),
    [entries],
  )

  const cancelPendingDelete = useCallback(
    (teamId: string) => {
      const next = { ...entries }
      delete next[teamId]
      persist(next)
    },
    [entries, persist],
  )

  const purgePendingDelete = useCallback(
    async (teamId: string) => {
      const entry = entries[teamId] ?? readStored()[teamId]
      if (!entry?.workspaceKey) {
        cancelPendingDelete(teamId)
        return
      }
      await deleteTeam(entry.workspaceKey, teamId)
      cancelPendingDelete(teamId)
    },
    [cancelPendingDelete, entries],
  )

  const scheduleDelete = useCallback(
    (args: { workspaceKey: string; teamId: string; name?: string; graceMs?: number }) => {
      const graceMs = args.graceMs ?? TEAM_DELETE_GRACE_MS
      const now = Date.now()
      const next: PendingMap = {
        ...entries,
        [args.teamId]: {
          teamId: args.teamId,
          workspaceKey: args.workspaceKey,
          name: args.name,
          pendingAt: new Date(now).toISOString(),
          expiresAt: new Date(now + graceMs).toISOString(),
        },
      }
      persist(next)
    },
    [entries, persist],
  )

  // Sweep expired pending deletes → hard DELETE.
  useEffect(() => {
    const timers: number[] = []
    const now = Date.now()
    for (const entry of Object.values(entries)) {
      const remaining = Date.parse(entry.expiresAt) - now
      const run = () => {
        void deleteTeam(entry.workspaceKey, entry.teamId)
          .catch(() => {
            /* keep pending so user can retry / cancel */
          })
          .finally(() => {
            setEntries(current => {
              if (!current[entry.teamId]) return current
              const next = { ...current }
              delete next[entry.teamId]
              writeStored(next)
              return next
            })
          })
      }
      if (remaining <= 0) {
        run()
      } else {
        timers.push(window.setTimeout(run, remaining))
      }
    }
    return () => {
      for (const timer of timers) window.clearTimeout(timer)
    }
  }, [entries])

  const value = useMemo<PendingDeletedTeamsApi>(
    () => ({
      teamsPendingDelete: toFlagMap(entries),
      pendingTeams: Object.values(entries),
      isDeletePending,
      onDeleteStateChange,
      scheduleDelete,
      cancelPendingDelete,
      purgePendingDelete,
    }),
    [cancelPendingDelete, entries, isDeletePending, onDeleteStateChange, purgePendingDelete, scheduleDelete],
  )

  return (
    <PendingDeletedTeamsContext.Provider value={value}>
      {children}
    </PendingDeletedTeamsContext.Provider>
  )
}

export function usePendingDeletedTeams(): PendingDeletedTeamsApi {
  const value = useContext(PendingDeletedTeamsContext)
  if (!value) {
    throw new Error('usePendingDeletedTeams must be used within a PendingDeletedTeamsProvider.')
  }
  return value
}

/** Optional hook that returns null outside the provider (directory chrome). */
export function useOptionalPendingDeletedTeams(): PendingDeletedTeamsApi | null {
  return useContext(PendingDeletedTeamsContext)
}
