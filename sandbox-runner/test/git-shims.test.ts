import assert from 'node:assert/strict'
import { readFileSync, writeFileSync, chmodSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import {
  credentialFingerprint,
  credentialRequest,
  formatCredentialOutput,
  parseCredentialInput,
  runCredentialHelper,
} from '../src/git/credential-helper.js'
import { gpgStatus, parseSignArgs, runSign, type SignDeps } from '../src/git/sign.js'
import type { CredentialRequest, SignRequest } from '../src/protocol.js'
import { json, MAIN_TS, NODE_DIR, sh, shAsync, startServer, tempDir, TSX_BIN } from './helpers.js'

// Credential helper and sign shim: unit tests plus end-to-end runs where the
// real git binary drives the shims against a fake runtime API.

test('parseCredentialInput reads key=value lines until the blank line', () => {
  assert.deepEqual(parseCredentialInput('protocol=https\nhost=github.com\npath=acme/app.git\n\nignored=1\n'), {
    protocol: 'https',
    host: 'github.com',
    path: 'acme/app.git',
  })
  assert.deepEqual(parseCredentialInput('protocol=https\r\nhost=gitlab.example.com:8443\r\n'), { protocol: 'https', host: 'gitlab.example.com:8443' })
  assert.deepEqual(parseCredentialInput('wwwauth[]=Basic realm="x"\n'), { wwwauth: 'Basic realm="x"' })
})

test('credentialRequest serves http(s) only', () => {
  const cases: Array<[string, CredentialRequest | null]> = [
    ['protocol=https\nhost=github.com\npath=acme/app.git', { protocol: 'https', host: 'github.com', path: 'acme/app.git', purpose: 'git' }],
    ['protocol=http\nhost=gitlab.local', { protocol: 'http', host: 'gitlab.local', path: undefined, purpose: 'git' }],
    ['protocol=ssh\nhost=github.com', null],
    ['protocol=https', null],
  ]
  for (const [input, expected] of cases) assert.deepEqual(credentialRequest(parseCredentialInput(input)), expected, input)
})

test('formatCredentialOutput includes the expiry and rejects injected newlines', () => {
  assert.equal(
    formatCredentialOutput({ username: 'x-access-token', password: 'ghs_abc', expiresAt: '2026-10-18T01:00:00Z' }),
    'username=x-access-token\npassword=ghs_abc\npassword_expiry_utc=1792285200\n',
  )
  assert.equal(formatCredentialOutput({ username: '', password: 'glpat', expiresAt: '' }), 'username=x-access-token\npassword=glpat\n')
  assert.throws(() => formatCredentialOutput({ username: 'u', password: 'p\nusername=evil', expiresAt: '' }))
})

test('runCredentialHelper only answers get', async () => {
  const calls: CredentialRequest[] = []
  const deps = {
    credentials: async (request: CredentialRequest) => {
      calls.push(request)
      return { username: 'oauth2', password: 'tok', expiresAt: '' }
    },
  }
  assert.equal(await runCredentialHelper('store', 'protocol=https\nhost=github.com\nusername=a\npassword=b\n', deps), '')
  assert.equal(await runCredentialHelper('erase', 'protocol=https\nhost=github.com\n', deps), '')
  assert.equal(await runCredentialHelper('get', 'protocol=https\nhost=gitlab.com\npath=g/p.git\n', deps), 'username=oauth2\npassword=tok\n')
  assert.deepEqual(calls, [{ protocol: 'https', host: 'gitlab.com', path: 'g/p.git', purpose: 'git' }])
})

test('erase reports the rejected credential to Flow by fingerprint, never by value', async () => {
  const calls: CredentialRequest[] = []
  const deps = {
    credentials: async (request: CredentialRequest) => {
      calls.push(request)
      return { username: '', password: '', expiresAt: '' }
    },
  }
  const out = await runCredentialHelper('erase', 'protocol=https\nhost=gitlab.example.com\npath=acme/app.git\nusername=flow-cs\npassword=glpat-rejected\n', deps)
  assert.equal(out, '')
  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], { protocol: 'https', host: 'gitlab.example.com', path: 'acme/app.git', purpose: 'git', rejected: true, fingerprint: credentialFingerprint('glpat-rejected') })
  assert.match(calls[0].fingerprint ?? '', /^[0-9a-f]{64}$/)
  assert.ok(!JSON.stringify(calls).includes('glpat-rejected'), 'the value never leaves the sandbox')
  // Nothing to report without a password or for a non-http(s) URL.
  await runCredentialHelper('erase', 'protocol=https\nhost=gitlab.example.com\n', deps)
  await runCredentialHelper('erase', 'protocol=ssh\nhost=gitlab.example.com\npassword=x\n', deps)
  assert.equal(calls.length, 1)
})

