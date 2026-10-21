import assert from 'node:assert/strict'
import { execFile, spawnSync } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import type { RunnerConfig } from '../src/config.js'
import { loadConfig } from '../src/config.js'
import { baseEnv } from '../src/environment.js'
import { EventBuffer } from '../src/event-buffer.js'
import type { RunnerEvent } from '../src/protocol.js'
import {
  classifyNetworkFailure,
  networkFailureHint,
  PROXY_VARIABLES,
  proxyEnvShell,
  sandboxProxyEnv,
  sandboxSecrets,
  withSandboxIdentity,
} from '../src/proxy-env.js'

// Fixture values only: none of these is a real credential.
const SANDBOX = '01jsbxa'
const TOKEN = 'fcs_fixtureTokenAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
const SPECIAL = "fcs_p@ss:w/rd%41 +?#&='é"

const tempDir = () => mkdtempSync(join(tmpdir(), 'flow-proxy-env-'))

function identity(value: string): { user: string; password: string; host: string } {
  const url = new URL(value)
  return { user: decodeURIComponent(url.username), password: decodeURIComponent(url.password), host: url.host }
}

function runnerConfig(dir: string, sandboxId = SANDBOX): RunnerConfig {
  const tokenFile = join(dir, 'token')
  return loadConfig({ FLOW_RUNTIME_URL: 'http://flow:8090', FLOW_TOKEN_FILE: tokenFile, FLOW_SANDBOX_ID: sandboxId, HOME: join(dir, 'home') }, 'session')
}

test('withSandboxIdentity encodes the identity through the URL API', () => {
  const plain = withSandboxIdentity('http://flow-egress:3128', SANDBOX, TOKEN)
  assert.ok(plain)
  assert.equal(plain.endsWith('@flow-egress:3128'), true, 'keeps the configured form')
  assert.deepEqual(identity(plain), { user: SANDBOX, password: TOKEN, host: 'flow-egress:3128' })

  const special = withSandboxIdentity('http://flow-egress.flow.svc:3128/', 'sb/1:x', SPECIAL)
  assert.ok(special)
  assert.deepEqual(identity(special), { user: 'sb/1:x', password: SPECIAL, host: 'flow-egress.flow.svc:3128' })
  assert.equal(special.split('@').length, 2, 'every @ in the identity is escaped')

  // A configured credential is replaced by the sandbox's own identity.
  const replaced = withSandboxIdentity('http://shared:operator@flow-egress:3128', SANDBOX, TOKEN)
  assert.ok(replaced && !replaced.includes('operator') && !replaced.includes('shared'))
  assert.equal(identity(replaced).user, SANDBOX)
  // Without an identity the URL carries no user info at all.
  assert.equal(withSandboxIdentity('http://shared:operator@flow-egress:3128', SANDBOX, ''), 'http://flow-egress:3128')
  assert.equal(withSandboxIdentity('flow-egress:3128', SANDBOX, TOKEN)?.startsWith('http://'), true)
  assert.equal(withSandboxIdentity('', SANDBOX, TOKEN), undefined)
  assert.equal(withSandboxIdentity('http://', SANDBOX, TOKEN), undefined)
})

test('sandboxProxyEnv unifies the proxy variables and keeps NO_PROXY', () => {
  const env = sandboxProxyEnv({ https_proxy: 'http://proxy:3128', no_proxy: 'localhost,flow' }, SANDBOX, () => TOKEN)
  const values = PROXY_VARIABLES.map((name) => env[name])
  assert.equal(new Set(values).size, 1, 'upper and lower case carry the same URL')
  assert.equal(identity(values[0]!).password, TOKEN)
  assert.equal(env.NO_PROXY, 'localhost,flow')
  assert.equal(env.no_proxy, 'localhost,flow')

  assert.deepEqual(sandboxProxyEnv({}, SANDBOX, () => TOKEN), {}, 'no proxy: nothing is proxied')
  assert.deepEqual(sandboxProxyEnv({ NO_PROXY: 'x' }, SANDBOX, () => TOKEN), { NO_PROXY: 'x', no_proxy: 'x' })

  const unreadable = sandboxProxyEnv({ HTTP_PROXY: 'http://proxy:3128' }, SANDBOX, () => {
    throw new Error('missing token file')
  })
  assert.equal(unreadable.HTTPS_PROXY, 'http://proxy:3128', 'without a token the proxy answers 407, nothing leaks')
})

