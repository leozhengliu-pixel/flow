import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

export type CommentsEditValue = {
  editingId: string | null
  isEditing: (commentId: string) => boolean
  isDirty: (commentId: string) => boolean
  startEditing: (commentId: string, initialBody?: string) => void
  setDraft: (commentId: string, body: string) => void
  cancel: (commentId?: string) => void
  markClean: (commentId: string) => void
  onEditorReadyStateChange: (commentId: string, ready: boolean) => void
  isEditorReady: (commentId: string) => boolean
  validateNoOpSuggestionOrToast: (body: string, original: string) => boolean
}

const defaultValue: CommentsEditValue = {
  editingId: null,
  isEditing: () => false,
  isDirty: () => false,
  startEditing: () => undefined,
  setDraft: () => undefined,
  cancel: () => undefined,
  markClean: () => undefined,
  onEditorReadyStateChange: () => undefined,
  isEditorReady: () => false,
  validateNoOpSuggestionOrToast: () => true,
}

const CommentsEditContext = createContext<CommentsEditValue>(defaultValue)

export function CommentsEditProvider({ children }: { children: ReactNode }) {
  const value = useCommentsEditState()
  return <CommentsEditContext.Provider value={value}>{children}</CommentsEditContext.Provider>
}

export function useCommentsEdit() {
  return useContext(CommentsEditContext)
}

/** Shared dirty/edit kit for activity + inline comment editors (LS-0726). */
export function useCommentsEditState(): CommentsEditValue {
  const [editingId, setEditingId] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, { body: string; original: string }>>({})
  const [ready, setReady] = useState<Record<string, boolean>>({})

  const startEditing = useCallback((commentId: string, initialBody = '') => {
    setEditingId(commentId)
    setDrafts((current) => ({ ...current, [commentId]: { body: initialBody, original: initialBody } }))
  }, [])

  const setDraft = useCallback((commentId: string, body: string) => {
    setDrafts((current) => {
      const existing = current[commentId]
      return { ...current, [commentId]: { body, original: existing?.original ?? body } }
    })
  }, [])

  const cancel = useCallback((commentId?: string) => {
    const id = commentId ?? editingId
    setEditingId((current) => (id && current === id ? null : current))
    if (id) {
      setDrafts((current) => {
        const next = { ...current }
        delete next[id]
        return next
      })
    }
  }, [editingId])

  const markClean = useCallback((commentId: string) => {
    setDrafts((current) => {
      const draft = current[commentId]
      if (!draft) return current
      return { ...current, [commentId]: { body: draft.body, original: draft.body } }
    })
    setEditingId((current) => (current === commentId ? null : current))
  }, [])

  const onEditorReadyStateChange = useCallback((commentId: string, nextReady: boolean) => {
    setReady((current) => ({ ...current, [commentId]: nextReady }))
  }, [])

  const validateNoOpSuggestionOrToast = useCallback((body: string, original: string) => {
    if (body.trim() === original.trim()) {
      toast.message('No changes to save')
      return false
    }
    return true
  }, [])

  return useMemo(() => ({
    editingId,
    isEditing: (commentId: string) => editingId === commentId,
    isDirty: (commentId: string) => {
      const draft = drafts[commentId]
      return Boolean(draft && draft.body !== draft.original)
    },
    startEditing,
    setDraft,
    cancel,
    markClean,
    onEditorReadyStateChange,
    isEditorReady: (commentId: string) => Boolean(ready[commentId]),
    validateNoOpSuggestionOrToast,
  }), [editingId, drafts, ready, startEditing, setDraft, cancel, markClean, onEditorReadyStateChange, validateNoOpSuggestionOrToast])
}
