// Flow's service worker (registered by production builds only, see src/lib/service-worker.ts).
// - Content-hashed build output (/assets/<name>-<hash>.ext) is served cache-first.
// - Page navigations get the app shell (index.html) from the cache at once, while a fresh copy is fetched in the
//   background (stale-while-revalidate). A shell is stored only together with every hashed file it references, in a
//   cache nothing trims, and it is served only while all of them are present: a cached shell never points at an
//   evicted file. A new deploy's shell is stored once its files are, and pages load it from the next navigation on.
// - Never intercepted: /api, /uploads and the other server routes, sign-in/sign-up/OAuth/SSO callback pages, files,
//   other origins, and anything but GET.
// Kill switch: replace this file with one that calls self.registration.unregister(); clients drop the worker on
// their next update check. Clients can also set localStorage['flow:disable-sw'] = '1': the page then sends RETIRE
// before it unregisters this worker, which stops every cache write still in flight and drops the caches once the
// last one settles, so a worker that keeps running for its old page cannot recreate what the page removed.
const ASSET_CACHE = 'flow-assets-v2'
const SHELL_CACHE = 'flow-shell-v2'
const OWNED_CACHE = /^flow-(?:assets|shell)-/
const SHELL_KEY = '/__flow-app-shell'
const SHELL_ASSETS_HEADER = 'x-flow-shell-assets'
const MAX_ENTRIES = 400
// Vite emits name-[hash].ext; an unhashed file in /assets/ could change in place, so it is never cached.
const HASHED = /-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$/
// Answered by the API server or static files (api/cmd/server: serverRoute and withStaticFiles), never the app.
const SERVER_PREFIXES = ['/api/', '/scim/', '/uploads/', '/.well-known/', '/internal/', '/assets/', '/fonts/', '/tour/', '/email/']
const SERVER_PATHS = ['/api', '/mcp', '/mcp/readonly', '/oauth/register', '/oauth/token', '/oauth/revoke', '/oauth/introspect']
// Sign-in, sign-up, invitations, OAuth/SSO callbacks and connections always load from the network.
const NETWORK_PAGES = ['auth', 'oauth', 'connect', 'login', 'signup', 'verify-email', 'forgot-password', 'reset-password', 'invite', 'join', 'mobile-auth']
// Dropped from a stored shell: they describe the network body, not the decoded one the cache keeps.
const BODY_HEADERS = ['content-length', 'content-encoding', 'transfer-encoding']
const RETIRE = 'flow:retire'
let retired = false

self.addEventListener('install', () => self.skipWaiting())

self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) if (OWNED_CACHE.test(key) && key !== ASSET_CACHE && key !== SHELL_CACHE) await caches.delete(key)
    await self.clients.claim()
  })())
})

async function dropOwnedCaches() {
  for (const key of await caches.keys()) if (OWNED_CACHE.test(key)) await caches.delete(key)
}

self.addEventListener('message', event => {
  if (event.data !== RETIRE) return
  retired = true
  event.waitUntil(installing.then(dropOwnedCaches).catch(() => {}))
})

async function trim(cache) {
  const keys = await cache.keys()
  for (const request of keys.slice(0, Math.max(0, keys.length - MAX_ENTRIES))) await cache.delete(request)
}

function shellPage(url) {
  const path = url.pathname
  if (SERVER_PATHS.includes(path) || SERVER_PREFIXES.some(prefix => path.startsWith(prefix))) return false
  const segments = path.split('/').filter(Boolean)
  if (segments.length && NETWORK_PAGES.includes(segments[0].toLowerCase())) return false
  // A file name (sw.js, favicon.svg, ...) is never a page of the app.
  return !segments.length || !/\.[A-Za-z0-9]+$/.test(segments[segments.length - 1])
}

function shellAssets(html) {
  const assets = new Set()
  for (const match of html.matchAll(/(?:src|href)=["'](\/assets\/[^"'?#]+)["']/g)) if (HASHED.test(match[1])) assets.add(match[1])
  return [...assets]
}

function isShellResponse(response) {
  return response.status === 200 && response.type === 'basic' && !response.redirected && /text\/html/i.test(response.headers.get('content-type') ?? '')
}

/** The stored shell, only while every hashed file it references is stored with it. */
async function cachedShell() {
  const cache = await caches.open(SHELL_CACHE)
  const [shell, keys] = await Promise.all([cache.match(SHELL_KEY), cache.keys()])
  if (!shell) return undefined
  const assets = shell.headers.get(SHELL_ASSETS_HEADER)?.split(' ') ?? shellAssets(await shell.clone().text())
  const files = new Set(keys.map(request => request.url))
  if (assets.length && assets.every(path => files.has(new URL(path, self.location.origin).href))) return shell
}

