import { RotateCw } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { AGENT_STALE_LABELS, type AgentReplyStale } from './agent-reply-status'
import styles from './agent-stale-reply.module.css'

/**
 * The Agent panel's status for a chat whose latest reply was interrupted (agent-reply-status.ts): "Stopped
 * responding" once the reply had started; "Agent didn't start" with "The request may not have reached the agent"
 * otherwise, with Retry only when running the turn again cannot repeat a write.
 */
export function AgentStaleReplyStatus({ stale, onRetry }: { stale: AgentReplyStale | undefined; onRetry?: () => void }) {
  const { t } = useI18n()
  if (!stale) return null
  if (stale.started) {
    return <div className={styles.stale} data-agent-reply-stale="started" role="status"><strong>{t(AGENT_STALE_LABELS.stoppedResponding)}</strong></div>
  }
  return (
    <div className={styles.stale} data-agent-reply-stale="not-started" role="status">
      <strong>{t(AGENT_STALE_LABELS.panelDidntStart)}</strong>
      <span>{t(AGENT_STALE_LABELS.panelDidntStartDetail)}</span>
      {stale.retrySafe && onRetry && <button className={styles.retry} onClick={onRetry} type="button"><RotateCw size={12}/>{t('Retry')}</button>}
    </div>
  )
}

/** The chat status label of a stale chat ("Stopped responding" / "Didn't start"), for lists and headers. */
export function AgentStaleLabel({ stale, className }: { stale: AgentReplyStale | undefined; className?: string }) {
  const { t } = useI18n()
  if (!stale) return null
  return <span className={`${styles.label}${className ? ` ${className}` : ''}`} data-agent-reply-stale={stale.started ? 'started' : 'not-started'}>{t(stale.label)}</span>
}
