import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'
import styles from './agent-panel-layout.module.css'

/** LS-0034 AgentPanelLayout — error boundary chrome for dockable agent panels. */
export class AgentPanelErrorBoundary extends Component<
  { children: ReactNode; name?: string; onRequestClose?: () => void },
  { hasError: boolean; retries: number }
> {
  state = { hasError: false, retries: 0 }

  static getDerivedStateFromError() {
    return { hasError: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`${this.props.name ?? 'AgentPanel'}: AgentPanelErrorBoundary caught an error`, error, info)
  }

  private handleRetry = () => {
    this.setState(current => ({ hasError: false, retries: current.retries + 1 }))
  }

  render() {
    if (this.state.hasError) {
      return (
        <AgentPanelErrorFallback
          onRetry={this.handleRetry}
          onRequestClose={this.props.onRequestClose}
        />
      )
    }
    return <div key={this.state.retries}>{this.props.children}</div>
  }
}

function AgentPanelErrorFallback({
  onRetry,
  onRequestClose,
}: {
  onRetry: () => void
  onRequestClose?: () => void
}) {
  const { t } = useI18n()
  return (
    <div className={styles.error} role="alert">
      <strong>{t('Something went wrong')}</strong>
      <p>{t('An error occurred while loading the agent panel.')}</p>
      <div className={styles.errorActions}>
        {onRequestClose && (
          <button type="button" onClick={onRequestClose}>
            {t('Close')}
          </button>
        )}
        <button type="button" className={styles.primary} onClick={onRetry}>
          {t('Try again')}
        </button>
      </div>
    </div>
  )
}

export function AgentPanelLayout({
  children,
  onRequestClose,
  name = 'AgentPanel',
}: {
  children: ReactNode
  onRequestClose?: () => void
  name?: string
}) {
  return (
    <AgentPanelErrorBoundary name={name} onRequestClose={onRequestClose}>
      {children}
    </AgentPanelErrorBoundary>
  )
}
