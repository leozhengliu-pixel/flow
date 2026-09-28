/**
 * LS-0212 DocumentContentEditorState — shared contentById + presence listener registry.
 * Multiple live consumers (DocumentPage, CollaborativeEditor slot, future minimap/agent)
 * subscribe here instead of each owning ad-hoc maps.
 */

export type DocumentPresenceListener = (documentId: string) => void

const contentById = new Map<string, string>()
const contentStateById = new Map<string, string>()
const listenerPresenceListenersById = new Map<string, Set<DocumentPresenceListener>>()

export function getDocumentContent(documentId: string): string | undefined {
  return contentById.get(documentId)
}

export function getDocumentContentState(documentId: string): string | undefined {
  return contentStateById.get(documentId)
}

export function setDocumentContent(
  documentId: string,
  content: string,
  contentState?: string,
): void {
  contentById.set(documentId, content)
  if (contentState !== undefined) contentStateById.set(documentId, contentState)
  notifyListenerPresence(documentId)
}

export function clearDocumentContent(documentId: string): void {
  contentById.delete(documentId)
  contentStateById.delete(documentId)
  listenerPresenceListenersById.delete(documentId)
}

export function subscribeToListenerPresence(
  documentId: string,
  listener: DocumentPresenceListener,
): () => void {
  const bucket = listenerPresenceListenersById.get(documentId) ?? new Set()
  bucket.add(listener)
  listenerPresenceListenersById.set(documentId, bucket)
  return () => {
    const current = listenerPresenceListenersById.get(documentId)
    if (!current) return
    current.delete(listener)
    if (!current.size) listenerPresenceListenersById.delete(documentId)
  }
}

export function notifyListenerPresence(documentId: string): void {
  const bucket = listenerPresenceListenersById.get(documentId)
  if (!bucket) return
  for (const listener of [...bucket]) listener(documentId)
}

/** Test / support hatch — wipe registry between cases. */
export function resetDocumentContentEditorState(): void {
  contentById.clear()
  contentStateById.clear()
  listenerPresenceListenersById.clear()
}