test('baseEnv adds the identity for every mode and follows token rotation', () => {
  const dir = tempDir()
  const cfg = runnerConfig(dir)
  writeFileSync(cfg.tokenFile, TOKEN + '\n')
  const parent = { PATH: '/usr/bin', HTTPS_PROXY: 'http://proxy:3128', HTTP_PROXY: 'http://proxy:3128', http_proxy: 'http://proxy:3128', https_proxy: 'http://proxy:3128', NO_PROXY: 'localhost,flow', no_proxy: 'localhost,flow' }
  for (const options of [{}, { snapshot: true }]) {
    const env = baseEnv(cfg, undefined, options, parent)
    for (const name of PROXY_VARIABLES) assert.deepEqual(identity(env[name]!), { user: SANDBOX, password: TOKEN, host: 'proxy:3128' })
    assert.equal(env.NO_PROXY, 'localhost,flow')
    assert.equal(env.no_proxy, 'localhost,flow')
  }
  // Flow minted a new token (restart, adoption): the next spawn uses it.
  const rotated = 'fcs_fixtureTokenBBBBBBBBBBBBBBBBBBBBBBBBBBBB'
  writeFileSync(cfg.tokenFile, rotated)
  assert.equal(identity(baseEnv(cfg, undefined, {}, parent).HTTPS_PROXY!).password, rotated)
  // Warm sandboxes bound to a session keep their id; a different sandbox
  // never presents this one's identity.
  const other = runnerConfig(dir, '01jsbxb')
  assert.equal(identity(baseEnv(other, undefined, {}, parent).HTTPS_PROXY!).user, '01jsbxb')
  // No proxy configured: no proxy variables appear.
  const direct = baseEnv(cfg, undefined, {}, { PATH: '/usr/bin' })
  for (const name of PROXY_VARIABLES) assert.equal(direct[name], undefined)
})

test('proxy-env shell exports round-trip through sh', () => {
  const values = sandboxProxyEnv({ HTTPS_PROXY: 'http://proxy:3128', NO_PROXY: 'localhost' }, SANDBOX, () => SPECIAL)
  const script = proxyEnvShell(values)
  const result = spawnSync('/bin/sh', ['-c', `eval "$FLOW_EXPORTS"; printf '%s\\n%s\\n%s' "$HTTPS_PROXY" "$http_proxy" "$no_proxy"`], {
    env: { PATH: '/usr/bin:/bin', FLOW_EXPORTS: script },
    encoding: 'utf8',
  })
  assert.equal(result.status, 0)
  const [https, http_, noProxy] = result.stdout.split('\n')
  assert.equal(https, values.HTTPS_PROXY)
  assert.equal(http_, values.HTTPS_PROXY)
  assert.equal(noProxy, 'localhost')
  assert.equal(identity(https!).password, SPECIAL)
})

/**
 * A fake egress proxy with the deployed contract: Basic proxy credentials
 * must name a known sandbox and its current token (407 otherwise), the
 * destination must be allowed (403), and an unreachable destination is 503.
 */
