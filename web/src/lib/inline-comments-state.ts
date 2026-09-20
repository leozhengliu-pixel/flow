/** TipTap/ProseMirror-style inline comment thread marks for document/issue editors (LS-0704). */

export type InlineCommentParentType = 'issue' | 'document'

export type InlineCommentMark = {
  id: string
  commentId: string
  parentId: string
  parentType: InlineCommentParentType
  from: number
  to: number
  resolved?: boolean
  createdBy?: string
}

export type InlineCommentsSnapshot = {
  marks: InlineCommentMark[]
  activeCommentId?: string
  expandedThreadIds: string[]
}

type Listener = () => void

function keyFor(parentType: InlineCommentParentType, parentId: string) {
  return `${parentType}:${parentId}`
}

class InlineCommentsStateStore {
  private byParent = new Map<string, InlineCommentsSnapshot>()
  private listeners = new Set<Listener>()

  subscribe = (listener: Listener) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private emit() {
    for (const listener of this.listeners) listener()
  }

  private ensure(parentType: InlineCommentParentType, parentId: string): InlineCommentsSnapshot {
    const key = keyFor(parentType, parentId)
    let snapshot = this.byParent.get(key)
    if (!snapshot) {
      snapshot = { marks: [], expandedThreadIds: [] }
      this.byParent.set(key, snapshot)
    }
    return snapshot
  }

  getSnapshot(parentType: InlineCommentParentType, parentId: string): InlineCommentsSnapshot {
    const snapshot = this.ensure(parentType, parentId)
    return {
      marks: [...snapshot.marks],
      activeCommentId: snapshot.activeCommentId,
      expandedThreadIds: [...snapshot.expandedThreadIds],
    }
  }

  setMarks(parentType: InlineCommentParentType, parentId: string, marks: InlineCommentMark[]) {
    const snapshot = this.ensure(parentType, parentId)
    snapshot.marks = marks.map((mark) => ({ ...mark, parentId, parentType }))
    this.emit()
  }

  addMark(mark: InlineCommentMark) {
    const snapshot = this.ensure(mark.parentType, mark.parentId)
    snapshot.marks = [...snapshot.marks.filter((item) => item.id !== mark.id && item.commentId !== mark.commentId), mark]
    snapshot.activeCommentId = mark.commentId
    this.emit()
  }

  removeMark(parentType: InlineCommentParentType, parentId: string, commentId: string) {
    const snapshot = this.ensure(parentType, parentId)
    snapshot.marks = snapshot.marks.filter((mark) => mark.commentId !== commentId)
    if (snapshot.activeCommentId === commentId) snapshot.activeCommentId = undefined
    snapshot.expandedThreadIds = snapshot.expandedThreadIds.filter((id) => id !== commentId)
    this.emit()
  }

  resolveMark(parentType: InlineCommentParentType, parentId: string, commentId: string, resolved = true) {
    const snapshot = this.ensure(parentType, parentId)
    snapshot.marks = snapshot.marks.map((mark) => (mark.commentId === commentId ? { ...mark, resolved } : mark))
    this.emit()
  }

  activate(parentType: InlineCommentParentType, parentId: string, commentId?: string) {
    const snapshot = this.ensure(parentType, parentId)
    snapshot.activeCommentId = commentId
    if (commentId && !snapshot.expandedThreadIds.includes(commentId)) {
      snapshot.expandedThreadIds = [...snapshot.expandedThreadIds, commentId]
    }
    this.emit()
  }

  toggleThreadExpanded(parentType: InlineCommentParentType, parentId: string, commentId: string) {
    const snapshot = this.ensure(parentType, parentId)
    snapshot.expandedThreadIds = snapshot.expandedThreadIds.includes(commentId)
      ? snapshot.expandedThreadIds.filter((id) => id !== commentId)
      : [...snapshot.expandedThreadIds, commentId]
    this.emit()
  }

  isThreadExpanded(parentType: InlineCommentParentType, parentId: string, commentId: string) {
    return this.ensure(parentType, parentId).expandedThreadIds.includes(commentId)
  }

  activeUnresolvedCount(parentType: InlineCommentParentType, parentId: string) {
    return this.ensure(parentType, parentId).marks.filter((mark) => !mark.resolved).length
  }

  reset(parentType?: InlineCommentParentType, parentId?: string) {
    if (parentType && parentId) this.byParent.delete(keyFor(parentType, parentId))
    else this.byParent.clear()
    this.emit()
  }
}

/** Shared module store — document/issue editors register TipTap mark ranges here. */
export const inlineCommentsState = new InlineCommentsStateStore()
