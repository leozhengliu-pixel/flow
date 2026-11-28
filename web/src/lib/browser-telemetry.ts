type RecordKind = 'lcp' | 'inp' | 'cls' | 'ttfb' | 'navigation' | 'long_task' | 'memory' | 'api' | 'reconnect' | 'storage' | 'js_error' | 'dropped'

export type BrowserMeasurement = {
  kind: RecordKind
  route?: string
  method?: string
  status?: string
  value?: number
  hash?: string
  frames?: string[]
}

type Client = {
  stop(): void
  api(response: Response | undefined, method: string | undefined): void
  record(measurement: BrowserMeasurement): void
  navigate(pathname: string): void
  error(error: unknown): void
}

let client: Client | undefined
let configured = false
let permitted = false
let onStart: ((client: Client) => () => void) | undefined
let unsubscribe: (() => void) | undefined

export function configureBrowserTelemetry(response: Response) {
  const endpoint = response.headers?.get('Flow-Telemetry')
  if (response.status === 401 || response.ok && endpoint !== '/api/telemetry') stopBrowserTelemetry()
  if (configured || !response.ok || endpoint !== '/api/telemetry') return
  configured = true
  const sampleRate = Number(response.headers.get('Flow-Telemetry-Sample-Rate'))
  if (!(sampleRate > 0 && sampleRate <= 1 && Math.random() < sampleRate)) return
  permitted = true
  void import('./browser-telemetry-client').then(module => {
    if (!permitted) return
    client = module.startBrowserTelemetry(endpoint, () => {
      client = undefined
      unsubscribe?.()
      unsubscribe = undefined
    })
    unsubscribe = onStart?.(client)
    onStart = undefined
  }).catch(() => undefined)
}

export function onBrowserTelemetryStart(callback: (client: Client) => () => void) {
  if (client) unsubscribe = callback(client)
  else onStart = callback
}

export function stopBrowserTelemetry() {
  permitted = false
  client?.stop()
}

export function activeBrowserTelemetry() {
  return client
}
