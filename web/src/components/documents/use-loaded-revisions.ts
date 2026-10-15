import { useEffect, useMemo, useState } from 'react'

import { fetchDocumentRevisions } from '@/lib/api'
import type { DocumentRevision, FlowDocument } from '@/types/flow'

/** Loaded bodies of offloaded versions, by document and version id. A version's body never changes once it is offloaded. */
const loadedBodies = new Map<string, DocumentRevision>()
const bodyKey = (documentId: string, revisionId: string) => `${documentId}:${revisionId}`

/**
 * The document with its older versions' bodies, for views that compare or show versions (history, author labels).
 * Workspace data carries older versions without their bodies (`_offloaded`); they are fetched once when `enabled`.
 */
export function useLoadedRevisions<T extends Pick<FlowDocument, 'id' | 'revisions'>>(document: T, enabled: boolean): T {
  const missing = useMemo(() => (document.revisions ?? []).filter(revision => revision._offloaded && !loadedBodies.has(bodyKey(document.id, revision.id))).map(revision => revision.id), [document])
  const [loads, setLoads] = useState(0)
  const pending = enabled && missing.length > 0
  const wanted = missing.join(',')
  useEffect(() => {
    if (!pending) return
    const controller = new AbortController()
    fetchDocumentRevisions(document.id, controller.signal).then(revisions => {
      for (const revision of revisions) if (!revision._offloaded) loadedBodies.set(bodyKey(document.id, revision.id), revision)
      setLoads(count => count + 1)
    }).catch(() => undefined)
    return () => controller.abort()
  }, [pending, document.id, wanted])
  return useMemo(() => {
    const revisions = document.revisions ?? []
    if (!revisions.some(revision => revision._offloaded)) return document
    return { ...document, revisions: revisions.map(revision => (revision._offloaded && loadedBodies.get(bodyKey(document.id, revision.id))) || revision) }
    // `loads` re-reads the cache after a fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [document, loads])
}
