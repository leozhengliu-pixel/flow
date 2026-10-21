import { readToken } from './config.js'

// Egress proxy identity (docs/coding-sessions-design.md §19).
//
// The egress proxy authenticates every request as one sandbox:
// http://<sandboxId>:<token>@proxy:3128. Flow puts only the bare proxy URL
// into the container environment (sandbox.ProxyBaseURL), because the token
// is per sandbox, lives in the token file and changes when Flow mints a new
// one. The runner is the trusted layer that adds the identity: every process
// it starts gets the four proxy variables with the sandbox id and the token
// read from the token file at spawn time, and login shells (the debug
// terminal) get the same values from /etc/profile.d via `flow-runner
// proxy-env`. Never log the values returned here.

/** The proxy variables, upper and lower case; always set to the same value. */
export const PROXY_VARIABLES = ['HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy'] as const

/** The configured proxy (the first non-empty proxy variable), '' when none. */
export function configuredProxy(env: NodeJS.ProcessEnv): string {
  for (const name of ['HTTPS_PROXY', 'https_proxy', 'HTTP_PROXY', 'http_proxy']) {
    const value = (env[name] || '').trim()
    if (value) return value
  }
  return ''
}

function parseProxy(raw: string): URL | undefined {
  try {
    const url = new URL(raw.includes('://') ? raw : `http://${raw}`)
    return url.host ? url : undefined
  } catch {
    return undefined
  }
}

function serialize(url: URL, raw: string): string {
  const text = url.toString()
  // Keep the configured form: no trailing slash the operator did not write.
  return url.pathname === '/' && !url.search && !url.hash && !raw.endsWith('/') ? text.slice(0, -1) : text
}

/**
 * The proxy URL with the sandbox's identity, set through the URL API with
 * both parts percent-encoded (clients decode them for Basic auth; Squid
 * re-escapes them for the helper). Any user info already in the URL is
 * replaced: a configured credential would be shared by every sandbox.
 * Without an id or token the URL is returned without user info.
 */
export function withSandboxIdentity(raw: string, sandboxId: string, token: string): string | undefined {
  const url = parseProxy(raw.trim())
  if (!url) return undefined
  url.username = ''
  url.password = ''
  if (sandboxId && token) {
    url.username = encodeURIComponent(sandboxId)
    url.password = encodeURIComponent(token)
  }
  return serialize(url, raw.trim())
}

/**
 * The proxy variables for a child process: all four proxy variables carry
 * the same URL with this sandbox's identity, and NO_PROXY / no_proxy the
 * same list (loopback and Flow, set by Flow). Without a configured proxy
 * nothing is returned, so nothing is proxied. When the token cannot be read
 * the variables carry the URL without identity and the proxy answers 407.
 */
export function sandboxProxyEnv(env: NodeJS.ProcessEnv, sandboxId: string, token: () => string): Record<string, string> {
  const out: Record<string, string> = {}
  const raw = configuredProxy(env)
  if (raw) {
    let secret = ''
    try {
      secret = token()
    } catch {
      secret = ''
    }
    const value = withSandboxIdentity(raw, sandboxId, secret)
    if (value) for (const name of PROXY_VARIABLES) out[name] = value
  }
  const noProxy = (env.NO_PROXY || '').trim() || (env.no_proxy || '').trim()
  if (noProxy) out.NO_PROXY = out.no_proxy = noProxy
  return out
}

/** sandboxProxyEnv for the runner's own sandbox (id from the env, token from the token file). */
export function runnerProxyEnv(env: NodeJS.ProcessEnv, sandboxId: string, tokenFile: string): Record<string, string> {
  return sandboxProxyEnv(env, sandboxId, () => readToken(tokenFile))
}

/** POSIX shell `export` lines for /etc/profile.d (single-quoted values). */
export function proxyEnvShell(values: Record<string, string>): string {
  const quote = (value: string) => `'${value.split("'").join(`'\\''`)}'`
  return Object.entries(values)
    .map(([name, value]) => `export ${name}=${quote(value)}\n`)
    .join('')
}

/**
 * The token values to mask in logs and events (raw and percent-encoded),
 * re-read at most every ttlMs because the token file may be rewritten.
 */
export function sandboxSecrets(tokenFile: string, ttlMs = 5_000, now: () => number = Date.now): () => string[] {
  let cached: string[] = []
  let readAt = -Infinity
  return () => {
    if (now() - readAt < ttlMs) return cached
    readAt = now()
    try {
      const token = readToken(tokenFile)
      cached = [...new Set([token, encodeURIComponent(token)])]
    } catch {
      // Keep the last known values.
    }
    return cached
  }
}

/** Why a networked command failed, so a setup error names the real cause. */
export type NetworkFailure = 'proxyAuth' | 'egressDenied' | 'egressUnavailable' | 'toolchainMissing' | 'upstream'

const FAILURE_PATTERNS: Array<[NetworkFailure, RegExp]> = [
  ['proxyAuth', /(\b407\b[^\n]*proxy|proxy[^\n]*\b407\b|tunnel (connection )?failed(, response|:) 407|proxy authentication required|\bE407\b)/i],
  [
    'egressDenied',
    /(received http code 403 from proxy|tunnel connection failed: 403|connect tunnel failed, response 403|proxyconnect[^\n]*403|ERR_ACCESS_DENIED|403 forbidden[^\n]*proxy|proxy[^\n]*403 forbidden)/i,
  ],
  [
    'egressUnavailable',
    /(received http code 50[234] from proxy|tunnel connection failed: 50[234]|connect tunnel failed, response 50[234]|ERR_CONNECT_FAIL|ERR_DNS_FAIL|could not resolve proxy|unable to connect to proxy|failed to connect to [^\n]*proxy|proxyconnect tcp)/i,
  ],
  ['toolchainMissing', /(command not found|^\S+: not found$|exited with (exit code )?127\b|failed with exit code 127\b|mise[^\n]*(not installed|missing:)|no such file or directory[^\n]*\b(bin|exec)\b)/im],
  ['upstream', /(\b5\d\d (internal server error|bad gateway|service unavailable|gateway time-?out)\b|\bE50[0-4]\b|\bETIMEDOUT\b|\bECONNRESET\b|\bEAI_AGAIN\b|returned error: 5\d\d|status code 5\d\d)/i],
]

const FAILURE_TEXT: Record<NetworkFailure, string> = {
  proxyAuth: 'the egress proxy rejected the sandbox credentials (HTTP 407)',
  egressDenied: 'the egress proxy denied the destination (HTTP 403; check the environment network mode and allowed domains)',
  egressUnavailable: 'the egress proxy could not reach the destination or is unreachable (HTTP 502/503)',
  toolchainMissing: 'a required tool is not installed (command not found)',
  upstream: 'an upstream service failed (HTTP 5xx, timeout or reset from the registry or host)',
}

/** Classifies command output; proxy failures win over the symptoms they cause. */
export function classifyNetworkFailure(output: string): NetworkFailure | undefined {
  for (const [kind, pattern] of FAILURE_PATTERNS) if (pattern.test(output)) return kind
  return undefined
}

/** A short explanation of the failure in output, '' when it is not recognized. */
export function networkFailureHint(output: string): string {
  const kind = classifyNetworkFailure(output)
  return kind ? `${FAILURE_TEXT[kind]} [${kind}]` : ''
}
