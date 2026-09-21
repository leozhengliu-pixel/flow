/**
 * LS-0209 DocumentAgentChangesState — client store for document-agent change
 * availability / highlight visibility (Linear counterpart, REST-safe local state).
 */

export type DocumentAgentChangeAvailability = 'cleared' | 'pending' | 'active'
export type DocumentAgentChangeAnimationKind = 'iteration' | 'checkpoint'

export type DocumentAgentChangeEntry = {
  visible: boolean
  availability: DocumentAgentChangeAvailability
  animationKind: DocumentAgentChangeAnimationKind
}

const defaultEntry = (): DocumentAgentChangeEntry => ({
  visible: false,
  availability: 'cleared',
  animationKind: 'iteration',
})

type Listener = () => void

class DocumentAgentChangesStateStore {
  private states = new Map<string, DocumentAgentChangeEntry>()
  private pendingCheckpointOperationDocumentContentIds = new Set<string>()
  private listeners = new Set<Listener>()

  subscribe(listener: Listener) {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  private emit() {
    for (const listener of this.listeners) listener()
  }

  visibleFor(documentContentId: string | undefined) {
    return !!(documentContentId && this.states.get(documentContentId)?.visible)
  }

  availabilityFor(documentContentId: string | undefined): DocumentAgentChangeAvailability {
    return (documentContentId ? this.states.get(documentContentId)?.availability : undefined) ?? 'cleared'
  }

  animationKindFor(documentContentId: string | undefined): DocumentAgentChangeAnimationKind {
    return (documentContentId ? this.states.get(documentContentId)?.animationKind : undefined) ?? 'iteration'
  }

  setPending(documentContentId: string) {
    const current = this.getState(documentContentId)
    this.states.set(documentContentId, { ...current, availability: 'pending' })
    this.emit()
  }

  setActive(documentContentId: string, animationKind: DocumentAgentChangeAnimationKind) {
    const current = this.states.get(documentContentId)
    if (current?.availability === 'active' && current.animationKind === animationKind && current.visible) return
    this.states.set(documentContentId, {
      availability: 'active',
      animationKind,
      visible: true,
    })
    this.emit()
  }

  setCleared(documentContentId: string) {
    this.states.delete(documentContentId)
    this.emit()
  }

  setCheckpointOperationPending(documentContentId: string) {
    this.pendingCheckpointOperationDocumentContentIds.add(documentContentId)
    this.emit()
  }

  clearCheckpointOperationPending(documentContentId: string) {
    this.pendingCheckpointOperationDocumentContentIds.delete(documentContentId)
    this.emit()
  }

  hasPendingCheckpointOperation(documentContentId: string) {
    return this.pendingCheckpointOperationDocumentContentIds.has(documentContentId)
  }

  syncEditorHighlightVisible(documentContentId: string, visible: boolean) {
    const current = this.getState(documentContentId)
    if (visible) {
      if (current.visible && current.availability === 'active') return
      this.states.set(documentContentId, {
        ...current,
        visible: true,
        availability: 'active',
      })
      this.emit()
      return
    }
    if (current.availability !== 'pending') {
      this.states.delete(documentContentId)
      this.emit()
    }
  }

  getState(documentContentId: string): DocumentAgentChangeEntry {
    return this.states.get(documentContentId) ?? defaultEntry()
  }

  /** Test / reset helper */
  reset() {
    this.states.clear()
    this.pendingCheckpointOperationDocumentContentIds.clear()
    this.emit()
  }
}

/** Singleton matching Linear's module-level DocumentAgentChangesState. */
export const documentAgentChangesState = new DocumentAgentChangesStateStore()
