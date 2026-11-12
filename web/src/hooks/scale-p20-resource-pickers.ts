import { useContext, useEffect, useMemo, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { loadResourcePeople, resourceWindowEnabled } from '@/lib/resource-window-adapters'
import { syncClientFor } from '@/lib/sync-client'
import { markSuspendedMembersInactive } from '@/lib/people'
import { searchDocuments } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import type { BootstrapData, FlowDocument, User } from '@/types/flow'

/** Loaded reference identities stay available even when they are no longer eligible for a new selection. */
export function useResourcePeople(users: User[], active = true): User[] {
  const data = useContext(ApplicationStoreContext)?.data
  // Some controls pass an intentionally restricted set, such as eligible assignees.
  const enabled = resourceWindowEnabled('pickers') && users === data?.users
  const client = syncClientFor(data)
  const workspace = data?.workspace.urlKey
  const viewer = data?.viewer.id
  const epoch = data?.sync?.authzEpoch
  const [loaded, setLoaded] = useState<{ workspace: string; viewer: string; epoch?: string; users: User[] }>()
  useEffect(() => {
    if (!active || !workspace || !viewer || !enabled) return
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    void loadResourcePeople(workspace, signal, client).then(people => {
      if (!signal.aborted) setLoaded({ workspace, viewer, epoch, users: people })
    }).catch(error => {
      if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status)))
    })
    return () => controller.abort()
  }, [active, client, enabled, epoch, viewer, workspace])
  return useMemo(() => {
    if (!enabled || !loaded || loaded.workspace !== workspace || loaded.viewer !== viewer || loaded.epoch !== epoch || client?.disposed) return users
    const eligible = new Map(loaded.users.map(user => [user.id, user]))
    const references = users.map(user => ({ ...user, active: eligible.has(user.id) && user.active !== false && eligible.get(user.id)?.active !== false }))
    return markSuspendedMembersInactive([...new Map([...loaded.users, ...references].map(user => [user.id, user])).values()], data?.members)
  }, [client?.disposed, data?.members, enabled, epoch, loaded, users, viewer, workspace])
}

export function useResourceMentionDocuments(data: BootstrapData | undefined, active: boolean, query: string): FlowDocument[] {
  const enabled = resourceWindowEnabled('pickers')
  const client = syncClientFor(data)
  const workspace = data?.workspace.urlKey
  const viewer = data?.viewer.id
  const epoch = data?.sync?.authzEpoch
  const [loaded, setLoaded] = useState<{ workspace: string; viewer: string; epoch?: string; query: string; documents: FlowDocument[] }>()
  useEffect(() => {
    if (!active || !workspace || !viewer || !enabled) return
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    const timer = window.setTimeout(() => {
      // searchDocuments is an existing title/content connection, separate from body hydration.
      void searchDocuments({ q: query, first: 20 }, signal).then(page => {
        if (!signal.aborted) setLoaded({ workspace, viewer, epoch, query, documents: page.nodes })
      }).catch(error => {
        if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status)))
      })
    }, query.trim() ? 150 : 0)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [active, client, enabled, epoch, query, viewer, workspace])
  return useMemo(() => {
    if (!active || !enabled || !loaded || loaded.workspace !== workspace || loaded.viewer !== viewer || loaded.epoch !== epoch || loaded.query !== query || client?.disposed) return data?.documents ?? []
    return [...new Map([...(data?.documents ?? []), ...loaded.documents].map(document => [document.id, document])).values()]
  }, [active, client?.disposed, data?.documents, enabled, epoch, loaded, query, viewer, workspace])
}
