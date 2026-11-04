import { useCallback, useEffect, useRef, useState } from 'react'
import { COMMENT_ACTION_RESOLVED, COMMENT_ACTIONS_CHANGED, listCommentActions, resolveCommentAction, type CommentAction } from '@/lib/comment-actions'
import { currentWorkspaceKey } from '@/lib/api-client'

export function useCommentActions(issueId: string | undefined, commentKey: string, viewerId: string) {
  const scope = `${currentWorkspaceKey()}:${viewerId}:${issueId ?? ''}`
  const [actions, setActions] = useState<CommentAction[]>([])
  const [loadedScope, setLoadedScope] = useState('')
  const [busy, setBusy] = useState<Set<string>>(new Set())
  const [error, setError] = useState<string>()
  const pending = useRef(new Set<string>())
  const revision = useRef(0)
  useEffect(() => {
    let active: AbortController | undefined
    const load = async () => {
      if (!issueId) return
      active?.abort()
      const controller = new AbortController()
      active = controller
      const version = ++revision.current
      try {
        const next = await listCommentActions(issueId, controller.signal)
        if (!controller.signal.aborted && version === revision.current) {
          setLoadedScope(scope)
          setActions(current => next.map(item => pending.current.has(item.id) ? current.find(action => action.id === item.id) ?? item : item))
        }
      } catch {
        if (!controller.signal.aborted && version === revision.current) {
          setLoadedScope(scope)
          setActions([])
        }
      }
    }
    void load()
    const changed = (event: Event) => { const id = (event as CustomEvent<string | null | undefined>).detail; if (id === undefined || id === null || id === issueId) void load() }
    window.addEventListener(COMMENT_ACTIONS_CHANGED, changed)
    return () => { active?.abort(); window.removeEventListener(COMMENT_ACTIONS_CHANGED, changed) }
  }, [issueId, commentKey, scope])
  const resolve = useCallback(async (action: CommentAction, status: 'accepted' | 'dismissed') => {
    if (!issueId || pending.current.has(action.id)) return
    const workspaceKey = currentWorkspaceKey()
    pending.current.add(action.id)
    revision.current++
    setBusy(new Set(pending.current))
    setError(undefined)
    setActions(current => current.map(item => item.id === action.id ? { ...item, status } : item))
    try {
      const result = await resolveCommentAction(issueId, action.id, status)
      revision.current++
      setActions(current => current.map(item => item.id === action.id ? result.suggestion : item))
      window.dispatchEvent(new CustomEvent(COMMENT_ACTION_RESOLVED, { detail: { issue: result.issue, workspaceKey, viewerId } }))
      if (currentWorkspaceKey() === workspaceKey) window.dispatchEvent(new CustomEvent('flow-issue-history-changed', { detail: issueId }))
    } catch {
      setActions(current => current.some(item => item.id === action.id) ? current.map(item => item.id === action.id ? action : item) : [...current, action])
      setError(action.id)
      window.dispatchEvent(new CustomEvent(COMMENT_ACTIONS_CHANGED, { detail: issueId }))
    } finally {
      pending.current.delete(action.id)
      setBusy(new Set(pending.current))
    }
  }, [issueId, viewerId])
  return { actions: loadedScope === scope ? actions.filter(action => action.issueId === issueId) : [], busy, error, resolve }
}

