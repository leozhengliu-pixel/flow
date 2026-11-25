import { useContext, useEffect, useMemo, useState } from 'react'
import { fetchDocumentRevisions } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { loadResourceRevision, loadResourceRevisions } from '@/lib/resource-window-adapters'
import { resourceEndpointUnavailable } from '@/lib/scale-p20-transport'
import { syncClientFor } from '@/lib/sync-client'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import type { DocumentRevision, FlowDocument } from '@/types/flow'

export type RevisionSelection = { selectedId: string; highlight: boolean }

export function useResourceRevisions<T extends Pick<FlowDocument, 'id' | 'revisions'>>(document: T, enabled: boolean, selection?: RevisionSelection): T {
  const data = useContext(ApplicationStoreContext)?.data
  const client = syncClientFor(data)
  const workspace = data?.workspace.urlKey
  const scope = JSON.stringify([data?.workspace.id, data?.viewer.id, data?.sync?.authzEpoch, document.id])
  const sourceSignature = JSON.stringify(document.revisions?.map(revision => [revision.id, revision.title, revision.createdAt, Boolean(revision._offloaded), Boolean(revision._bodyOmitted), Boolean(revision._current)]))
  const [directory, setDirectory] = useState<{ scope: string; signature: string | undefined; revisions: DocumentRevision[] }>()
  const [bodies, setBodies] = useState<{ scope: string; signature: string | undefined; wanted: string; revisions: DocumentRevision[] }>()
  const needsDirectory = !Object.hasOwn(document, 'revisions') || (document.revisions ?? []).some(revision => revision._offloaded)
  useEffect(() => {
    if (!enabled) { setDirectory(undefined); setBodies(undefined); return }
    if (!workspace || !needsDirectory) return
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    void loadResourceRevisions(workspace, document.id, signal).then(revisions => {
      if (!signal.aborted) setDirectory({ scope, signature: sourceSignature, revisions })
    }).catch(error => {
      if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status)))
    })
    return () => controller.abort()
  }, [client, document.id, enabled, needsDirectory, scope, sourceSignature, workspace])
  const revisions = useMemo(() => directory?.scope === scope && directory.signature === sourceSignature ? directory.revisions : document.revisions ?? [], [directory, document.revisions, scope, sourceSignature])
  const wanted = useMemo(() => {
    if (!selection) return revisions.filter(revision => revision._bodyOmitted || revision._offloaded).map(revision => revision.id)
    const source = document as T & Partial<FlowDocument>
    const visible = revisions.filter((revision, index) => !revision._current && !(index === 0 && !revision._bodyOmitted && !revision._offloaded && revision.title === source.title && revision.content === source.content))
    const index = selection.selectedId === 'current' ? -1 : visible.findIndex(revision => revision.id === selection.selectedId)
    return [index >= 0 ? visible[index] : undefined, selection.highlight ? visible[index + 1] : undefined]
      .filter((revision): revision is DocumentRevision => Boolean(revision && (revision._bodyOmitted || revision._offloaded))).map(revision => revision.id)
  }, [document, revisions, selection])
  const wantedSignature = JSON.stringify(wanted)
  useEffect(() => {
    if (!enabled || !workspace || !wanted.length || needsDirectory && !(directory?.scope === scope && directory.signature === sourceSignature)) return
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    void (async () => {
      const loaded: DocumentRevision[] = []
      try {
        for (const id of wanted) {
          signal.throwIfAborted()
          loaded.push(await loadResourceRevision(workspace, document.id, id, signal))
        }
      } catch (error) {
        if (signal.aborted || !resourceEndpointUnavailable(error)) throw error
        // An older replica has only the established complete history endpoint.
        loaded.splice(0, loaded.length, ...await fetchDocumentRevisions(document.id, signal))
      }
      if (!signal.aborted) setBodies({ scope, signature: sourceSignature, wanted: wantedSignature, revisions: loaded })
    })().catch(error => {
      if (!signal.aborted && client && !client.disposed) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status)))
    })
    return () => controller.abort()
    // Selection identity, rather than freshly allocated arrays, owns a read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, directory?.scope, directory?.signature, document.id, enabled, needsDirectory, scope, sourceSignature, wantedSignature, workspace])
  return useMemo(() => {
    if (!enabled || client?.disposed) return document
    const loaded = bodies?.scope === scope && bodies.signature === sourceSignature && bodies.wanted === wantedSignature ? new Map(bodies.revisions.map(revision => [revision.id, revision])) : new Map<string, DocumentRevision>()
    const current = new Map((document.revisions ?? []).map(revision => [revision.id, revision]))
    return { ...document, revisions: revisions.map(revision => {
      const metadata = current.get(revision.id) ?? revision
      const body = loaded.get(revision.id) ?? (!revision._offloaded && !revision._bodyOmitted ? revision : undefined)
      return body && !body._offloaded ? { ...metadata, _current: revision._current, content: body.content, contentState: body.contentState, contentData: body.contentData, _offloaded: undefined, _bodyOmitted: undefined } : metadata
    }) }
  }, [bodies, client?.disposed, document, enabled, revisions, scope, sourceSignature, wantedSignature])
}
