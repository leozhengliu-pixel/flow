import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { deletePasskey } from './api'

function json(payload: unknown, status = 200) {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('changing a registered passkey', () => {
  const fetchMock = vi.fn()
  const get = vi.fn()

  beforeEach(() => {
    fetchMock.mockReset()
    get.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    Object.defineProperty(navigator, 'credentials', { configurable: true, value: { get } })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('verifies the passkey when the server asks for a recent verification, then retries once', async () => {
    const bytes = new Uint8Array([1, 2, 3]).buffer
    get.mockResolvedValue({ id: 'cred', type: 'public-key', rawId: bytes, response: { clientDataJSON: bytes, authenticatorData: bytes, signature: bytes, userHandle: null } })
    fetchMock
      .mockResolvedValueOnce(json({ error: 'Verify your passkey again', code: 'mfa_reauth_required' }, 403))
      .mockResolvedValueOnce(json({ challengeId: 'passkey_mfa_1', options: { publicKey: { challenge: 'AQID', allowCredentials: [{ id: 'AQID', type: 'public-key' }] } } }))
      .mockResolvedValueOnce(json({ verified: true }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    await deletePasskey('passkey-1')
    expect(fetchMock.mock.calls.map(([url, init]) => `${(init as RequestInit).method} ${url}`)).toEqual([
      'DELETE /api/account/passkeys/passkey-1',
      'POST /api/account/mfa/start',
      'POST /api/account/mfa/finish',
      'DELETE /api/account/passkeys/passkey-1',
    ])
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('does not retry other refusals', async () => {
    fetchMock.mockResolvedValue(json({ error: 'Nope', code: 'something_else' }, 403))
    await expect(deletePasskey('passkey-1')).rejects.toMatchObject({ code: 'something_else' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
