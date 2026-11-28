import { onCLS, onINP, onLCP, onTTFB } from 'web-vitals'
import { parseAppRoute } from './app-routes'
import type { BrowserMeasurement } from './browser-telemetry'

const pages = {
  root: '/',
  'workspace-root': '/{workspace}',
  issue: '/{workspace}/issue/{id}',
  'my-issues': '/{workspace}/my-issues',
  'team-issues': '/{workspace}/team/{team}/issues',
  'workspace-issues': '/{workspace}/issues',
  document: '/{workspace}/document/{id}',
  project: '/{workspace}/project/{id}',
  projects: '/{workspace}/projects',
  documents: '/{workspace}/documents',
  initiatives: '/{workspace}/initiatives',
  initiative: '/{workspace}/initiative/{id}',
  pulse: '/{workspace}/pulse',
  agent: '/{workspace}/agent',
  inbox: '/{workspace}/inbox',
  search: '/{workspace}/search',
  settings: '/{workspace}/settings',
  'workspace-saved-view': '/{workspace}/views',
} as const

export function browserRoute(pathname: string) {
  if (pathname === '/login') return '/login'
  const kind = parseAppRoute(pathname).kind
  return Object.hasOwn(pages, kind) ? pages[kind as keyof typeof pages] : 'unmatched'
}

const assets = /^\/assets\/([A-Za-z0-9_][A-Za-z0-9_.-]{0,100}-[A-Za-z0-9_-]{8,32}\.m?js)$/
const apiPattern = /^\/api\/(?:[a-z][a-z0-9-]*|\{[A-Za-z][A-Za-z0-9]*\})(?:\/(?:[a-z][a-z0-9-]*|\{[A-Za-z][A-Za-z0-9]*\}))*$/

export function bundledFrames(stack: string, origin: string) {
  const frames: string[] = []
  for (const entry of stack.slice(0, 4096).split('\n').slice(1, 17)) {
    const match = entry.match(/(?:^|[@ (\t])(https?:\/\/[^\s()]+):(\d{1,7}):\d{1,7}\)?$/)
    if (!match) continue
    try {
      const url = new URL(match[1])
      const name = assets.exec(url.pathname)?.[1]
      const line = Number(match[2])
      if (url.origin === origin && name && line > 0 && line <= 1000000 && !url.search && !url.hash) frames.push(`${name}:${line}`)
    } catch { /* Only parseable same-origin asset frames qualify. */ }
    if (frames.length === 5) break
  }
  return frames
}