async function fakeProxy(tokens: Map<string, string>, allowed: Set<string>, upstream: string) {
  const server = http.createServer((req, res) => {
    const header = req.headers['proxy-authorization'] || ''
    const [scheme, encoded] = header.split(' ')
    const decoded = scheme === 'Basic' && encoded ? Buffer.from(encoded, 'base64').toString('utf8') : ''
    const colon = decoded.indexOf(':')
    const user = colon > 0 ? decoded.slice(0, colon) : ''
    const password = colon > 0 ? decoded.slice(colon + 1) : ''
    if (!user || tokens.get(password) !== user) {
      res.writeHead(407, { 'Proxy-Authenticate': 'Basic realm="flow-egress"' }).end()
      return
    }
    const target = new URL(req.url || '/')
    if (!allowed.has(target.hostname)) {
      res.writeHead(403).end()
      return
    }
    if (target.hostname === 'unreachable.example') {
      res.writeHead(503).end()
      return
    }
    http.get(upstream + target.pathname, (up) => {
      res.writeHead(up.statusCode || 502)
      up.pipe(res)
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return { server, url: `http://127.0.0.1:${(server.address() as AddressInfo).port}` }
}

/** A client that uses the proxy variables like curl and npm do: Basic auth from the decoded user info. */
function viaProxy(env: Record<string, string | undefined>, target: string): Promise<number> {
  const proxy = new URL(env.http_proxy || env.HTTP_PROXY || '')
  const headers: Record<string, string> = {}
  if (proxy.username) headers['Proxy-Authorization'] = `Basic ${Buffer.from(`${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`).toString('base64')}`
  return new Promise((resolve, reject) => {
    const request = http.request({ host: proxy.hostname, port: proxy.port, path: target, headers }, (response) => {
      response.resume()
      resolve(response.statusCode || 0)
    })
    request.on('error', reject)
    request.end()
  })
}

test('fake egress proxy: per-sandbox identity, rotation and error classes', async () => {
  const registry = http.createServer((req, res) => res.writeHead(req.url?.includes('broken') ? 502 : 200).end('tarball'))
  await new Promise<void>((resolve) => registry.listen(0, '127.0.0.1', resolve))
  const upstream = `http://127.0.0.1:${(registry.address() as AddressInfo).port}`
  const tokens = new Map([
    [TOKEN, SANDBOX],
    [SPECIAL, '01jsbxc'],
  ])
  const { server, url } = await fakeProxy(tokens, new Set(['registry.npmjs.org', 'unreachable.example']), upstream)
  try {
    const dir = tempDir()
    const cfg = runnerConfig(dir)
    writeFileSync(cfg.tokenFile, TOKEN)
    const parent = { PATH: '/usr/bin', HTTP_PROXY: url, HTTPS_PROXY: url, http_proxy: url, https_proxy: url }
    const pkg = 'http://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz'

    assert.equal(await viaProxy(parent, pkg), 407, 'the bare container environment is the 407 regression')
    assert.equal(await viaProxy(baseEnv(cfg, undefined, {}, parent), pkg), 200)
    assert.equal(await viaProxy(baseEnv(cfg, undefined, { snapshot: true }, parent), pkg), 200)
    assert.equal(await viaProxy(baseEnv(cfg, undefined, {}, parent), 'http://api.anthropic.com/v1'), 403)
    assert.equal(await viaProxy(baseEnv(cfg, undefined, {}, parent), 'http://unreachable.example/x'), 503)
    assert.equal(await viaProxy(baseEnv(cfg, undefined, {}, parent), 'http://registry.npmjs.org/broken'), 502)

    // Special characters survive the encoding.
    const special = runnerConfig(tempDir(), '01jsbxc')
    writeFileSync(special.tokenFile, SPECIAL)
    // readToken requires the fcs_ prefix, which SPECIAL has.
    assert.equal(await viaProxy(baseEnv(special, undefined, {}, parent), pkg), 200)

    // Wrong identity: this sandbox's token under another sandbox id.
    const impostor = runnerConfig(dir, '01jsbxc')
    assert.equal(await viaProxy(baseEnv(impostor, undefined, {}, parent), pkg), 407)

    // Rotation: the new token works as soon as the file changes; once the
    // old one expires it is rejected.
    const rotated = 'fcs_fixtureTokenCCCCCCCCCCCCCCCCCCCCCCCCCCCC'
    const before = baseEnv(cfg, undefined, {}, parent)
    tokens.set(rotated, SANDBOX)
    writeFileSync(cfg.tokenFile, rotated)
    assert.equal(await viaProxy(baseEnv(cfg, undefined, {}, parent), pkg), 200)
    tokens.delete(TOKEN)
    assert.equal(await viaProxy(before, pkg), 407)

    // A real client: curl reads the lower-case http_proxy and decodes the
    // user info itself (skipped when curl is not installed).
    if (spawnSync('curl', ['--version']).status === 0) {
      const curl = (env: NodeJS.ProcessEnv) =>
        new Promise<string>((resolve) => execFile('curl', ['-s', '-o', '/dev/null', '-w', '%{http_code}', pkg], { env }, (_error, stdout) => resolve(String(stdout))))
      assert.equal(await curl({ PATH: process.env.PATH, http_proxy: url }), '407')
      assert.equal(await curl({ ...baseEnv(special, undefined, {}, parent), PATH: process.env.PATH }), '200')
    }
  } finally {
    server.close()
    registry.close()
  }
})

test('classifyNetworkFailure separates proxy auth, egress, toolchain and upstream failures', () => {
  const cases: Array<[string, ReturnType<typeof classifyNetworkFailure>]> = [
    ['curl: (56) CONNECT tunnel failed, response 407', 'proxyAuth'],
    ['curl: (56) Received HTTP code 407 from proxy after CONNECT', 'proxyAuth'],
    ['npm error 407 Proxy Authentication Required - GET https://registry.npmjs.org/left-pad', 'proxyAuth'],
    ["ProxyError('Unable to connect to proxy', OSError('Tunnel connection failed: 407 Proxy Authentication Required'))", 'proxyAuth'],
    ['curl: (56) Received HTTP code 403 from proxy after CONNECT', 'egressDenied'],
    ["ProxyError('Unable to connect to proxy', OSError('Tunnel connection failed: 403 Forbidden'))", 'egressDenied'],
    ['curl: (56) Received HTTP code 503 from proxy after CONNECT', 'egressUnavailable'],
    ['go: downloading x: proxyconnect tcp: dial tcp 10.0.0.9:3128: connect: connection refused', 'egressUnavailable'],
    ['prepare.sh: line 3: cargo: command not found', 'toolchainMissing'],
    ['bash -eo exited with 127', 'toolchainMissing'],
    ['npm error 503 Service Unavailable - GET https://registry.npmjs.org/left-pad', 'upstream'],
    ['npm error code ETIMEDOUT', 'upstream'],
    ['added 407 packages in 3s', undefined],
    ['error: tests failed', undefined],
  ]
  for (const [output, want] of cases) assert.equal(classifyNetworkFailure(output), want, output)
  assert.match(networkFailureHint('Received HTTP code 407 from proxy'), /HTTP 407.*\[proxyAuth\]/)
  assert.equal(networkFailureHint('fine'), '')
})

test('the sandbox token never leaves in events or setup logs', async () => {
  const dir = tempDir()
  const cfg = runnerConfig(dir)
  writeFileSync(cfg.tokenFile, TOKEN)
  const secrets = sandboxSecrets(cfg.tokenFile)
  assert.deepEqual(secrets(), [TOKEN])
  const posted: RunnerEvent[] = []
  const buffer = new EventBuffer({ post: async (events) => void posted.push(...events), secrets, flushIntervalMs: 1 })
  const proxyUrl = baseEnv(cfg, undefined, {}, { HTTPS_PROXY: 'http://proxy:3128' }).HTTPS_PROXY!
  buffer.push({ id: 'e1', type: 'log', createdAt: new Date(0).toISOString(), payload: { line: `HTTPS_PROXY=${proxyUrl}` } } as unknown as RunnerEvent)
  await buffer.flush(5_000)
  const sent = JSON.stringify(posted)
  assert.equal(sent.includes(TOKEN), false)
  assert.equal(sent.includes('***'), true)
})
