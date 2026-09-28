import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { LoaderCircle } from 'lucide-react'

import { startIntegrationOAuth } from '@/lib/api'
import {
  FIGMA_OAUTH_STATE_KEY,
  figmaAuthUrl,
  writeFigmaOAuthState,
} from '@/lib/figma-auth'
import { OAuthCompleteShell } from './complete-oauth-view'

import './complete-oauth-views.css'

/**
 * LS-0078 AuthDesktopRedirectFigma
 * No Electron build yet — browser path starts (or explains) Figma OAuth.
 */
export function AuthDesktopRedirectFigma() {
  const location = useLocation()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const workspace = params.get('workspace')?.trim() ?? params.get('urlKey')?.trim() ?? ''
  const [message, setMessage] = useState('Signing in…')
  const [error, setError] = useState('')
  const [authorizationURL, setAuthorizationURL] = useState('')

  useEffect(() => {
    let active = true
    void (async () => {
      try {
        // Prefer server oauth/start so state is single-use and credentials stay server-side.
        const started = await startIntegrationOAuth('figma')
        if (!active) return
        if (workspace) {
          writeFigmaOAuthState({ key: started.state, urlKey: workspace })
        } else {
          sessionStorage.setItem(FIGMA_OAUTH_STATE_KEY, JSON.stringify({ key: started.state, urlKey: 'workspace' }))
        }
        setAuthorizationURL(started.authorizationURL)
        window.location.assign(started.authorizationURL)
      } catch (err) {
        if (!active) return
        const text = err instanceof Error ? err.message : 'Figma OAuth is unavailable'
        setError(text)
        setMessage('Figma OAuth is not configured')
      }
    })()
    return () => {
      active = false
    }
  }, [workspace])

  const openFlow = () => {
    if (authorizationURL) {
      window.location.assign(authorizationURL)
      return
    }
    const target = workspace ? `/${encodeURIComponent(workspace)}/settings/integrations` : '/'
    window.location.assign(target)
  }

  return (
    <OAuthCompleteShell>
      <div className="oauth-complete-card" data-state={error ? 'error' : 'loading'} role={error ? 'alert' : 'status'}>
        <span className="oauth-complete-badge">Figma · browser</span>
        {!error && <LoaderCircle className="oauth-complete-spinner" aria-hidden />}
        <h1>{message}</h1>
        <p>
          {error
            ? `${error} Set FLOW_INTEGRATION_FIGMA_CLIENT_ID / CLIENT_SECRET (and optional REDIRECT_URI) to enable Figma OAuth, or connect later from Settings.`
            : 'Continue in this browser to authorize Figma. Desktop deep-link redirect is reserved for a future Electron client.'}
        </p>
        <div className="oauth-complete-actions">
          <button type="button" onClick={openFlow}>
            Open Flow
          </button>
          {workspace && (
            <Link className="oauth-complete-secondary" to={`/${encodeURIComponent(workspace)}/settings/integrations`}>
              Integrations settings
            </Link>
          )}
        </div>
      </div>
    </OAuthCompleteShell>
  )
}

/** Exported for tests — builds a local Figma authorize URL when client id is known. */
export function buildBrowserFigmaAuthorizeURL(clientId: string, redirectUri: string, urlKey: string, key: string) {
  writeFigmaOAuthState({ key, urlKey })
  return figmaAuthUrl({ clientId, redirectUri, state: { key, urlKey } })
}
