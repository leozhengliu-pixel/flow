import { afterEach, describe, expect, it, vi } from 'vitest'
import { updateDocumentPresence, updatePresence } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('P06 negotiated heartbeat', () => {
  for (const document of [false, true]) {
    it(`${document ? 'document' : 'issue'} preserves a 204 as no roster`, async () => {
      const json = vi.fn()
      const fetch = vi.fn().mockResolvedValue({ ok: true, status: 204, json })
      vi.stubGlobal('fetch', fetch)
      const result = document ? await updateDocumentPresence('tab', 'doc', '/acme/document/doc') : await updatePresence('tab', 'issue', '/acme/issue/ISS-1')
      expect(result).toBeUndefined()
      expect(json).not.toHaveBeenCalled()
      expect(new Headers(fetch.mock.calls[0][1].headers).get('X-Flow-Presence')).toBe('minimal')
    })
    it(`${document ? 'document' : 'issue'} keeps an old server's 200 roster`, async () => {
      const roster = [{ clientId: 'other', user: { id: 'other' } }]
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => roster }))
      expect(document ? await updateDocumentPresence('tab', 'doc', '/acme/document/doc') : await updatePresence('tab', 'issue', '/acme/issue/ISS-1')).toEqual(roster)
    })
  }
})
