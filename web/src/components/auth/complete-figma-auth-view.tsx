import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { LoaderCircle } from 'lucide-react'

import { finishFigmaAuth } from '@/lib/finish-figma-auth'
import { OAuthCompleteShell } from './complete-oauth-view'

import './complete-oauth-views.css'

/** LS-0126 CompleteFigmaAuthView */
export function CompleteFigmaAuthView() {
  const location = useLocation()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const status = (params.get('status') ?? '').toLowerCase()
  const flashError = params.get('error')?.trim() ?? ''
  const workspace = params.get('workspace')?.trim() ?? ''
  const [error, setError] = useState<string | null>(flashError || (status === 'error' ? 'Unable to complete connection with Figma' : null))
  const [done, setDone] = useState(status === 'connected' || status === 'success')
  const [pending, setPending] = useState(!done && !error && Boolean(params.get('code')))

  useEffect(() => {
    if (done || error || !params.get('code')) return
    let active = true
    setPending(true)
    void finishFigmaAuth(location.search).then((result) => {
      if (!active) return
      setPending(false)
      if (result.ok) {
        setDone(true)
        // Popup / desktop companion windows close themselves after success.
        window.setTimeout(() => {
          try {
            window.close()
          } catch {
            /* ignore */
          }
        }, 400)
      } else {
        setError(result.error || 'Unable to complete connection with Figma')
      }
    })
    return () => {
      active = false
    }
  }, [done, error, location.search, params])

  const settingsPath = workspace ? `/${encodeURIComponent(workspace)}/settings/integrations` : '/'

  if (error) {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="error" role="alert">
          <h1>Unable to complete connection with Figma</h1>
          <p>{error}</p>
          <div className="oauth-complete-actions">
            <Link to={settingsPath}>Back to integrations</Link>
          </div>
        </div>
      </OAuthCompleteShell>
    )
  }

  if (done) {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="success" role="status">
          <h1>Figma connected</h1>
          <p>Design previews can now link to issues. You can close this window.</p>
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

  if (pending || params.get('code')) {
    return (
      <OAuthCompleteShell>
        <div className="oauth-complete-card" data-state="loading" role="status">
          <LoaderCircle className="oauth-complete-spinner" aria-hidden />
          <p>Completing Figma authorization…</p>
        </div>
      </OAuthCompleteShell>
    )
  }

  return (
    <OAuthCompleteShell>
      <div className="oauth-complete-card" data-state="error" role="alert">
        <h1>Unable to complete connection with Figma</h1>
        <p>This authorization link is incomplete. Connect Figma again from Settings → Integrations when client credentials are configured.</p>
        <div className="oauth-complete-actions">
          <Link to={settingsPath}>Back to integrations</Link>
        </div>
      </div>
    </OAuthCompleteShell>
  )
}
