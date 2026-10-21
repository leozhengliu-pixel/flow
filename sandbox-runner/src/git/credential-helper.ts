import { createHash } from 'node:crypto'
import type { CredentialRequest, CredentialResponse } from '../protocol.js'

// git credential helper (`credential.helper=/opt/flow/bin/flow-git-credential`).
// git runs `<helper> get|store|erase` with `key=value` lines on stdin
// (gitcredentials(7)). For `get` we ask Flow for a short-lived token
// (POST /credentials {purpose:"git"}); `erase` reports a rejected token to
// Flow (fingerprint only); `store` is a no-op because nothing is persisted
// in the sandbox. git asks again for every operation,
// so an expired GitHub installation token is refreshed transparently. For
// Flow's git relay URL (origin's push URL when the session pushes as its
// creator) Flow answers with an opaque placeholder; the relay adds the
// member's token server side, so it never reaches the sandbox.

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

/** Hex SHA-256 of a credential value: how a rejected credential is named to Flow (never the value). */
export function credentialFingerprint(password: string): string {
  return createHash('sha256').update(password, 'utf8').digest('hex')
}

/**
 * Runs one helper invocation and returns what to print on stdout. `erase` is
 * git reporting that the host rejected the credential we gave it (HTTP 401):
 * Flow is told which one (by fingerprint) so it drops it when the host
 * confirms it is dead, and the caller's single retry gets a fresh one.
 * `store` is a no-op: nothing is persisted in the sandbox.
 */
export async function runCredentialHelper(action: string, stdinText: string, deps: CredentialHelperDeps): Promise<string> {
  if (action === 'erase') {
    const input = parseCredentialInput(stdinText)
    const request = credentialRequest(input)
    if (!request || !input.password) return ''
    await deps.credentials({ ...request, rejected: true, fingerprint: credentialFingerprint(input.password) })
    return ''
  }
  if (action !== 'get') return ''
  const request = credentialRequest(parseCredentialInput(stdinText))
  if (!request) return ''
  const response = await deps.credentials(request)
  if (!response.password) return ''
  return formatCredentialOutput(response)
}
