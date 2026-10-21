import { Component, Fragment, type ErrorInfo, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'

type Props = { children: ReactNode; className?: string }

/**
 * Keeps a render failure inside a coding session view: the host page stays up and the view shows an inline error with
 * Retry (which remounts the children).
 */
export class CodingSessionErrorBoundary extends Component<Props, { error?: Error; attempt: number }> {
  state: { error?: Error; attempt: number } = { attempt: 0 }
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('Coding session view failed to render', error, info) }
  render() {
    if (this.state.error) return <CodingSessionRenderError className={this.props.className} onRetry={() => this.setState(state => ({ error: undefined, attempt: state.attempt + 1 }))}/>
    return <Fragment key={this.state.attempt}>{this.props.children}</Fragment>
  }
}

function CodingSessionRenderError({ className, onRetry }: { className?: string; onRetry: () => void }) {
  const { t } = useI18n()
  return <p className={`coding-session-error-text${className ? ` ${className}` : ''}`} role="alert">
    <span>{t('Couldn’t show this coding session.')}</span>{' '}
    <button type="button" className="cs-button is-ghost is-small" onClick={onRetry}>{t('Retry')}</button>
  </p>
}
