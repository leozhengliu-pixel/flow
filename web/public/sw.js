// Flow's service worker (registered by production builds only, see src/lib/service-worker.ts).
// - Content-hashed build output (/assets/<name>-<hash>.ext) is served cache-first.
// - Page navigations get the app shell (index.html) from the cache at once, while a fresh copy is fetched in the
//   background (stale-while-revalidate). A shell is stored only together with every hashed file it references, in a
//   cache nothing trims, and it is served only while all of them are present: a cached shell never points at an
//   evicted file. A new deploy's shell is stored once its files are, and pages load it from the next navigation on.
// - Never intercepted: /api, /uploads and the other server routes, sign-in/sign-up/OAuth/SSO callback pages, files,
//   other origins, and anything but GET.
// Kill switch: replace this file with one that calls self.registration.unregister(); clients drop the worker on
// their next update check. Clients can also set localStorage['flow:disable-sw'] = '1'.
const ASSET_CACHE = 'flow-assets-v2'
const SHELL_CACHE = 'flow-shell-v2'
const OWNED_CACHE = /^flow-(?:assets|shell)-/
const SHELL_KEY = '/__flow-app-shell'
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

self.addEventListener('install', () => self.skipWaiting())

self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) if (OWNED_CACHE.test(key) && key !== ASSET_CACHE && key !== SHELL_CACHE) await caches.delete(key)
    await self.clients.claim()
  })())
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
  const shell = await cache.match(SHELL_KEY)
  if (!shell) return undefined
  const assets = shellAssets(await shell.clone().text())
  if (!assets.length) return undefined
  for (const path of assets) if (!await cache.match(path)) return undefined
  return shell
}

let installing = Promise.resolve()

/** Stores a fresh shell after its hashed files; files only older shells referenced move to the trimmed cache. */
function installShell(response) {
  installing = installing.then(async () => {
    const html = await response.text()
    const assets = shellAssets(html)
    // Not the app's page (an error page, a proxy's answer): keep what is stored.
    if (!assets.length) return
    const shellCache = await caches.open(SHELL_CACHE)
    const assetCache = await caches.open(ASSET_CACHE)
    const headers = new Headers(response.headers)
    for (const name of BODY_HEADERS) headers.delete(name)
    for (const path of assets) {
      if (await shellCache.match(path)) continue
      let file = await assetCache.match(path)
      if (!file) {
        file = await fetch(path, { credentials: 'same-origin' })
        // A file the server does not have: this shell cannot be served offline-first, keep the stored one.
        if (file.status !== 200 || file.type !== 'basic') return
      }
      await shellCache.put(path, file)
    }
    // The same page as stored (its files are all there now): nothing to replace.
    const previous = await shellCache.match(SHELL_KEY)
    if (previous && await previous.text() === html && previous.headers.get('content-security-policy') === headers.get('content-security-policy')) return
    await shellCache.put(SHELL_KEY, new Response(html, { status: 200, statusText: 'OK', headers }))
    const keep = new Set([SHELL_KEY, ...assets])
    for (const request of await shellCache.keys()) {
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
  const shellHit = await (await caches.open(SHELL_CACHE)).match(request)
  if (shellHit) return shellHit
  const cache = await caches.open(ASSET_CACHE)
  const hit = await cache.match(request)
  if (hit) return hit
  const response = await fetch(request)
  // Only a complete 200 is stored: a 404 for a stale chunk must keep failing visibly, never be replayed.
  if (response.status === 200 && response.type === 'basic') {
    event.waitUntil(cache.put(request, response.clone()).then(() => trim(cache)))
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
