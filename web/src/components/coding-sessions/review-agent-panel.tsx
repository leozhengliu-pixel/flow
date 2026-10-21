/* oxlint-disable react/only-export-components -- the panel, its toggle and the hook that finds the review's session belong together. */
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, CodeReview } from '@/types/flow'
import { CodingSessionErrorBoundary } from './coding-session-error-boundary'
import { reviewForPullRequest } from './coding-session-model'
import { CodingAgentPanel } from './coding-session-panel'
import { useCodingAvailability, useCodingSession, useCodingSessions, useDismissedCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

/**
 * The agent panel on a pull request's review page (`/review/<slug>/changes?showAgent=true&agentSessionId=<id>`), like
 * the reference app's session view: the review's Overview / Guide / Diff on the left, the session's conversation and
 * composer on the right. "Close chat" and Archive close the panel.
 */
export function ReviewAgentPanel({ sessionId, data, onClose, onOpenChanges }: { sessionId: string; data: BootstrapData; onClose: () => void; /** The end-of-turn diff chip opens the Diff tab. */ onOpenChanges?: () => void }) {
  const { t } = useI18n()
  const available = useCodingAvailability(data)
  const { session, error, apply } = useCodingSession(available ? sessionId : '')
  const { dismiss } = useDismissedCodingSessions(data.workspace.urlKey, data.viewer.id)
  if (!available) return null
  if (!session) return <aside className="coding-agent-panel review-agent-panel" aria-label={t('Agent')}>
    {error ? <p className="coding-session-error-text" role="alert">{error}</p> : <p className="coding-session-muted" role="status">{t('Loading coding session…')}</p>}
  </aside>
  return <CodingSessionErrorBoundary className="coding-agent-panel review-agent-panel">
    <CodingAgentPanel session={session} data={data} onChange={apply} onClose={onClose} onOpenChanges={onOpenChanges} className="review-agent-panel"
      onDismiss={() => { dismiss(session.id); onClose() }}/>
  </CodingSessionErrorBoundary>
}

/**
 * The coding session that opened (or pushed to) this pull request, found through the review's linked issue, so the
 * review page offers "Show agent" even when its URL names no session (opened from the Reviews list or the issue).
 */
export function useReviewCodingSessionId(review: Pick<CodeReview, 'id' | 'issueIds' | 'provider' | 'number' | 'repositoryOwner' | 'repositoryName'> | undefined, data: BootstrapData, enabled = true): string | undefined {
  const issueId = review?.issueIds?.[0]
  const available = useCodingAvailability(data, enabled && Boolean(issueId))
  const { sessions } = useCodingSessions({ issueId }, available && Boolean(issueId))
  if (!review || !available) return undefined
  const linked = sessions
    .filter(session => (session.pullRequests ?? []).some(pr => reviewForPullRequest(pr, [review as CodeReview])?.id === review.id))
    .sort((a, b) => (Date.parse(b.createdAt) || 0) - (Date.parse(a.createdAt) || 0))
  return linked[0]?.id
}

/** "Hide agent" / "Show agent" at the end of the review's tabs row. */
export function ReviewAgentToggle({ shown, onToggle }: { shown: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  const label = t(shown ? 'Hide agent' : 'Show agent')
  return <button type="button" className="coding-session-agent-toggle review-agent-toggle" aria-pressed={shown} aria-label={label} title={label} onClick={onToggle}><AgentCursorGlyph size={14}/></button>
}