export function scrubBrowserError(message: string) {
  return message.slice(0, 512)
    .replace(/https?:\/\/[^\s]+/gi, '<url>')
    .replace(/[\w.+-]+@[\w.-]+/g, '<email>')
    .replace(/\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b/gi, '<id>')
    .replace(/\b(?:bearer\s+|(?:token|secret|password|api[_-]?key)[=:]\s*|sk-|gh[pousr]_|eyJ)[^\s]+/gi, '<secret>')
    .replace(/['"`][^'"`]*['"`]|\b[0-9a-f]{16,}\b|\b(?:usr|ws|iss|doc)_[A-Za-z0-9_-]+\b/gi, '<value>')
}

const maxRecords = 32
const interval = 10_000

export function startBrowserTelemetry(endpoint: '/api/telemetry', onStop: () => void) {
  let route = browserRoute(location.pathname)
  const landingRoute = route
  let path = location.pathname
  let pendingNavigation = 0
  let pendingErrors = 0
  let sending = false
  let dropped = 0
  const batch: BrowserMeasurement[] = []
  const lifetime = new AbortController()
  const { signal } = lifetime
  let longTasks: PerformanceObserver | undefined

  function record(row: BrowserMeasurement) {
    if (signal.aborted) return
    if (batch.length >= maxRecords) dropped = Math.min(dropped + 1, 10000)
    else batch.push(row)
  }

  function flush(hide: boolean) {
    if (signal.aborted || !batch.length || sending && !hide) return
    if (!hide) sending = true
    do {
      // A batch of 16 fits even when every record includes five asset frames.
      const rows = batch.splice(0, 16)
      if (dropped) {
        rows.push({ kind: 'dropped', value: dropped })
        dropped = 0
      }
      // The same-origin intake authenticates with the existing session.
      void fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(rows),
        credentials: 'same-origin',
        referrerPolicy: 'no-referrer',
        redirect: 'error',
        keepalive: true,
        cache: 'no-store',
        signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
      }).then(response => {
        if (response.status === 404 || response.status === 401) stop()
      }).catch(() => undefined).finally(() => { if (!hide) sending = false })
    } while (hide && batch.length)
  }

  const vital = (kind: 'cls' | 'inp' | 'lcp' | 'ttfb', scale = 1000) => (metric: { value: number }) => {
    if (Number.isFinite(metric.value) && metric.value >= 0) record({ kind, route: landingRoute, value: metric.value / scale })
  }
  onLCP(vital('lcp'))
  onINP(vital('inp'))
  onCLS(vital('cls', 1))
  onTTFB(vital('ttfb'))

  const pageLoaded = () => {
    const navigation = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined
    if (navigation?.domContentLoadedEventEnd) {
      record({ kind: 'navigation', route: landingRoute, value: navigation.domContentLoadedEventEnd / 1000 })
    }
  }
  if (document.readyState === 'complete') pageLoaded()
  else window.addEventListener('load', pageLoaded, { once: true, signal })

  if (typeof PerformanceObserver !== 'undefined' && PerformanceObserver.supportedEntryTypes?.includes('longtask')) {
    longTasks = new PerformanceObserver(list => {
      for (const { duration } of list.getEntries()) record({ kind: 'long_task', route, value: duration / 1000 })
    })
    longTasks.observe({ type: 'longtask', buffered: true })
  }

  function error(reason: unknown) {
    if (signal.aborted || pendingErrors >= 4 || !crypto.subtle || batch.length >= maxRecords) return
    const isError = reason instanceof Error
    const message = typeof reason === 'string' ? reason : isError ? reason.message : 'unhandled exception'
    const frames = bundledFrames(isError ? reason.stack ?? '' : '', location.origin)
    const errorRoute = route
    pendingErrors++
    return crypto.subtle.digest('SHA-256', new TextEncoder().encode(scrubBrowserError(message))).then(bytes => {
      const hash = Array.from(new Uint8Array(bytes), byte => byte.toString(16).padStart(2, '0')).join('')
      record({ kind: 'js_error', route: errorRoute, frames, hash })
    }).catch(() => undefined).finally(() => { pendingErrors-- })
  }

  window.addEventListener('error', event => error(event.error ?? event.message), { signal })
  window.addEventListener('unhandledrejection', event => error(event.reason), { signal })
  const timer = window.setInterval(() => {
    if (document.visibilityState === 'hidden') return
    const memory = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory?.usedJSHeapSize
    if (Number.isFinite(memory) && memory! > 0) record({ kind: 'memory', route, value: memory })
    flush(false)
  }, interval)
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flush(true)
  }, { signal })
  window.addEventListener('pagehide', () => flush(true), { signal })

  function stop() {
    if (signal.aborted) return
    lifetime.abort()
    window.clearInterval(timer)
    longTasks?.disconnect()
    batch.length = 0
    dropped = 0
    onStop()
  }

  return {
    stop,
    record,
    error,
    navigate(pathname: string) {
      if (signal.aborted || path === pathname) return
      route = browserRoute(pathname)
      path = pathname
      const started = performance.now()
      const navigation = ++pendingNavigation
      // Two frames let the router commit and paint the next shell.
      requestAnimationFrame(() => requestAnimationFrame(() => {
        if (navigation === pendingNavigation) record({ kind: 'navigation', route, value: (performance.now() - started) / 1000 })
      }))
    },
    api(response: Response | undefined, method: string | undefined) {
      const pattern = response?.headers?.get('Flow-Telemetry-Route')
      const safeMethod = method?.toUpperCase() ?? 'GET'
      const status = response ? `${Math.floor(response.status / 100)}xx` : 'network'
      record({
        kind: 'api', route: pattern && pattern.length <= 180 && apiPattern.test(pattern) ? pattern : 'unmatched',
        method: ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].includes(safeMethod) ? safeMethod : 'OTHER',
        status,
      })
    },
  }
}
