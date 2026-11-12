import { useContext, useEffect, useMemo, useState } from 'react'

import { fetchDocumentRevisions } from '@/lib/api'
import type { DocumentRevision, FlowDocument } from '@/types/flow'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { loadResourceRevisions, resourceWindowEnabled } from '@/lib/resource-window-adapters'
import { syncClientFor } from '@/lib/sync-client'
import { ApiError } from '@/lib/api-client'

/** Loaded bodies of offloaded versions, by document and version id. A version's body never changes once it is offloaded. */
const loadedBodies = new Map<string, DocumentRevision>()
const bodyKey = (documentId: string, revisionId: string) => `${documentId}:${revisionId}`

/**
 * The document with its older versions' bodies, for views that compare or show versions (history, author labels).
 * Workspace data carries older versions without their bodies (`_offloaded`); they are fetched once when `enabled`.
 */
export function useLoadedRevisions<T extends Pick<FlowDocument, 'id' | 'revisions'>>(document: T, enabled: boolean): T {
  const data = useContext(ApplicationStoreContext)?.data
  const scoped = resourceWindowEnabled('documents')
  const client = syncClientFor(data)
  const workspace = data?.workspace.urlKey
  const scope = JSON.stringify([data?.workspace.id, data?.viewer.id, data?.sync?.authzEpoch, document.id])
  const [local, setLocal] = useState<{ scope: string; revisions: DocumentRevision[] }>()
  useEffect(() => {
    if (scoped) setLocal(current => !enabled || current?.scope !== scope ? undefined : current)
  }, [enabled, scope, scoped])
  const missing = useMemo(() => (document.revisions ?? []).filter(revision => revision._offloaded && !loadedBodies.has(bodyKey(document.id, revision.id))).map(revision => revision.id), [document])
  const [loads, setLoads] = useState(0)
  const pending = enabled && missing.length > 0
  const wanted = missing.join(',')
  useEffect(() => {
    if (scoped) return
    if (!pending) return
    const controller = new AbortController()
    fetchDocumentRevisions(document.id, controller.signal).then(revisions => {
      for (const revision of revisions) if (!revision._offloaded) loadedBodies.set(bodyKey(document.id, revision.id), revision)
      setLoads(count => count + 1)
    }).catch(() => undefined)
    return () => controller.abort()
  }, [pending, document.id, wanted, scoped])
  const needsBodies = !Object.hasOwn(document, 'revisions') || (document.revisions ?? []).some(revision => revision._offloaded)
  const scopedWanted = (document.revisions ?? []).filter(revision => revision._offloaded).map(revision => revision.id).join(',')
  useEffect(() => {
    if (!scoped || !enabled || !needsBodies || !workspace) return
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    void loadResourceRevisions(workspace, document.id, signal).then(revisions => {
      if (!signal.aborted) setLocal({ scope, revisions })
    }).catch(error => {
      if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status)))
    })
    return () => controller.abort()
  }, [client, document.id, enabled, needsBodies, scope, scoped, scopedWanted, workspace])
  return useMemo(() => {
    if (scoped) {
      if (!enabled || client?.disposed || local?.scope !== scope) return document
      const bodies = new Map(local.revisions.map(revision => [revision.id, revision]))
      return { ...document, revisions: Object.hasOwn(document, 'revisions') ? (document.revisions ?? []).map(revision => {
        const body = bodies.get(revision.id)
        return revision._offloaded && body && !body._offloaded
          ? { ...revision, content: body.content, contentState: body.contentState, contentData: body.contentData, _offloaded: undefined }
          : revision
      }) : local.revisions }
    }
    const revisions = document.revisions ?? []
    if (!revisions.some(revision => revision._offloaded)) return document
    return { ...document, revisions: revisions.map(revision => (revision._offloaded && loadedBodies.get(bodyKey(document.id, revision.id))) || revision) }
    // `loads` re-reads the cache after a fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [document, loads, scoped, local, scope, client?.disposed, enabled])
}
