import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'

const SHELL = '<!doctype html><html><head><script type="module" src="/assets/index-Bx3k9_Qa.js"></script><link rel="stylesheet" href="/assets/index-Cs7k2_Pq.css"></head><body><div id="root"></div></body></html>'

/** The worker's script with an in-memory Cache API and a scripted network. */
function loadWorker(network: (url: string) => Response | Promise<Response> = () => new Response('net')) {
  const listeners: Record<string, (event: unknown) => void> = {}
  const stores = new Map<string, Map<string, Response>>()
  const path = (request: string | Request) => new URL(typeof request === 'string' ? request : request.url, 'https://flow.test').pathname
  const caches = {
    open: async (name: string) => {
      if (!stores.has(name)) stores.set(name, new Map())
      const store = stores.get(name)!
      return {
        match: async (request: string | Request) => store.get(path(request))?.clone(),
        put: async (request: string | Request, response: Response) => { store.set(path(request), response.clone()) },
        delete: async (request: string | Request) => store.delete(path(request)),
        keys: async () => [...store.keys()].map(key => new Request(`https://flow.test${key}`)),
      }
    },
    keys: async () => [...stores.keys()],
    delete: async (name: string) => stores.delete(name),
  }
  const fetch = vi.fn(async (input: string | Request) => {
    const url = typeof input === 'string' ? input : input.url
    const response = await network(url)
    Object.defineProperty(response, 'type', { value: 'basic' })
    return response
  })
  const self = {
    location: { origin: 'https://flow.test' },
    addEventListener: (type: string, fn: (event: unknown) => void) => { listeners[type] = fn },
    skipWaiting: vi.fn(), clients: { claim: vi.fn() },
  }
  new Function('self', 'caches', 'fetch', readFileSync(resolve(__dirname, '../../public/sw.js'), 'utf8'))(self, caches, fetch)
  const dispatch = async (url: string, init: { method?: string; mode?: string; headers?: HeadersInit } = {}) => {
    const respondWith = vi.fn()
    const waits: Promise<unknown>[] = []
    listeners.fetch({
      request: { method: init.method ?? 'GET', url, mode: init.mode ?? 'cors', headers: new Headers(init.headers) },
      respondWith,
      waitUntil: (promise: Promise<unknown>) => waits.push(promise),
    })
    const response = respondWith.mock.calls[0]?.[0] as Promise<Response> | undefined
    const answered = response ? await response : undefined
    await Promise.all(waits)
    return answered
  }
  return { dispatch, fetch, stores, listeners }
}

const pathOf = (input: unknown) => new URL(typeof input === 'string' ? input : (input as { url: string }).url, 'https://flow.test').pathname
const html = (body = SHELL) => new Response(body, { status: 200, headers: { 'content-type': 'text/html; charset=utf-8', 'content-length': '1', 'content-encoding': 'gzip', 'content-security-policy': "script-src 'self'" } })
const asset = () => new Response('file', { status: 200, headers: { 'content-type': 'text/javascript' } })

function network(pages: { shell?: string } = {}) {
  return (url: string) => new URL(url, 'https://flow.test').pathname.startsWith('/assets/') ? asset() : html(pages.shell)
}

