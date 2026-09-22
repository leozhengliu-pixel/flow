import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { LoaderCircle } from 'lucide-react'

import { finishSentryAuth } from '@/lib/finish-sentry-auth'
import { OAuthCompleteShell } from './complete-oauth-view'

import './complete-oauth-views.css'

/** LS-0128 CompleteSentryAuthView — honest MCP-only until App OAuth exists. */
export function CompleteSentryAuthView() {
  const location = useLocation()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const workspace = params.get('workspace')?.trim() ?? ''
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(true)

  useEffect(() => {
    let active = true
    void finishSentryAuth(location.search).then((result) => {
      if (!active) return
      setPending(false)
      if (!result.ok) setError(result.error)
    })
    return () => {
      active = false
    }
  }, [location.search])

  const agentsPath = workspace
    ? `/${encodeURIComponent(workspace)}/settings/account/agents`
    : '/'
  const integrationsPath = workspace
    ? `/${encodeURIComponent(workspace)}/settings/integrations`
    : '/'

  if (pending) {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="loading" role="status">
          <LoaderCircle className="oauth-complete-spinner" aria-hidden />
          <p>Checking Sentry authorization…</p>
        </div>
      </OAuthCompleteShell>
    )
  }

  return (
    <OAuthCompleteShell>
      <div className="oauth-complete-card" data-state="error" role="alert">
        <span className="oauth-complete-badge">Sentry · MCP only</span>
        <h1>Unable to complete connection with Sentry</h1>
        <p>
          {error ||
            'Sentry App OAuth is not available. Use an MCP connector pointed at https://mcp.sentry.dev/mcp instead of the App install flow.'}
        </p>
        <div className="oauth-complete-actions">
          <Link to={agentsPath}>Open MCP connectors</Link>
          <Link className="oauth-complete-secondary" to={integrationsPath}>
            Integrations catalog
          </Link>
        </div>
      </div>
    </OAuthCompleteShell>
  )
}
