import { ThumbsDown, ThumbsUp } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import styles from './agent-toolbar-actions.module.css'

export type AgentToolbarSessionSummary = {
  id: string
  title: string
}

/**
 * LS-0043 AgentToolbarActions — feedback + ask-about-selected-models chrome (no credits/upsell).
 * Hosted in toolbar AgentChatPanel headerExtra / footer.
 */
export function AgentToolbarActions({
  sessions = [],
  activeSessionId,
  onSelectSession,
  onFeedback,
  selectedModelLabels = [],
  onAskAboutSelectedModels,
  className,
}: {
  sessions?: AgentToolbarSessionSummary[]
  activeSessionId?: string
  onSelectSession?: (sessionId: string) => void
  onFeedback?: (value: 'up' | 'down') => void
  selectedModelLabels?: string[]
  onAskAboutSelectedModels?: () => void
  className?: string
}) {
  const { t } = useI18n()
  const multi = sessions.length > 1

  return (
    <div className={[styles.actions, className].filter(Boolean).join(' ')} data-agent-toolbar-actions>
      {multi && onSelectSession && (
        <label className={styles.sessionSwitch}>
          <span className={styles.srOnly}>{t('Sessions')}</span>
          <select
            aria-label={t('Sessions')}
            onChange={event => onSelectSession(event.target.value)}
            value={activeSessionId ?? sessions[0]?.id}
          >
            {sessions.map(session => (
              <option key={session.id} value={session.id}>
                {session.title || t('New chat')}
              </option>
            ))}
          </select>
        </label>
      )}
      {selectedModelLabels.length > 0 && onAskAboutSelectedModels && (
        <button
          className={styles.textButton}
          onClick={onAskAboutSelectedModels}
          type="button"
        >
          {t('Ask about selected')}
          {selectedModelLabels.length > 1 ? ` (${selectedModelLabels.length})` : ''}
        </button>
      )}
      {onFeedback && (
        <div className={styles.feedback} role="group" aria-label={t('Feedback')}>
          <button aria-label={t('Helpful')} onClick={() => onFeedback('up')} type="button">
            <ThumbsUp size={14} />
          </button>
          <button aria-label={t('Not helpful')} onClick={() => onFeedback('down')} type="button">
            <ThumbsDown size={14} />
          </button>
        </div>
      )}
    </div>
  )
}

/** LS-0044 multi-session helpers for lazy toolbar host. */
export function listOpenToolbarSessions<T extends { id: string; location: string }>(
  sessions: T[] | undefined,
  closedIds: Set<string>,
): T[] {
  return (sessions ?? []).filter(session => session.location === 'toolbar' && !closedIds.has(session.id))
}

export function hasOpenToolbarSessions(
  sessions: Array<{ id: string; location: string }> | undefined,
  closedIds: Set<string>,
): boolean {
  return listOpenToolbarSessions(sessions, closedIds).length > 0
}
