import { useEffect, useRef, useState, type ComponentProps } from 'react'
import { ChevronDown, ChevronUp } from 'lucide-react'
import { createComment, deleteComment, fetchIssueHistory, toggleCommentReaction, updateComment } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { ActivityTimeline } from './activity-timeline'

type Cursor = { commentsCursor?: string; activitiesCursor?: string }
type Page = Awaited<ReturnType<typeof fetchIssueHistory>>

export function PagedActivityTimeline({ issueId, cursors, ...props }: ComponentProps<typeof ActivityTimeline> & { issueId: string; cursors?: Cursor }) {
  const { t } = useI18n()
  const [page, setPage] = useState<Page>()
  const [request, setRequest] = useState<Cursor>()
  const [previous, setPrevious] = useState<(Cursor | undefined)[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const active = useRef<AbortController | undefined>(undefined)
  useEffect(() => { setPage(undefined); setRequest(undefined); setPrevious([]); setLoading(false); return () => active.current?.abort() }, [issueId])
  const next = page ?? cursors
  const changed = () => window.dispatchEvent(new CustomEvent('flow-issue-history-changed', { detail: issueId }))
  const edit: typeof props.onEdit = async (id, body, bodyData) => {
    if (!page) return props.onEdit(id, body, bodyData)
    const saved = await updateComment(issueId, id, body, bodyData, page.comments.find(comment => comment.id === id)?.version)
    setPage(current => current ? { ...current, comments: current.comments.map(comment => comment.id === id ? saved : comment) } : current); changed()
  }
  const remove: typeof props.onDelete = async id => {
    if (!page) return props.onDelete(id)
    await deleteComment(issueId, id)
    setPage(current => current ? { ...current, comments: current.comments.filter(comment => comment.id !== id && comment.parentId !== id) } : current); changed()
  }
  const reply: typeof props.onReply = async (body, bodyData, parentId) => {
    if (!page) return props.onReply(body, bodyData, parentId)
    const saved = await createComment(issueId, body, bodyData, parentId)
    setPage(current => current ? { ...current, comments: [...current.comments, saved] } : current); changed()
  }
  const react: typeof props.onReaction = async (id, emoji) => {
    if (!page) return props.onReaction(id, emoji)
    const saved = await toggleCommentReaction(issueId, id, emoji)
    setPage(current => current ? { ...current, comments: current.comments.map(comment => comment.id === id ? saved : comment) } : current); changed()
  }
  const load = async (target: Cursor | undefined, older: boolean) => {
    if (loading) return
    setLoading(true); setError('')
    const controller = new AbortController(); active.current = controller
    try {
      const result = await fetchIssueHistory(issueId, controller.signal, target)
      if (controller.signal.aborted) return
      setPrevious(current => older ? [...current, request] : current.slice(0, -1))
      setRequest(target); setPage(result)
    } catch (error) { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : String(error)) }
    finally { if (!controller.signal.aborted) setLoading(false) }
  }
  return <>
    {(next?.commentsCursor || next?.activitiesCursor || previous.length > 0) && <nav aria-label={t('Activity pagination')}>
      <button className="show-older-activity" disabled={loading || !next?.commentsCursor && !next?.activitiesCursor} onClick={() => void load({ commentsCursor: next?.commentsCursor, activitiesCursor: next?.activitiesCursor }, true)}><ChevronUp size={12}/>{t('Older activity')}</button>
      {previous.length > 0 && <button className="show-older-activity" disabled={loading} onClick={() => void load(previous[previous.length - 1], false)}><ChevronDown size={12}/>{t('Newer activity')}</button>}
    </nav>}
    {error && <p role="alert">{error}</p>}
    <ActivityTimeline {...props} onEdit={edit} onDelete={remove} onReply={reply} onReaction={react} comments={page?.comments ?? props.comments} events={page?.activities ?? props.events}/>
  </>
}
