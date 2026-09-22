import { useMemo, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { LoaderCircle } from 'lucide-react'

import './complete-oauth-views.css'

const PROVIDER_LABELS: Record<string, string> = {
  github: 'GitHub',
  gitlab: 'GitLab',
  slack: 'Slack',
  figma: 'Figma',
  sentry: 'Sentry',
}

/** LS-0127 CompleteOAuthView — light success/error flash after server OAuth redirect. */
export function CompleteOAuthView() {
  const location = useLocation()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const provider = (params.get('provider') ?? '').toLowerCase()
  const status = (params.get('status') ?? '').toLowerCase()
  const error = params.get('error')?.trim() ?? ''
  const workspace = params.get('workspace')?.trim() ?? ''
  const label = PROVIDER_LABELS[provider] ?? (provider ? provider[0]!.toUpperCase() + provider.slice(1) : 'integration')
  const settingsPath = workspace ? `/${encodeURIComponent(workspace)}/settings/integrations` : '/'

  const hasCallbackSignal = ['code', 'state', 'installation_id', 'setup_action', 'error', 'status'].some((key) =>
    params.has(key),
  )

  if (!hasCallbackSignal) {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="error" role="alert">
          <h1>Unable to complete connection</h1>
          <p>This authorization link is incomplete or has expired. Start the connection again from Settings → Integrations.</p>
          <div className="oauth-complete-actions">
            <Link to={settingsPath}>Back to integrations</Link>
          </div>
        </div>
      </OAuthCompleteShell>
    )
  }

  if (error || status === 'error') {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="error" role="alert">
          <h1>Unable to complete connection with {label}</h1>
          <p>{error || 'Something went wrong while connecting. Please try again from Settings.'}</p>
          <div className="oauth-complete-actions">
            <Link to={settingsPath}>Back to integrations</Link>
          </div>
        </div>
      </OAuthCompleteShell>
    )
  }

  if (status === 'connected' || status === 'success') {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="success" role="status">
          <h1>{label} connected</h1>
          <p>You can close this window or continue to integration settings.</p>
          <div className="oauth-complete-actions">
            <Link to={settingsPath}>Continue to integrations</Link>
            <button type="button" className="oauth-complete-secondary" onClick={() => window.close()}>
              Close window
            </button>
          </div>
        </div>
      </OAuthCompleteShell>
    )
  }

  return (
    <OAuthCompleteShell>
      <div className="oauth-complete-card" data-state="loading" role="status">
        <LoaderCircle className="oauth-complete-spinner" aria-hidden />
        <p>Completing {label} authorization…</p>
      </div>
    </OAuthCompleteShell>
  )
}

export function OAuthCompleteShell({ children }: { children: ReactNode }) {
  return (
    <div className="oauth-complete-page">
      <header>
        <span className="oauth-complete-brand" aria-hidden />
        <span>Flow</span>
      </header>
      <div className="oauth-complete-body">{children}</div>
    </div>
  )
}
