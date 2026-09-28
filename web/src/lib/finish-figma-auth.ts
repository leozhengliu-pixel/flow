import { request } from '@/lib/api-client'
import {
  clearFigmaOAuthState,
  parseFigmaCallbackState,
  readFigmaOAuthState,
} from '@/lib/figma-auth'

export type FinishFigmaAuthResult =
  | { ok: true; status: string; connectionId?: string; workspace?: string }
  | { ok: false; error: string }

/**
 * LS-0698 finishFigmaAuth — exchange Figma callback code via REST.
 * Validates sessionStorage state when the authorize URL used JSON state;
 * otherwise accepts the opaque server state from oauth/start.
 */
export async function finishFigmaAuth(search: string): Promise<FinishFigmaAuthResult> {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const code = params.get('code')?.trim() ?? ''
  const state = params.get('state')?.trim() ?? ''
  const providerError = params.get('error')?.trim()
  if (providerError) {
    return { ok: false, error: providerError }
  }
  if (!code || !state) {
    return { ok: false, error: 'Missing required OAuth parameters' }
  }

  const stored = readFigmaOAuthState()
  const callbackState = parseFigmaCallbackState(search)
  if (stored && callbackState) {
    if (stored.key !== callbackState.key || stored.urlKey !== callbackState.urlKey) {
      clearFigmaOAuthState()
      return { ok: false, error: 'Invalid OAuth state: states do not match' }
    }
  } else if (stored && !callbackState) {
    // Opaque server state — still clear local mirror after attempt.
  } else if (!stored && callbackState) {
    return { ok: false, error: 'Invalid OAuth state: missing state' }
  }

  try {
    const result = await request<{
      provider: string
      connectionId: string
      status: string
    }>(`/api/integrations/figma/oauth/finish`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ code, state }),
    })
    clearFigmaOAuthState()
    return {
      ok: true,
      status: result.status,
      connectionId: result.connectionId,
    }
  } catch (error) {
    clearFigmaOAuthState()
    return {
      ok: false,
      error: error instanceof Error ? error.message : 'Unable to complete connection with Figma',
    }
  }
}