describe('service worker script', () => {
  it('caches only content-hashed build output under /assets/', async () => {
    const worker = loadWorker(() => asset())
    expect(await worker.dispatch('https://flow.test/assets/index-Bx3k9_Qa.js')).toBeDefined()
    expect(await worker.dispatch('https://flow.test/assets/logo.svg')).toBeUndefined()
    expect(await worker.dispatch('https://flow.test/api/issues')).toBeUndefined()
    expect(await worker.dispatch('https://other.test/assets/index-Bx3k9_Qa.js')).toBeUndefined()
    expect(await worker.dispatch('https://flow.test/assets/index-Bx3k9_Qa.js', { headers: { range: 'bytes=0-1' } })).toBeUndefined()
  })

  it('answers app page navigations, and never server routes, sign-in or OAuth pages, files or other methods', async () => {
    const worker = loadWorker(network())
    for (const page of ['/', '/acme', '/acme/team/ACM/all', '/acme/issue/ACM-1/title?x=1', '/acme/settings/account']) {
      expect(await worker.dispatch(`https://flow.test${page}`, { mode: 'navigate' }), page).toBeDefined()
    }
    for (const page of ['/api/health', '/api', '/uploads/a/b.png', '/scim/v2/Users', '/mcp', '/oauth/token', '/oauth/authorize', '/.well-known/oauth-authorization-server',
      '/login', '/signup', '/auth/google/callback', '/auth/oidc/callback', '/auth/token', '/auth/saml/acme', '/connect/oauth/complete', '/invite/abc', '/join',
      '/join/abc', '/mobile-auth', '/reset-password', '/verify-email', '/forgot-password', '/sw.js', '/favicon.svg', '/tour/intro.gif', '/email/logo.png', '/fonts/inter.woff2']) {
      expect(await worker.dispatch(`https://flow.test${page}`, { mode: 'navigate' }), page).toBeUndefined()
    }
    expect(await worker.dispatch('https://flow.test/acme', { mode: 'navigate', method: 'POST' })).toBeUndefined()
    expect(await worker.dispatch('https://other.test/acme', { mode: 'navigate' })).toBeUndefined()
  })

  it('stores a shell only with every hashed file it references, then serves it at once and revalidates', async () => {
    let shell = SHELL
    const worker = loadWorker(url => new URL(url, 'https://flow.test').pathname.startsWith('/assets/') ? asset() : html(shell))
    // First navigation: from the network; the shell and its files are stored.
    expect(await (await worker.dispatch('https://flow.test/acme/inbox', { mode: 'navigate' }))!.text()).toBe(SHELL)
    const stored = worker.stores.get('flow-shell-v2')!
    expect([...stored.keys()].sort()).toEqual(['/__flow-app-shell', '/assets/index-Bx3k9_Qa.js', '/assets/index-Cs7k2_Pq.css'])
    // The stored copy drops headers that describe the network body.
    const kept = stored.get('/__flow-app-shell')!
    expect(kept.headers.get('content-length')).toBeNull()
    expect(kept.headers.get('content-encoding')).toBeNull()
    expect(kept.headers.get('content-security-policy')).toBe("script-src 'self'")

    // A new deploy: this navigation still gets the stored shell; the new one (and its files) replaces it for the next.
    shell = SHELL.replace('index-Bx3k9_Qa.js', 'index-Nw4k9_Zz.js')
    worker.fetch.mockClear()
    expect(await (await worker.dispatch('https://flow.test/acme/inbox', { mode: 'navigate' }))!.text()).toBe(SHELL)
    expect(worker.fetch.mock.calls.map(call => pathOf(call[0]))).toEqual(['/acme/inbox', '/assets/index-Nw4k9_Zz.js'])
    expect(await (await worker.dispatch('https://flow.test/acme', { mode: 'navigate' }))!.text()).toBe(shell)
    expect([...stored.keys()].sort()).toEqual(['/__flow-app-shell', '/assets/index-Cs7k2_Pq.css', '/assets/index-Nw4k9_Zz.js'])
    // The previous build's files move to the trimmed cache (an open page of that build may still ask for them).
    expect(worker.stores.get('flow-assets-v2')?.has('/assets/index-Bx3k9_Qa.js')).toBe(true)
  })

  it('never serves a shell whose files are gone, and never stores a page that is not the app', async () => {
    const worker = loadWorker(network())
    await worker.dispatch('https://flow.test/acme', { mode: 'navigate' })
    const stored = worker.stores.get('flow-shell-v2')!
    stored.delete('/assets/index-Cs7k2_Pq.css')
    worker.fetch.mockClear()
    await worker.dispatch('https://flow.test/acme', { mode: 'navigate' })
    // Not served from the cache: the navigation itself went to the network (and restored the files).
    expect(pathOf(worker.fetch.mock.calls[0][0])).toBe('/acme')
    expect(stored.has('/assets/index-Cs7k2_Pq.css')).toBe(true)

    const other = loadWorker(url => new URL(url, 'https://flow.test').pathname.startsWith('/assets/') ? new Response('missing', { status: 404 }) : html())
    await other.dispatch('https://flow.test/acme', { mode: 'navigate' })
    expect(other.stores.get('flow-shell-v2')?.has('/__flow-app-shell') ?? false).toBe(false)
    const error = loadWorker(() => new Response('<html>Bad gateway</html>', { status: 502, headers: { 'content-type': 'text/html' } }))
    await error.dispatch('https://flow.test/acme', { mode: 'navigate' })
    expect(error.stores.get('flow-shell-v2')?.has('/__flow-app-shell') ?? false).toBe(false)
  })

  it('retires only older Flow caches on activation', async () => {
    const worker = loadWorker()
    for (const name of ['flow-assets-v1', 'flow-assets-v2', 'flow-shell-v1', 'flow-shell-v2', 'unrelated']) worker.stores.set(name, new Map())
    const waits: Promise<unknown>[] = []
    worker.listeners.activate({ waitUntil: (promise: Promise<unknown>) => waits.push(promise) })
    await Promise.all(waits)
    expect([...worker.stores.keys()].sort()).toEqual(['flow-assets-v2', 'flow-shell-v2', 'unrelated'])
  })
})
