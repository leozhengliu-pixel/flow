import { useEffect, useMemo } from 'react'
import { LoaderCircle } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import { tokenAuthLogin } from '@/lib/api'
import type { AuthSession } from '@/types/flow'

import './auth-page.css'

type Props = {
  onAuthenticated: (session: AuthSession, returnTo?: string) => Promise<void>
}

/**
 * LS-0083 AuthToken — email + authToken login / forceReauth path for magic-link and SSO bounce.
 * Also covers thin LS-0077 desktop email handoff via /auth/email.
 */
export function AuthTokenPage({ onAuthenticated }: Props) {
  const location = useLocation()
  const navigate = useNavigate()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const message = 'Signing in…'

  const service =
    location.pathname.startsWith('/auth/saml') || location.pathname.startsWith('/auth/web-saml')
      ? 'saml'
      : 'email'

  useEffect(() => {
    const email = params.get('email') ?? ''
    const token = params.get('token') ?? params.get('authToken') ?? ''
    const forceReauth =
      params.get('forceReauth') === '1' ||
      params.get('forceReauth') === 'true' ||
      params.has('force_reauth')
    const inviteLink = params.get('inviteLink') ?? params.get('invite') ?? undefined
    const isMobileAppLogin =
      params.get('isMobileAppLogin') === '1' ||
      params.get('mobile') === '1' ||
      Boolean(params.get('mobileRedirectUri'))

    if (!email || !token) {
      navigate('/auth/error', {
        replace: true,
        state: { error: 'Invalid auth token parameters' },
      })
      return
    }

    let cancelled = false
    void (async () => {
      try {
        const session = await tokenAuthLogin({
          email,
          authToken: token,
          service,
          inviteLink,
          forceReauth,
        })
        if (cancelled) return
        if (isMobileAppLogin) {
          const redirectUri = params.get('mobileRedirectUri') ?? params.get('redirectUri') ?? ''
          const search = redirectUri ? `?redirectUri=${encodeURIComponent(redirectUri)}` : ''
          await onAuthenticated(session, `/mobile-auth${search}`)
          return
        }
        await onAuthenticated(session, '/')
      } catch (error) {
        if (cancelled) return
        navigate('/auth/error', {
          replace: true,
          state: {
            error: error instanceof Error ? error.message : 'Something went wrong, please try again.',
          },
        })
      }
    })()

    return () => {
      cancelled = true
    }
  }, [navigate, onAuthenticated, params, service])

  return (
    <main className="auth-page">
      <div className="auth-brand">
        <span className="auth-brand-mark" />
        Flow
      </div>
      <section className="auth-panel">
        <LoaderCircle className="auth-spinner" />
        <h1>{message}</h1>
        <p>Completing your sign-in link…</p>
      </section>
    </main>
  )
}

export default AuthTokenPage
