import type { CredentialRequest, CredentialResponse } from '../protocol.js'

// git credential helper (`credential.helper=/opt/flow/bin/flow-git-credential`).
// git runs `<helper> get|store|erase` with `key=value` lines on stdin
// (gitcredentials(7)). For `get` we ask Flow for a short-lived token
// (POST /credentials {purpose:"git"}); `store` and `erase` are no-ops because
// nothing is persisted in the sandbox. git asks again for every operation,
// so an expired GitHub installation token is refreshed transparently.

export type CredentialAction = 'get' | 'store' | 'erase'

export interface CredentialInput {
  protocol?: string
  host?: string
  path?: string
  username?: string
  [key: string]: string | undefined
}

export function parseCredentialInput(text: string): CredentialInput {
  const out: CredentialInput = {}
  for (const line of text.split('\n')) {
    const trimmed = line.replace(/\r$/, '')
    if (!trimmed) break
    const eq = trimmed.indexOf('=')
    if (eq <= 0) continue
    const key = trimmed.slice(0, eq)
    // Multi-valued keys (key[]) are not used by our helper; keep the last value.
    out[key.replace(/\[\]$/, '')] = trimmed.slice(eq + 1)
  }
  return out
}

/** Builds the credential request; null when git asks for something we do not serve. */
export function credentialRequest(input: CredentialInput): CredentialRequest | null {
  const protocol = (input.protocol || '').toLowerCase()
  if (protocol !== 'https' && protocol !== 'http') return null
  if (!input.host) return null
  return { protocol, host: input.host, path: input.path || undefined, purpose: 'git' }
}

function clean(value: string): string {
  // Values may not contain newlines or NUL (they would inject extra keys).
  if (/[\n\r\0]/.test(value)) throw new Error('credential contains a newline')
  return value
}

export function formatCredentialOutput(response: CredentialResponse): string {
  const lines = [`username=${clean(response.username || 'x-access-token')}`, `password=${clean(response.password)}`]
  const expiry = Date.parse(response.expiresAt)
  if (Number.isFinite(expiry) && expiry > 0) lines.push(`password_expiry_utc=${Math.floor(expiry / 1000)}`)
  return lines.join('\n') + '\n'
}

export interface CredentialHelperDeps {
  credentials: (request: CredentialRequest) => Promise<CredentialResponse>
}

/** Runs one helper invocation and returns what to print on stdout. */
export async function runCredentialHelper(action: string, stdinText: string, deps: CredentialHelperDeps): Promise<string> {
  if (action !== 'get') return ''
  const request = credentialRequest(parseCredentialInput(stdinText))
  if (!request) return ''
  const response = await deps.credentials(request)
  if (!response.password) return ''
  return formatCredentialOutput(response)
}
