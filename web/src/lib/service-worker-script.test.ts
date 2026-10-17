import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'

function loadWorker() {
  const listeners: Record<string, (event: unknown) => void> = {}
  const self = { location: { origin: 'https://flow.test' }, addEventListener: (type: string, fn: (event: unknown) => void) => { listeners[type] = fn } }
  new Function('self', 'caches', 'fetch', readFileSync(resolve(__dirname, '../../public/sw.js'), 'utf8'))(self, { open: async () => ({ match: async () => new Response('x') }) }, vi.fn())
  return listeners.fetch
}

function intercepts(url: string) {
  const respondWith = vi.fn()
  loadWorker()({ request: { method: 'GET', url, headers: new Headers() }, respondWith, waitUntil: vi.fn() })
  return respondWith.mock.calls.length > 0
}

describe('service worker script', () => {
  it('caches only content-hashed build output under /assets/', () => {
    expect(intercepts('https://flow.test/assets/index-Bx3k9_Qa.js')).toBe(true)
    expect(intercepts('https://flow.test/assets/logo.svg')).toBe(false)
    expect(intercepts('https://flow.test/api/issues')).toBe(false)
    expect(intercepts('https://other.test/assets/index-Bx3k9_Qa.js')).toBe(false)
  })
})
