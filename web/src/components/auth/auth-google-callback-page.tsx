import { useEffect } from 'react'
import { LoaderCircle } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import {
  clearGoogleLoginState,
  readGoogleLoginState,
} from '@/lib/google-login-state'
import { fetchAuthSession } from '@/lib/api'
import type { AuthSession } from '@/types/flow'

import './auth-page.css'

type Props = {
  onAuthenticated?: (session: AuthSession, returnTo?: string) => Promise<void>
}

/**
 * LS-0081 client completion mirror.
 * Server finishOIDC remains the source of truth (PKCE HttpOnly cookie).
 * This page verifies the optional client_key mirror, then routes home / mobile-auth / error.
 */
export function AuthGoogleCallbackPage({ onAuthenticated }: Props) {
  const location = useLocation()
  const navigate = useNavigate()

  useEffect(() => {
    const params = new URLSearchParams(location.search)
    const error = params.get('error')
    if (error) {
      clearGoogleLoginState()
      navigate(`/auth/error?error=${encodeURIComponent(error)}`, { replace: true })
      return
    }

    const clientKey = params.get('client_key') ?? ''
    const mirrored = readGoogleLoginState()
    clearGoogleLoginState()

    if (clientKey) {
      if (!mirrored?.key) {
        navigate('/auth/error', { replace: true, state: { error: 'No OAuth state' } })
        return
      }
      if (mirrored.key !== clientKey) {
        navigate('/auth/error', { replace: true, state: { error: 'Invalid OAuth state' } })
        return
      }
    }

    let cancelled = false
    void (async () => {
      try {
        const session = await fetchAuthSession()
        if (cancelled) return
        const mobile =
          mirrored?.isMobileAppLogin ||
          params.get('isMobileAppLogin') === '1' ||
          Boolean(mirrored?.mobileRedirectUri)
        if (mobile) {
          const redirectUri =
            mirrored?.mobileRedirectUri ||
            params.get('mobileRedirectUri') ||
            params.get('redirectUri') ||
            ''
          const search = redirectUri ? `?redirectUri=${encodeURIComponent(redirectUri)}` : ''
          if (onAuthenticated) await onAuthenticated(session, `/mobile-auth${search}`)
          else navigate(`/mobile-auth${search}`, { replace: true })
          return
        }
        if (onAuthenticated) await onAuthenticated(session, '/')
        else navigate('/', { replace: true })
      } catch (err) {
        if (cancelled) return
        navigate('/auth/error', {
          replace: true,
          state: {
            error: err instanceof Error ? err.message : 'Something went wrong, please try again.',
          },
        })
      }
    })()
    return () => {
      cancelled = true
    }
  }, [location.search, navigate, onAuthenticated])

  return (
    <main className="auth-page">
      <div className="auth-brand">
        <span className="auth-brand-mark" />
        Flow
      </div>
      <section className="auth-panel">
        <LoaderCircle className="auth-spinner" />
        <h1>Signing in…</h1>
        <p>Finishing Google authentication.</p>
      </section>
    </main>
  )
}

export default AuthGoogleCallbackPage
