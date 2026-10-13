import styles from './agent-badge.module.css'

/** The reference app's "Agent" pill: marks agent members wherever people are listed. The word stays untranslated. */
export function AgentBadge({ className }: { className?: string }) {
  return <span className={className ? `${styles.badge} ${className}` : styles.badge} data-agent-badge="" data-i18n-ignore>Agent</span>
}