test('parseSignArgs recognizes git ssh and gpg invocations', () => {
  assert.deepEqual(parseSignArgs(['-Y', 'sign', '-n', 'git', '-f', '/tmp/.git_signing_key_tmpX', '-U', '/tmp/.git_signing_buffer_tmpY']), {
    kind: 'ssh',
    namespace: 'git',
    keyFile: '/tmp/.git_signing_key_tmpX',
    bufferFile: '/tmp/.git_signing_buffer_tmpY',
  })
  assert.deepEqual(parseSignArgs(['-Y', 'sign', '-n', 'git', '-f', 'key']), { kind: 'ssh', namespace: 'git', keyFile: 'key', bufferFile: undefined })
  assert.deepEqual(parseSignArgs(['--status-fd=2', '-bsau', 'ABCDEF0123456789']), { kind: 'openpgp', keyId: 'ABCDEF0123456789', statusFd: 2, armor: true })
  assert.deepEqual(parseSignArgs(['--status-fd', '1', '--detach-sign', '--sign', '--armor', '--local-user=KEY']), { kind: 'openpgp', keyId: 'KEY', statusFd: 1, armor: true })
  assert.deepEqual(parseSignArgs(['-Y', 'verify', '-n', 'git', '-f', 'allowed', '-I', 'a@b', '-s', 'sig']), { kind: 'passthrough', program: 'ssh-keygen' })
  assert.deepEqual(parseSignArgs(['-Y', 'find-principals', '-f', 'allowed', '-s', 'sig']), { kind: 'passthrough', program: 'ssh-keygen' })
  assert.deepEqual(parseSignArgs(['--status-fd=1', '--verify', 'sig', '-']), { kind: 'passthrough', program: 'gpg' })
})

function fakeDeps(overrides: Partial<SignDeps> = {}) {
  const out = { stdout: '', stderr: '', files: new Map<string, string>(), fds: new Map<number, string>(), requests: [] as SignRequest[], passed: [] as string[][] }
  const deps: SignDeps = {
    sign: async (request) => {
      out.requests.push(request)
      return { signature: request.format === 'ssh' ? '-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----' : '-----BEGIN PGP SIGNATURE-----\n\niQ==\n-----END PGP SIGNATURE-----\n' }
    },
    readStdin: async () => Buffer.from('tree abc\nauthor A\n\nmessage\n'),
    readFile: async () => Buffer.from('commit payload'),
    writeFile: async (path, data) => {
      out.files.set(path, data)
    },
    writeStdout: (data) => {
      out.stdout += data
    },
    writeStderr: (data) => {
      out.stderr += data
    },
    writeFd: (fd, data) => {
      out.fds.set(fd, (out.fds.get(fd) ?? '') + data)
    },
    passthrough: async (_program, argv) => {
      out.passed.push(argv)
      return 0
    },
    now: () => new Date('2026-10-18T00:00:00Z'),
    ...overrides,
  }
  return { deps, out }
}

test('runSign ssh writes <buffer>.sig from Flow’s signature', async () => {
  const { deps, out } = fakeDeps()
  assert.equal(await runSign(['-Y', 'sign', '-n', 'git', '-f', 'k', '-U', '/tmp/buf'], deps), 0)
  assert.deepEqual(out.requests, [{ format: 'ssh', payload: Buffer.from('commit payload').toString('base64') }])
  assert.equal(out.files.get('/tmp/buf.sig'), '-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n')
})

test('runSign openpgp prints the signature and a SIG_CREATED status git accepts', async () => {
  const { deps, out } = fakeDeps()
  assert.equal(await runSign(['--status-fd=2', '-bsau', 'abcd'], deps), 0)
  assert.match(out.stdout, /BEGIN PGP SIGNATURE/)
  assert.ok(out.stderr.includes('\n[GNUPG:] SIG_CREATED '), 'git looks for "\\n[GNUPG:] SIG_CREATED "')
  assert.equal(gpgStatus('0xabcd', new Date(0)).split('\n')[2], '[GNUPG:] SIG_CREATED D 1 8 00 0 ABCD')
  const viaFd = fakeDeps()
  await runSign(['--status-fd=5', '-bsau', 'k'], viaFd.deps)
  assert.match(viaFd.out.fds.get(5) ?? '', /SIG_CREATED/)
})

test('runSign passes verification through and fails cleanly when Flow refuses', async () => {
  const { deps, out } = fakeDeps()
  assert.equal(await runSign(['-Y', 'verify', '-n', 'git'], deps), 0)
  assert.deepEqual(out.passed, [['-Y', 'verify', '-n', 'git']])
  const failing = fakeDeps({ sign: async () => Promise.reject(new Error('missingCommitSigningKey')) })
  assert.equal(await runSign(['-Y', 'sign', '-n', 'git', '-f', 'k', '/tmp/b'], failing.deps), 1)
  assert.match(failing.out.stderr, /flow-sign: signing through Flow failed: missingCommitSigningKey/)
  const bogus = fakeDeps({ sign: async () => ({ signature: 'not a signature' }) })
  assert.equal(await runSign(['-Y', 'sign', '-n', 'git', '-f', 'k', '/tmp/b'], bogus.deps), 1)
})

function writeShim(dir: string, name: string, subcommand: string): string {
  const path = join(dir, name)
  writeFileSync(path, `#!/bin/sh\nexec "${TSX_BIN}" "${MAIN_TS}" ${subcommand} "$@"\n`)
  chmodSync(path, 0o755)
  return path
}

