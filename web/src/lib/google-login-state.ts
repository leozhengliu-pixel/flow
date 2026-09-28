/** Client mirror for Google/OIDC login state (LS-0081). Server still finishes OIDC via PKCE cookie. */

export const GOOGLE_LOGIN_STATE_KEY = 'GOOGLE_LOGIN_STATE_KEY'

export type GoogleLoginState = {
  key: string
  inviteLink?: string
  mobileRedirectUri?: string
  isMobileAppLogin?: boolean
}

function storage(): Storage | null {
  try {
    return typeof sessionStorage !== 'undefined' ? sessionStorage : null
  } catch {
    return null
  }
}

export function createGoogleLoginState(extra: Partial<GoogleLoginState> = {}): GoogleLoginState {
  const state: GoogleLoginState = {
    key: crypto.randomUUID(),
    ...extra,
  }
  const raw = JSON.stringify(state)
  const store = storage()
  store?.setItem(GOOGLE_LOGIN_STATE_KEY, raw)
  try {
    localStorage.setItem(GOOGLE_LOGIN_STATE_KEY, raw)
  } catch {
    /* ignore */
  }
  return state
}

export function readGoogleLoginState(): GoogleLoginState | null {
  const store = storage()
  const raw =
    store?.getItem(GOOGLE_LOGIN_STATE_KEY) ||
    (() => {
      try {
        return localStorage.getItem(GOOGLE_LOGIN_STATE_KEY)
      } catch {
        return null
      }
    })()
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as GoogleLoginState
    if (!parsed?.key) return null
    return parsed
  } catch {
    return null
  }
}

export function clearGoogleLoginState() {
  storage()?.removeItem(GOOGLE_LOGIN_STATE_KEY)
  try {
    localStorage.removeItem(GOOGLE_LOGIN_STATE_KEY)
  } catch {
    /* ignore */
  }
}

export function withClientKey(startUrl: string, key: string): string {
  const url = new URL(startUrl, window.location.origin)
  url.searchParams.set('client_key', key)
  return `${url.pathname}${url.search}`
}
