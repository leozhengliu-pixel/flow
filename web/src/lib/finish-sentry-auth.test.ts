import { describe, expect, it } from 'vitest'
import { finishSentryAuth, parseSentryCallbackSearch } from './finish-sentry-auth'

describe('finishSentryAuth', () => {
  it('parses installation callback params', () => {
    expect(parseSentryCallbackSearch('?installationId=1&code=c&orgSlug=acme')).toEqual({
      installationId: '1',
      code: 'c',
      orgSlug: 'acme',
    })
  })

  it('honestly reports MCP-only when App OAuth is unavailable', async () => {
    const missing = await finishSentryAuth('')
    expect(missing.ok).toBe(false)
    if (!missing.ok) expect(missing.mcpOnly).toBe(true)

    const present = await finishSentryAuth('?installationId=1&code=c&orgSlug=acme')
    expect(present.ok).toBe(false)
    if (!present.ok) {
      expect(present.mcpOnly).toBe(true)
      expect(present.error).toMatch(/MCP/)
    }
  })
})
