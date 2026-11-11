const pageRealtimeClientId = crypto.randomUUID()

export class ApiError<T = unknown> extends Error {
  status: number
  code?: string
  current?: T
  /** Values for the message of the error code (a provider name, a limit). */
  params?: Record<string, string>

  constructor(message: string, status: number, code?: string, current?: T, params?: Record<string, string>) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.current = current
    this.params = params
  }
}

export function realtimeClientId() {
  return pageRealtimeClientId
}

export function jsonRequest(method: string, input: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  }
}

export async function request<T>(url: string, init?: ApiRequestInit): Promise<T> {
	// A read the server turned away as busy (503 + Retry-After: its bounded projection gates for the bootstrap and
	// for project, document and initiative reads) is tried again after the wait it names, a few times. Writes are not.
	const method = (init?.method ?? 'GET').toUpperCase()
	if (method === 'GET' || method === 'HEAD') return requestRetryingBusy<T>(url, init ?? {}, 3)
	return parseResponse<T>(await apiFetch(url, init))
}

function abortableDelay(ms: number, signal?: AbortSignal | null) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason ?? new DOMException('Aborted', 'AbortError'))
    const timer = setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve() }, ms)
    const onAbort = () => { clearTimeout(timer); reject(signal?.reason ?? new DOMException('Aborted', 'AbortError')) }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

/**
 * request, but a 503 that carries Retry-After (the server is bounding how many
 * heavy reads it builds at once) is retried a few times after the wait it
 * names, with jitter so a crowd of clients does not return in step. Aborting
 * the signal stops the wait.
 */
export async function requestRetryingBusy<T>(url: string, init: ApiRequestInit = {}, attempts = 5): Promise<T> {
  for (let attempt = 1; ; attempt++) {
    const response = await apiFetch(url, init)
    if (response.status !== 503 || attempt >= attempts) return parseResponse<T>(response)
    const retryAfter = Number(response.headers?.get('Retry-After'))
    if (!Number.isFinite(retryAfter) || retryAfter <= 0) return parseResponse<T>(response)
    await response.body?.cancel().catch(() => undefined)
    await abortableDelay(Math.min(retryAfter, 30) * 1000 * (0.75 + Math.random() * 0.5), init.signal)
  }
}

async function parseResponse<T>(response: Response): Promise<T> {
	if (!response.ok) {
		const payload = await response.json().catch(() => null)
		throw new ApiError(payload?.error_description || payload?.error || `Request failed: ${response.status}`, response.status, payload?.code ?? payload?.error, payload?.current, payload?.params && typeof payload.params === 'object' ? payload.params : undefined)
	}
	if (response.status === 204) return undefined as T
	return response.json()
}

/** `quiet` keeps a failed speculative call from raising the authentication-policy event. */
export type ApiRequestInit = RequestInit & { quiet?: boolean }

export function apiFetch(url: string, { quiet, ...init }: ApiRequestInit = {}) {
  if (import.meta.env.VITE_PAGED_ISSUES === 'true') url = url.replace(/^\/api\/issues(?=\/|\?|$)/, '/api/issue-records')
  const workspaceKey = currentWorkspaceKey()
  const headers = new Headers(init.headers)
  if (workspaceKey && !headers.has('X-Workspace-Key')) headers.set('X-Workspace-Key', workspaceKey)
  headers.set('X-Client-ID', realtimeClientId())
	return fetch(url, { ...init, headers, credentials: 'same-origin' }).then(async response => {
    if(response.status===403 && !quiet) {
      const payload=await response.clone().json().catch(()=>null)
      if(payload?.code==='mfa_required'||payload?.code==='authentication_method_required') window.dispatchEvent(new CustomEvent('flow:authentication-policy',{detail:{code:payload.code,workspaceKey:headers.get('X-Workspace-Key') ?? workspaceKey}}))
    }
    return response
  })
}

export function currentWorkspaceKey() {
  if (typeof window === 'undefined') return ''
  const key = decodeURIComponent(window.location.pathname.split('/').filter(Boolean)[0] ?? '')
  return ['join', 'login', 'signup', 'verify-email', 'forgot-password', 'reset-password', 'invite', 'oauth'].includes(key) ? '' : key
}