let installing = Promise.resolve()

/** Stores a fresh shell after its hashed files; files only older shells referenced move to the trimmed cache. */
function installShell(response) {
  installing = installing.then(async () => {
    const html = await response.text()
    const assets = shellAssets(html)
    // Not the app's page (an error page, a proxy's answer): keep what is stored.
    if (!assets.length || retired) return
    const shellCache = await caches.open(SHELL_CACHE)
    const assetCache = await caches.open(ASSET_CACHE)
    const cached = new Set((await shellCache.keys()).map(request => request.url))
    const headers = new Headers(response.headers)
    for (const name of BODY_HEADERS) headers.delete(name)
    headers.set(SHELL_ASSETS_HEADER, assets.join(' '))
    for (const path of assets) {
      if (cached.has(new URL(path, self.location.origin).href)) continue
      let file = await assetCache.match(path)
      if (!file) {
        file = await fetch(path, { credentials: 'same-origin' })
        // A file the server does not have: this shell cannot be served offline-first, keep the stored one.
        if (file.status !== 200 || file.type !== 'basic') return
      }
      if (retired) return
      await shellCache.put(path, file)
    }
    // The same page as stored (its files are all there now): nothing to replace.
    const previous = await shellCache.match(SHELL_KEY)
    if (previous && previous.headers.get(SHELL_ASSETS_HEADER) === headers.get(SHELL_ASSETS_HEADER) &&
        await previous.text() === html && previous.headers.get('content-security-policy') === headers.get('content-security-policy')) return
    if (retired) return
    await shellCache.put(SHELL_KEY, new Response(html, { status: 200, statusText: 'OK', headers }))
    const keep = new Set([SHELL_KEY, ...assets])
    for (const request of await shellCache.keys()) {
      if (retired) return
      if (keep.has(new URL(request.url).pathname)) continue
      const file = await shellCache.match(request)
      if (file) await assetCache.put(request, file)
      await shellCache.delete(request)
    }
    await trim(assetCache)
  }).catch(() => {})
  return installing
}

async function revalidateShell(url) {
  try {
    const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store' })
    if (isShellResponse(response)) await installShell(response)
  } catch { /* offline: the stored shell stays */ }
}

async function navigate(event) {
  if (event.request.cache === 'reload' || event.request.cache === 'no-cache') {
    // Start the requested network reload while checking the offline shell.
    // Either response can take the fast path without waiting for the other.
    const network = fetch(event.request)
    const cached = cachedShell()
    try {
      return await Promise.any([
        network.then(response => {
          if (!isShellResponse(response)) throw new Error('No app shell from the server')
          event.waitUntil(installShell(response.clone()))
          return response
        }),
        cached.then(shell => {
          if (!shell) throw new Error('No stored app shell')
          event.waitUntil(network.catch(() => undefined))
          return shell
        }),
      ])
    } catch { return network }
  }
  const shell = await cachedShell().catch(() => undefined)
  if (shell) {
    event.waitUntil(revalidateShell(event.request.url))
    return shell
  }
  const response = await fetch(event.request)
  if (isShellResponse(response)) event.waitUntil(installShell(response.clone()))
  return response
}

async function asset(event) {
  const { request } = event
  const shellHit = await caches.match(request, { cacheName: SHELL_CACHE })
  if (shellHit) return shellHit
  const hit = await caches.match(request, { cacheName: ASSET_CACHE })
  if (hit) return hit
  const response = await fetch(request)
  // Only a complete 200 is stored: a 404 for a stale chunk must keep failing visibly, never be replayed.
  if (response.status === 200 && response.type === 'basic' && !retired) {
    const copy = response.clone()
    event.waitUntil((async () => {
      if (retired) return
      const cache = await caches.open(ASSET_CACHE)
      if (retired) return
      await cache.put(request, copy)
      await trim(cache)
    })())
  }
  return response
}

self.addEventListener('fetch', event => {
  const { request } = event
  if (request.method !== 'GET' || request.headers.has('range')) return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (request.mode === 'navigate') {
    if (shellPage(url)) event.respondWith(navigate(event))
    return
  }
  if (url.pathname.startsWith('/assets/') && HASHED.test(url.pathname)) event.respondWith(asset(event))
})
