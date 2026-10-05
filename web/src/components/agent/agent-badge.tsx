import styles from './agent-badge.module.css'

/** Linear's "Agent" pill: marks agent members wherever people are listed. The word stays untranslated. */
export function AgentBadge({ className }: { className?: string }) {
  return <span className={className ? `${styles.badge} ${className}` : styles.badge} data-agent-badge="" data-i18n-ignore>Agent</span>
}

/** A person label followed by the "Agent" pill (for `labelContent` slots). */
export function AgentLabel({ label }: { label: string }) {
  return <><span data-i18n-ignore>{label}</span><AgentBadge/></>
}