test('end to end: git signs a commit through flow-sign and the runtime /sign endpoint', async () => {
  const tmp = tempDir('sign')
  try {
    const key = join(tmp.path, 'id_ed25519')
    sh('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-C', 'dev@example.com', '-f', key])
    const publicKey = readFileSync(`${key}.pub`, 'utf8').trim()
    const tokenFile = join(tmp.path, 'token')
    writeFileSync(tokenFile, 'fcs_test_token\n')
    // The fake runtime signs like Flow does (SSHSIG, namespace git) with the user's key.
    const server = await startServer(0, (req, body, res) => {
      if (req.url !== '/internal/coding-runtime/sign' || req.headers.authorization !== 'Bearer fcs_test_token') return json(res, 401, {})
      const request = JSON.parse(body) as SignRequest
      const payloadFile = join(tmp.path, 'payload')
      writeFileSync(payloadFile, Buffer.from(request.payload, 'base64'))
      sh('ssh-keygen', ['-q', '-Y', 'sign', '-n', 'git', '-f', key, payloadFile])
      json(res, 200, { signature: readFileSync(`${payloadFile}.sig`, 'utf8') })
    })
    try {
      const bin = join(tmp.path, 'bin')
      sh('mkdir', ['-p', bin])
      const signShim = writeShim(bin, 'flow-sign', 'sign')
      const repo = join(tmp.path, 'repo')
      const env = { PATH: `${NODE_DIR}:/usr/bin:/bin`, HOME: tmp.path, FLOW_RUNTIME_URL: server.url, FLOW_TOKEN_FILE: tokenFile, GIT_CONFIG_NOSYSTEM: '1' }
      sh('git', ['init', '-q', '-b', 'flow/x', repo], { env })
      const git = (...args: string[]) => sh('git', args, { cwd: repo, env })
      git('config', 'user.name', 'Dev')
      git('config', 'user.email', 'dev@example.com')
      git('config', 'gpg.format', 'ssh')
      git('config', 'gpg.ssh.program', signShim)
      git('config', 'user.signingkey', publicKey)
      git('config', 'commit.gpgsign', 'true')
      writeFileSync(join(repo, 'a.txt'), 'a\n')
      git('add', 'a.txt')
      await shAsync('git', ['commit', '-q', '-m', 'signed through Flow'], { cwd: repo, env })
      const allowed = join(tmp.path, 'allowed_signers')
      writeFileSync(allowed, `dev@example.com ${publicKey}\n`)
      git('config', 'gpg.ssh.allowedSignersFile', allowed)
      // Verification goes through the shim's passthrough to the real ssh-keygen.
      const verify = sh('git', ['log', '--format=%G?', '-1'], { cwd: repo, env })
      assert.equal(verify.trim(), 'G')
      assert.equal(server.requests.filter((r) => r.url.endsWith('/sign')).length, 1)
    } finally {
      await server.close()
    }
  } finally {
    tmp.cleanup()
  }
})

test('end to end: git asks flow-git-credential, which fetches a short-lived token', async () => {
  const tmp = tempDir('cred')
  try {
    const tokenFile = join(tmp.path, 'token')
    writeFileSync(tokenFile, 'fcs_test_token\n')
    const flowDir = join(tmp.path, '.flow')
    sh('mkdir', ['-p', flowDir])
    const server = await startServer(1, (req, body, res) => {
      if (req.url !== '/internal/coding-runtime/credentials') return json(res, 404, {})
      const request = JSON.parse(body) as CredentialRequest
      json(res, 200, { username: 'x-access-token', password: `ghs_for_${request.host}_${request.path}`, expiresAt: new Date(Date.now() + 3600_000).toISOString() })
    })
    try {
      // No FLOW_* env: the helper falls back to ~/.flow/runtime.json (codex scrubs *TOKEN* vars).
      writeFileSync(join(flowDir, 'runtime.json'), JSON.stringify({ runtimeUrl: server.url, tokenFile, sandboxId: 's' }))
      const helper = writeShim(tmp.path, 'flow-git-credential', 'git-credential')
      const env = { PATH: `${NODE_DIR}:/usr/bin:/bin`, HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0' }
      const out = await shAsync('git', ['-c', 'credential.helper=', '-c', `credential.helper=${helper}`, '-c', 'credential.useHttpPath=true', 'credential', 'fill'], {
        env,
        input: 'protocol=https\nhost=github.com\npath=acme/app.git\n\n',
      })
      assert.match(out, /password=ghs_for_github\.com_acme\/app\.git/)
      assert.match(out, /username=x-access-token/)
      const request = JSON.parse(server.requests[0].body)
      assert.deepEqual(request, { protocol: 'https', host: 'github.com', path: 'acme/app.git', purpose: 'git' })
      assert.equal(server.requests[0].headers.authorization, 'Bearer fcs_test_token')
    } finally {
      await server.close()
    }
  } finally {
    tmp.cleanup()
  }
})
