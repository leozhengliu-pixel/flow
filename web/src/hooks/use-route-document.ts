import { useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import { getDocument } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { findDocumentBySlugSuffix } from '@/lib/document-slug'
import { syncClientFor, SyncResetRequired } from '@/lib/sync-client'
import { loadResourceComments, loadResourceDocument, resourceTrashedDocument, resourceWindowEnabled } from '@/lib/resource-window-adapters'
import { useLatestRef } from '@/hooks/use-latest-ref'
import type { BootstrapData } from '@/types/flow'

export type RouteDocumentState = 'loading' | 'missing' | undefined

/**
 * A document link can name a document the loaded workspace data does not hold yet: one an Agent reply just wrote, or
 * one another person or tab created a moment ago. The page then fetches it by its slug (or id) and adds it to the
 * workspace data instead of showing nothing; only a document the server does not know (or the viewer may not open)
 * reads as not found.
 */
export function useRouteDocument({ data, slugId, found, setData }: {
  data: BootstrapData | null | undefined
  /** The document route's slug, or undefined on any other route. */
  slugId: string | undefined
  /** The page already has the document (or its trash entry). */
  found: boolean
  setData: Dispatch<SetStateAction<BootstrapData | null>>
}): RouteDocumentState {
  const workspaceKey = data?.workspace.urlKey
  const latestData = useLatestRef(data)
  const client = syncClientFor(data)
  const candidate = data?.documents?.find(item => item.id === slugId || item.slugId === slugId) ?? (slugId ? findDocumentBySlugSuffix(data?.documents ?? [], slugId) : undefined) ?? (slugId && resourceWindowEnabled('trash') ? resourceTrashedDocument(data?.trash, slugId)?.document : undefined)
  const candidateId = candidate?.id
  const record = candidate && client?.get({ kind: 'document', id: candidate.id })
  const windows = resourceWindowEnabled('documents') || resourceWindowEnabled('trash')
  const commentScope = windows && candidateId && !candidate?.deletedAt ? JSON.stringify([data?.workspace.id, data?.viewer.id, data?.sync?.authzEpoch, candidateId]) : undefined
  useEffect(() => {
    if (!commentScope || !workspaceKey || !candidateId) return
    const snapshot = latestData.current!
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    void loadResourceComments(workspaceKey, candidateId, signal).then(comments => {
      if (signal.aborted) return
      setData(current => current?.workspace.id === snapshot.workspace.id && current.viewer.id === snapshot.viewer.id && (!client || !client.disposed && syncClientFor(current) === client)
        ? { ...current, comments: { ...current.comments, [candidateId]: comments } } : current)
    }).catch(error => {
      if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403, 404].includes(error.status)))
    })
    return () => controller.abort()
  }, [candidateId, client, commentScope, latestData, setData, workspaceKey])
  const wanted = slugId && workspaceKey && data && (!data.resourceDetailsOmitted || client) && !found ? JSON.stringify([data.workspace.id, data.viewer.id, data.sync?.authzEpoch, slugId, record?.version, record?.completeness]) : undefined
  const [result, setResult] = useState<{ key: string; state: 'missing' }>()
  useEffect(() => {
    if (!wanted) return
    const slug = slugId!
    const snapshot = latestData.current!
    const viewerId = snapshot.viewer.id
    const workspaceId = snapshot.workspace.id
    const controller = new AbortController()
    let active = true
    const request = windows ? loadResourceDocument(snapshot, slug, controller.signal) : (client ? client.loadDocument(slug, controller.signal) : getDocument(slug)).then(document => ({ document, entry: undefined }))
    request.then(({ document, entry }) => {
      if (!active || client?.disposed) return
      // A deleted document is opened from the trash, which the workspace data lists; one this tab has not seen is not found.
      if (document.deletedAt && !entry) { setResult({ key: wanted, state: 'missing' }); return }
      setData(current => {
        if (!current || current.workspace.id !== workspaceId || current.viewer.id !== viewerId || client && (client.disposed || syncClientFor(current) !== client)) return current
        if (entry) return { ...current, trash: [entry, ...current.trash.filter(item => item.id !== entry.id)] }
        if (client) return client.adapt(current)
        return current.documents.some(item => item.id === document.id) ? current : { ...current, documents: [document, ...current.documents] }
      })
    }, error => {
      if (active && client && !client.disposed && error instanceof ApiError && [401, 403, 404].includes(error.status) && (error.status === 401 || candidateId)) { client.requestReset(); return }
      if (active && client && !client.disposed && (error instanceof SyncResetRequired || !(error instanceof ApiError && [403, 404].includes(error.status)))) { client.requestReset(true); return }
      if (active && !controller.signal.aborted && !client?.disposed) setResult({ key: wanted, state: 'missing' })
    })
    return () => { active = false; controller.abort() }
  }, [wanted, setData, client, slugId, candidateId, data?.workspace.id, data?.viewer.id, windows, latestData])
  if (!wanted) return undefined
  return result?.key === wanted ? result.state : 'loading'
}
