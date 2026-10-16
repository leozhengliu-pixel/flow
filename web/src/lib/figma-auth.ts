/** Figma OAuth helpers (LS-0078 / LS-0698). REST-only; no GraphQL. */

export const FIGMA_OAUTH_STATE_KEY = 'FIGMA_OAUTH_STATE'
export const FIGMA_OAUTH_SCOPES = ['file_content:read', 'file_metadata:read'] as const

export type FigmaOAuthState = {
  key: string
  urlKey: string
}

export function figmaAuthUrl(input: {
  clientId: string
  redirectUri: string
  state: FigmaOAuthState
  scope?: readonly string[]
}): string {
  const params = new URLSearchParams({
    client_id: input.clientId,
    redirect_uri: input.redirectUri,
    response_type: 'code',
    scope: (input.scope ?? FIGMA_OAUTH_SCOPES).join(' '),
    state: JSON.stringify(input.state),
  })
  return `https://www.figma.com/oauth?${params}`
}

export function readFigmaOAuthState(storage: Storage = sessionStorage): FigmaOAuthState | null {
  try {
    const raw = storage.getItem(FIGMA_OAUTH_STATE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as FigmaOAuthState
    if (!parsed?.key || !parsed?.urlKey) return null
    return parsed
  } catch {
    return null
  }
}

export async function figmaStateFingerprint(key: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(key))
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
}

export async function writeFigmaOAuthState(state: FigmaOAuthState, storage: Storage = sessionStorage): Promise<void> {
  storage.setItem(FIGMA_OAUTH_STATE_KEY, JSON.stringify({ key: await figmaStateFingerprint(state.key), urlKey: state.urlKey }))
}

export function clearFigmaOAuthState(storage: Storage = sessionStorage): void {
  storage.removeItem(FIGMA_OAUTH_STATE_KEY)
}

export function parseFigmaCallbackState(search: string): FigmaOAuthState | null {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const raw = params.get('state')
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as FigmaOAuthState
    if (!parsed?.key || !parsed?.urlKey) return null
    return parsed
  } catch {
    // Server-issued opaque state (flow_oauth_state_*) — not JSON.
    return null
  }
}
