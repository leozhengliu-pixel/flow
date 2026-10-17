// Caches content-hashed build output (/assets/) cache-first. Everything else (/api, /uploads, index.html,
// navigations) is never intercepted, so the browser's normal network path and HTTP caching apply.
// Kill switch: replace this file with one that calls self.registration.unregister(); clients drop the worker on
// their next update check. Clients can also set localStorage['flow:disable-sw'] = '1'.
const CACHE = 'flow-assets-v1'
const MAX_ENTRIES = 400
// Vite emits name-[hash].ext; an unhashed file in /assets/ could change in place, so it is never cached.
const HASHED = /-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$/

self.addEventListener('install', () => self.skipWaiting())

self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) if (key !== CACHE) await caches.delete(key)
    await self.clients.claim()
  })())
})

async function trim(cache) {
  const keys = await cache.keys()
  for (const request of keys.slice(0, Math.max(0, keys.length - MAX_ENTRIES))) await cache.delete(request)
}

self.addEventListener('fetch', event => {
  const { request } = event
  if (request.method !== 'GET' || request.headers.has('range')) return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin || !url.pathname.startsWith('/assets/') || !HASHED.test(url.pathname)) return
  event.respondWith((async () => {
    const cache = await caches.open(CACHE)
    const hit = await cache.match(request)
    if (hit) return hit
    const response = await fetch(request)
    // Only a complete 200 is stored: a 404 for a stale chunk must keep failing visibly, never be replayed.
    if (response.status === 200 && response.type === 'basic') {
      event.waitUntil(cache.put(request, response.clone()).then(() => trim(cache)))
    }
    return response
  })())
})
