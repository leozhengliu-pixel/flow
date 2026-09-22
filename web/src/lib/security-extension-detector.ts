/**
 * LS-0544 SecurityExtensionDetector — low-priority probe for Seraphic / NordVPN TPP.
 * Toast when interfering extensions inject same-origin resource noise; ClientStorage dismiss.
 */
import { toast } from 'sonner'
import { ClientStorage } from '@/lib/client-storage'

export const NORDVPN_TPP_DISMISSED_KEY = 'nordvpn-tpp-dismissed'
/** One week — matches Linear `a.WEEK`. */
export const NORDVPN_TPP_DISMISS_TTL_MS = 7 * 24 * 60 * 60 * 1000
const NORDVPN_PATH_RE = /^\/[A-Za-z0-9_-]{60,}$/

export type ExtensionProbe = {
  name: string
  detect: () => boolean
}

export type SecurityExtensionDetectResult = {
  detected: boolean
  extensionNames: string[]
}

let cached: SecurityExtensionDetectResult | undefined
let observer: PerformanceObserver | undefined

export const DEFAULT_EXTENSION_PROBES: ExtensionProbe[] = [
  {
    name: 'Seraphic',
    detect: () =>
      typeof document !== 'undefined' &&
      document.querySelector('script[src*="jsAgent.js"]') !== null,
  },
  {
    name: 'NordVPN Threat Protection Pro',
    detect: () => detectNordVPNThreatProtection(),
  },
]

export function detectNordVPNThreatProtection(
  entries: { name: string }[] = typeof performance !== 'undefined' && performance.getEntriesByType
    ? (performance.getEntriesByType('resource') as PerformanceResourceTiming[])
    : [],
  origin: string = typeof window !== 'undefined' ? window.location.origin : '',
): boolean {
  if (!origin) return false
  for (const entry of entries) {
    if (!entry.name.startsWith(origin)) continue
    try {
      const pathname = new URL(entry.name).pathname
      if (NORDVPN_PATH_RE.test(pathname)) return true
    } catch {
      /* ignore bad URLs */
    }
  }
  return false
}

export function detectAllSecurityExtensions(
  probes: ExtensionProbe[] = DEFAULT_EXTENSION_PROBES,
): SecurityExtensionDetectResult {
  if (cached) return cached
  if (typeof window === 'undefined') {
    cached = { detected: false, extensionNames: [] }
    return cached
  }
  const extensionNames: string[] = []
  for (const probe of probes) {
    try {
      if (probe.detect()) extensionNames.push(probe.name)
    } catch {
      /* probe failures are non-fatal */
    }
  }
  cached = { detected: extensionNames.length > 0, extensionNames }
  return cached
}

/** Reset memoized result (tests). */
export function resetSecurityExtensionDetectorCache() {
  cached = undefined
  observer?.disconnect()
  observer = undefined
}

export function isNordVpnDismissed(now = Date.now()): boolean {
  const stamped = ClientStorage.get<number>(NORDVPN_TPP_DISMISSED_KEY)
  return typeof stamped === 'number' && now - stamped < NORDVPN_TPP_DISMISS_TTL_MS
}

export function dismissNordVpnWarning(now = Date.now()) {
  ClientStorage.set(NORDVPN_TPP_DISMISSED_KEY, now)
}

export type ObserveNordVpnOptions = {
  toastId?: string
  onDetected?: (extensionName: string) => void
  /** Injectable for tests — defaults to PerformanceObserver when available. */
  createObserver?: (cb: (entries: PerformanceEntry[]) => void) => { disconnect: () => void; observe: (opts: { type: string }) => void } | undefined
}

/** Start PerformanceObserver for NordVPN TPP injection; shows dismissible toast. */
export function observeNordVPNThreatProtection(options: ObserveNordVpnOptions = {}) {
  if (isNordVpnDismissed()) return
  const toastId = options.toastId ?? 'nordvpn-tpp-warning'

  const handleHit = () => {
    options.onDetected?.('NordVPN Threat Protection Pro')
    toast.warning('Browser extension interfering with Flow', {
      id: toastId,
      description:
        'NordVPN Threat Protection Pro is injecting requests that may cause errors. Disable it for this site to resolve this.',
      duration: Infinity,
      onDismiss: () => dismissNordVpnWarning(),
    })
  }

  if (options.createObserver) {
    const obs = options.createObserver(entries => {
      for (const entry of entries) {
        if (entry.entryType !== 'resource') continue
        if (detectNordVPNThreatProtection([entry as PerformanceResourceTiming])) {
          obs?.disconnect()
          handleHit()
          return
        }
      }
    })
    obs?.observe({ type: 'resource' })
    return
  }

  if (typeof PerformanceObserver === 'undefined' || typeof window === 'undefined') return
  observer?.disconnect()
  observer = new PerformanceObserver(list => {
    for (const entry of list.getEntries()) {
      if (entry.entryType !== 'resource') continue
      if (detectNordVPNThreatProtection([entry as PerformanceResourceTiming])) {
        observer?.disconnect()
        observer = undefined
        handleHit()
        return
      }
    }
  })
  try {
    observer.observe({ type: 'resource' })
  } catch {
    observer = undefined
  }
}

/** App-root entry: log probes + schedule NordVPN observer (low priority). */
export function startSecurityExtensionDetector() {
  const result = detectAllSecurityExtensions()
  if (result.detected && typeof console !== 'undefined') {
    console.info('Harmful browser extension detected', {
      extensionNames: result.extensionNames,
    })
  }
  const schedule =
    typeof queueMicrotask === 'function'
      ? queueMicrotask
      : (fn: () => void) => setTimeout(fn, 0)
  schedule(() => observeNordVPNThreatProtection())
  return result
}
