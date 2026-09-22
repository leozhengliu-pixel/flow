import { beforeEach, describe, expect, it, vi } from 'vitest'
import { finishFigmaAuth } from './finish-figma-auth'
import { FIGMA_OAUTH_STATE_KEY } from './figma-auth'
import { request } from '@/lib/api-client'

vi.mock('@/lib/api-client', () => ({ request: vi.fn() }))

beforeEach(() => {
  sessionStorage.clear()
  vi.clearAllMocks()
})

describe('finishFigmaAuth', () => {
  it('rejects missing params', async () => {
    const result = await finishFigmaAuth('')
    expect(result).toEqual({ ok: false, error: 'Missing required OAuth parameters' })
  })

  it('posts code/state to REST finish and clears storage', async () => {
    sessionStorage.setItem(FIGMA_OAUTH_STATE_KEY, JSON.stringify({ key: 'k', urlKey: 'acme' }))
    vi.mocked(request).mockResolvedValue({ provider: 'figma', connectionId: 'c1', status: 'connected' })
    const state = encodeURIComponent(JSON.stringify({ key: 'k', urlKey: 'acme' }))
    const result = await finishFigmaAuth(`?code=auth-code&state=${state}`)
    expect(result).toEqual({ ok: true, status: 'connected', connectionId: 'c1' })
    expect(request).toHaveBeenCalledWith(
      '/api/integrations/figma/oauth/finish',
      expect.objectContaining({ method: 'POST' }),
    )
    expect(sessionStorage.getItem(FIGMA_OAUTH_STATE_KEY)).toBeNull()
  })

  it('rejects mismatched stored state', async () => {
    sessionStorage.setItem(FIGMA_OAUTH_STATE_KEY, JSON.stringify({ key: 'a', urlKey: 'acme' }))
    const state = encodeURIComponent(JSON.stringify({ key: 'b', urlKey: 'acme' }))
    const result = await finishFigmaAuth(`?code=auth-code&state=${state}`)
    expect(result.ok).toBe(false)
    expect(request).not.toHaveBeenCalled()
  })
})
