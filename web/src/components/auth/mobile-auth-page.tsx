import { useMemo } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import './auth-page.css'

/**
 * Mobile-ready auth handoff stub (LS-0412/0413 adjacent).
 * Web shows an honest completion page until a native scheme exists.
 */
export function MobileAuthPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const redirectUri = params.get('redirectUri') || params.get('redirect_uri') || ''

  return (
    <main className="auth-page">
      <div className="auth-brand">
        <span className="auth-brand-mark" />
        Flow
      </div>
      <section className="auth-panel">
        <h1>Open Flow to finish signing in</h1>
        <p>
          {redirectUri
            ? 'Continue in the Flow mobile app to complete authentication.'
            : 'Authentication link is ready. Install the Flow mobile app to finish on a device.'}
        </p>
        {redirectUri ? (
          <a className="auth-primary" href={redirectUri}>
            Open Flow
          </a>
        ) : (
          <button className="auth-primary" type="button" onClick={() => navigate('/', { replace: true })}>
            Continue in browser
          </button>
        )}
        <button className="auth-text-button" type="button" onClick={() => navigate('/login', { replace: true })}>
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

export default MobileAuthPage
