import { useMemo } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import './auth-page.css'

/** Unified /auth/error surface for OIDC / token / SSO bounce failures (LS-0081 / LS-0083). */
export function AuthErrorPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const stateError =
    location.state && typeof location.state === 'object' && 'error' in location.state
      ? String((location.state as { error?: string }).error ?? '')
      : ''
  const error =
    params.get('error') ||
    stateError ||
    'Something went wrong, please try again.'

  return (
    <main className="auth-page">
      <div className="auth-brand">
        <span className="auth-brand-mark" />
        Flow
      </div>
      <section className="auth-panel">
        <h1>Authentication error</h1>
        <p>We could not finish signing you in.</p>
        <div className="auth-error" role="alert">
          {error}
        </div>
        <button className="auth-primary auth-continue" type="button" onClick={() => navigate('/login', { replace: true })}>
          Back to login
        </button>
      </section>
      <footer>
        <a href="https://flow.app/privacy" rel="noreferrer" target="_blank">
          Privacy
        </a>
        <span>·</span>
        <a href="https://flow.app/terms" rel="noreferrer" target="_blank">
          Terms
        </a>
      </footer>
    </main>
  )
}

export default AuthErrorPage
