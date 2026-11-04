import { Check, CircleDashed, Link2, ListPlus, Signal, Sparkles, Tag, UserRound, X } from 'lucide-react'
import type { CommentAction } from '@/lib/comment-actions'
import type { useCommentActions } from '@/hooks/use-comment-actions'
import { useI18n } from '@/i18n/i18n'
import type { ActivityContext } from './issue-activity-model'
import './comment-actions.css'

const glyphs = { status: CircleDashed, assignee: UserRound, priority: Signal, labels: Tag, subIssue: ListPlus, link: Link2 }

export function CommentActions({ commentId, model, context }: { commentId: string; model: ReturnType<typeof useCommentActions>; context?: ActivityContext }) {
  const { t } = useI18n()
  const actions = model.actions.filter(action => action.commentId === commentId && action.status !== 'dismissed')
  if (!actions.length) return null
  const label = (action: CommentAction) => {
    const a = action.arguments
    switch (action.action) {
      case 'status': return t('Change status to {value}').replace('{value}', () => t(context?.states.find(s => s.id === a.stateId)?.name ?? 'Status'))
      case 'assignee': return a.assigneeId ? t('Assign to {value}').replace('{value}', () => context?.users.find(u => u.id === a.assigneeId)?.displayName ?? t('Member')) : t('Remove assignee')
      case 'priority': return t('Set priority to {value}').replace('{value}', () => t(['No priority', 'Urgent', 'High', 'Medium', 'Low'][a.priority ?? 0]))
      case 'labels': return a.labelIds?.length ? t('Set labels to {value}').replace('{value}', () => a.labelIds!.map(id => context?.labels.find(l => l.id === id)?.name ?? t('Label')).join(', ')) : t('Remove labels')
      case 'subIssue': return t('Create sub-issue: {value}').replace('{value}', () => a.title ?? '')
      case 'link': return t('Add link: {value}').replace('{value}', () => a.title || a.url || '')
    }
  }
  return <div className="comment-suggested-actions" aria-label={t('Suggested actions')}>
    <span className="comment-suggested-actions__heading"><Sparkles size={14} aria-hidden="true" />{t('Suggested actions')}</span>
    {actions.map(action => {
      const Icon = glyphs[action.action]
      const accepted = action.status === 'accepted'
      const busy = model.busy.has(action.id)
      return <div className="comment-suggested-action" key={action.id} data-status={action.status}>
        <Icon size={14} aria-hidden="true" />
        <span className="comment-suggested-action__label" data-i18n-ignore>{label(action)}</span>
        {accepted ? <span className="comment-suggested-action__accepted" role="status"><Check size={14} aria-hidden="true" />{t('Accepted')}</span> : <div className="comment-suggested-action__controls">
          <button type="button" className="comment-suggested-action__accept" disabled={busy} onClick={() => void model.resolve(action, 'accepted')}><Check size={14} aria-hidden="true" />{t('Accept')}</button>
          <button type="button" title={t('Dismiss suggestion')} aria-label={t('Dismiss suggestion')} disabled={busy} onClick={() => void model.resolve(action, 'dismissed')}><X size={14} aria-hidden="true" /></button>
        </div>}
        {model.error === action.id && <span className="comment-suggested-action__error" role="alert">{t('Could not update suggestion. Try again.')}</span>}
      </div>
    })}
  </div>
}
