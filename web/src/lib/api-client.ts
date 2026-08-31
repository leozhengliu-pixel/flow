const pageRealtimeClientId = crypto.randomUUID()

export class ApiError<T = unknown> extends Error {
  status: number
  code?: string
  current?: T

  constructor(message: string, status: number, code?: string, current?: T) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.current = current
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

export async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const workspaceKey = currentWorkspaceKey()
  const headers = new Headers(init?.headers)
  if (workspaceKey && !headers.has('X-Workspace-Key')) headers.set('X-Workspace-Key', workspaceKey)
  headers.set('X-Client-ID', realtimeClientId())
  const response = await fetch(url, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    throw new ApiError(payload?.error || `Request failed: ${response.status}`, response.status, payload?.code, payload?.current)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

function currentWorkspaceKey() {
  if (typeof window === 'undefined') return ''
  const key = decodeURIComponent(window.location.pathname.split('/').filter(Boolean)[0] ?? '')
  return ['join', 'login', 'signup', 'verify-email', 'forgot-password', 'reset-password', 'invite', 'oauth'].includes(key) ? '' : key
}
