import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { createTarZst, extractTarZst, fromArchiveLayout, prefixTransforms, tarZstBuffer, toArchiveLayout } from '../src/archive.js'
import { silentLogger } from '../src/log.js'
import { RESTORE_FAILED_EXIT, restoreSnapshot, restoreTarget, snapshotIdFromArchiveUrl } from '../src/restore.js'
import { RuntimeClient } from '../src/runtime-client.js'
import { gnuTarSkip, json, MAIN_TS, sh, startServer, tempDir, TSX_BIN } from './helpers.js'

// Restore mode: FLOW_ARCHIVE_URL (track A) or the snapshot archive route,
// and exit code 86 when the archive cannot be restored.

test('restoreSnapshot downloads from FLOW_ARCHIVE_URL with the sandbox token and unpacks it', { skip: gnuTarSkip }, async () => {
  const tmp = tempDir('restore')
  try {
    const src = join(tmp.path, 'src')
    mkdirSync(join(src, 'workspace', 'repo'), { recursive: true })
    writeFileSync(join(src, 'workspace', 'repo', 'README.md'), '# restored\n')
    const archive = await tarZstBuffer(src, ['workspace'], 1 << 20)
    const server = await startServer(4, (req, _body, res) => {
      if (req.url !== '/archives/snap1.tar.zst' || req.headers.authorization !== 'Bearer fcs_tok') {
        res.writeHead(404)
        res.end()
        return
      }
      res.writeHead(200, { 'content-type': 'application/zstd' })
      res.end(Buffer.from(archive!))
    })
    try {
      const client = new RuntimeClient({ baseUrl: server.url, token: () => 'fcs_tok', sleep: async () => {} })
      const root = join(tmp.path, 'root')
      mkdirSync(root)
      await restoreSnapshot(client, 'snap1', silentLogger, root, `${server.url}/archives/snap1.tar.zst`)
      assert.equal(readFileSync(join(root, 'workspace', 'repo', 'README.md'), 'utf8'), '# restored\n')
      await assert.rejects(restoreSnapshot(client, 'snap1', silentLogger, root, `${server.url}/missing`), /restoring snapshot snap1 failed/)
    } finally {
      await server.close()
    }
  } finally {
    tmp.cleanup()
  }
})

test('flow-runner restore exits 86 when the archive is corrupt', async () => {
  const tmp = tempDir('restore86')
  try {
    const tokenFile = join(tmp.path, 'token')
    writeFileSync(tokenFile, 'fcs_tok\n', { mode: 0o440 })
    const server = await startServer(5, (_req, _body, res) => {
      res.writeHead(200, { 'content-type': 'application/zstd' })
      res.end('this is not a zstd archive')
    })
    try {
      const code = await new Promise<number | null>((resolve) => {
        const child = spawn(TSX_BIN, [MAIN_TS, 'restore'], {
          env: { PATH: process.env.PATH, HOME: tmp.path, FLOW_RUNTIME_URL: server.url, FLOW_TOKEN_FILE: tokenFile, FLOW_ARCHIVE_URL: `${server.url}/a.tar.zst` },
          stdio: 'ignore',
        })
        child.once('close', resolve)
      })
      assert.equal(code, RESTORE_FAILED_EXIT)
    } finally {
      await server.close()
    }
  } finally {
    tmp.cleanup()
  }
})

test('restoreTarget takes the snapshot id from FLOW_ARCHIVE_URL, never a placeholder', () => {
  const url = 'http://flow:8090/internal/coding-runtime/snapshots/01m5bn6js36ey9kk83hhwhvc5t/archive'
  assert.equal(snapshotIdFromArchiveUrl(url), '01m5bn6js36ey9kk83hhwhvc5t')
  assert.equal(snapshotIdFromArchiveUrl('http://flow/archives/a.tar.zst'), undefined)
  assert.deepEqual(restoreTarget({ FLOW_ARCHIVE_URL: url }), { snapshotId: '01m5bn6js36ey9kk83hhwhvc5t', archiveUrl: url })
  assert.deepEqual(restoreTarget({ FLOW_SNAPSHOT_ID: '01m5bn6js36ey9kk83hhwhvc5t', FLOW_ARCHIVE_URL: url }).snapshotId, '01m5bn6js36ey9kk83hhwhvc5t')
  assert.throws(() => restoreTarget({ FLOW_SNAPSHOT_ID: 'other', FLOW_ARCHIVE_URL: url }), /does not match/)
  assert.throws(() => restoreTarget({ FLOW_ARCHIVE_URL: 'http://flow/archives/a.tar.zst' }), /does not name a snapshot/)
  assert.deepEqual(restoreTarget({}), { snapshotId: undefined, archiveUrl: undefined })
})

// The restore step against a server that behaves like Flow's archive route:
// only the granted snapshot id is served, any other id is 403.
test('flow-runner restore restores the granted snapshot (HEAD matches) and reports a refused one', { skip: gnuTarSkip }, async () => {
  const tmp = tempDir('restore-grant')
  try {
    const repoSrc = join(tmp.path, 'src', 'workspace', 'repo')
    mkdirSync(repoSrc, { recursive: true })
    writeFileSync(join(repoSrc, 'README.md'), '# snapshot\n')
    const git = (cwd: string, ...args: string[]) => sh('git', ['-C', cwd, '-c', 'user.name=Flow', '-c', 'user.email=flow@example.com', '-c', 'commit.gpgsign=false', ...args]).trim()
    git(repoSrc, 'init', '-q')
    git(repoSrc, 'add', 'README.md')
    git(repoSrc, 'commit', '-q', '-m', 'snapshot')
    const head = git(repoSrc, 'rev-parse', 'HEAD')
    const archive = await tarZstBuffer(join(tmp.path, 'src'), ['workspace'], 8 << 20)
    const granted = '01m5bn6js36ey9kk83hhwhvc5t'
    const server = await startServer(6, (req, _body, res) => {
      const match = /^\/internal\/coding-runtime\/snapshots\/([^/]+)\/archive$/.exec(req.url ?? '')
      if (!match || req.headers.authorization !== 'Bearer fcs_tok') {
        res.writeHead(401)
        res.end()
      } else if (match[1] !== granted) {
        json(res, 403, { error: 'this sandbox does not restore that snapshot' })
      } else {
        res.writeHead(200, { 'content-type': 'application/zstd' })
        res.end(Buffer.from(archive!))
      }
    })
    try {
      const tokenFile = join(tmp.path, 'token')
      writeFileSync(tokenFile, 'fcs_tok\n', { mode: 0o440 })
      const restore = (snapshotId: string, name: string) =>
        new Promise<{ code: number | null; log: string; workspace: string }>((resolve) => {
          // Local-provider paths: the archive's workspace/… lands in FLOW_WORKSPACE_DIR.
          const workspace = join(tmp.path, name, 'ws')
          const home = join(tmp.path, name, 'home')
          mkdirSync(workspace, { recursive: true })
          mkdirSync(home, { recursive: true })
          const child = spawn(TSX_BIN, [MAIN_TS, 'restore'], {
            env: {
              PATH: process.env.PATH,
              HOME: home,
              FLOW_WORKSPACE_DIR: workspace,
              FLOW_RUNTIME_URL: server.url,
              FLOW_TOKEN_FILE: tokenFile,
              FLOW_ARCHIVE_URL: `${server.url}/internal/coding-runtime/snapshots/${snapshotId}/archive`,
            },
            stdio: ['ignore', 'pipe', 'pipe'],
          })
          let log = ''
          child.stdout.on('data', (chunk) => (log += chunk))
          child.stderr.on('data', (chunk) => (log += chunk))
          child.once('close', (code) => resolve({ code, log, workspace }))
        })
      const ok = await restore(granted, 'granted')
      assert.equal(ok.code, 0, ok.log)
      assert.match(ok.log, new RegExp(`"msg":"restoring snapshot","snapshotId":"${granted}"`))
      assert.doesNotMatch(ok.log, /"snapshotId":"archive"/)
      assert.equal(git(join(ok.workspace, 'repo'), 'rev-parse', 'HEAD'), head)
      assert.equal(readFileSync(join(ok.workspace, 'repo', 'README.md'), 'utf8'), '# snapshot\n')

      const refused = await restore('01m5bn6js36ey9kk83hhwhvc5u', 'refused')
      assert.equal(refused.code, RESTORE_FAILED_EXIT)
      assert.match(refused.log, /"msg":"restore failed","error":"restoring snapshot 01m5bn6js36ey9kk83hhwhvc5u failed: [^"]*403/)
    } finally {
      await server.close()
    }
  } finally {
    tmp.cleanup()
  }
})

test('flow-runner debug writes the ready marker once it starts', async () => {
  const tmp = tempDir('ready')
  try {
    const tokenFile = join(tmp.path, 'token')
    writeFileSync(tokenFile, 'fcs_tok\n', { mode: 0o440 })
    const marker = join(tmp.path, 'ready')
    const child = spawn(TSX_BIN, [MAIN_TS, 'debug'], {
      env: { PATH: process.env.PATH, HOME: tmp.path, FLOW_RUNTIME_URL: 'http://127.0.0.1:9', FLOW_TOKEN_FILE: tokenFile, FLOW_RUNNER_READY_FILE: marker },
      stdio: 'ignore',
    })
    const closed = new Promise((resolve) => child.once('close', resolve))
    try {
      const deadline = Date.now() + 20_000
      while (!existsSync(marker) && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 50))
      assert.ok(existsSync(marker), 'the debug runner writes the ready marker')
    } finally {
      child.kill('SIGTERM')
      await closed
    }
  } finally {
    tmp.cleanup()
  }
})

test('snapshot archives keep the container layout when the runner paths differ (local provider)', { skip: gnuTarSkip }, async () => {
  const tmp = tempDir('layout')
  try {
    // Snapshot sandbox: workspace and home live at host paths, relative to cwd.
    const src = join(tmp.path, 'src')
    mkdirSync(join(src, 'sb1', 'vol.x', 'repo'), { recursive: true })
    mkdirSync(join(src, 'sb1', 'home', '.config'), { recursive: true })
    writeFileSync(join(src, 'sb1', 'vol.x', 'repo', 'a.js'), 'a\n')
    writeFileSync(join(src, 'sb1', 'home', '.config', 'b'), 'b\n')
    const tar = createTarZst(src, ['sb1/vol.x', 'sb1/home'], [], prefixTransforms([['sb1/vol.x', 'workspace'], ['sb1/home', 'home/flow']]))
    const chunks: Uint8Array[] = []
    for await (const chunk of tar.stream as unknown as AsyncIterable<Uint8Array>) chunks.push(chunk)
    await tar.done
    const archive = Buffer.concat(chunks)
    // A container runner unpacks it unchanged.
    const container = join(tmp.path, 'container')
    mkdirSync(container)
    await extractTarZst(archive, container)
    assert.equal(readFileSync(join(container, 'workspace', 'repo', 'a.js'), 'utf8'), 'a\n')
    assert.equal(readFileSync(join(container, 'home', 'flow', '.config', 'b'), 'utf8'), 'b\n')
    // Another local sandbox renames it to its own paths ("." in a path is literal).
    const other = join(tmp.path, 'other')
    mkdirSync(other)
    await extractTarZst(archive, other, prefixTransforms([['workspace', 'sb2/volxy'], ['home/flow', 'sb2/home']]))
    assert.equal(readFileSync(join(other, 'sb2', 'volxy', 'repo', 'a.js'), 'utf8'), 'a\n')
    assert.equal(readFileSync(join(other, 'sb2', 'home', '.config', 'b'), 'utf8'), 'b\n')
  } finally {
    tmp.cleanup()
  }
})

test('the container layout needs no transforms', () => {
  const container = { workspace: '/workspace', repo: '/workspace/repo', home: '/home/flow', flowDir: '/home/flow/.flow', binDir: '/opt/flow/bin' }
  assert.deepEqual(toArchiveLayout(container), [])
  assert.deepEqual(fromArchiveLayout(container), [])
  const local = { ...container, workspace: '/tmp/s,1/ws', home: '/tmp/s,1/home' }
  assert.deepEqual(toArchiveLayout(local), [
    '--transform=s,^tmp/s\\,1/ws$,workspace,S',
    '--transform=s,^tmp/s\\,1/ws/,workspace/,S',
    '--transform=s,^tmp/s\\,1/home$,home/flow,S',
    '--transform=s,^tmp/s\\,1/home/,home/flow/,S',
  ])
})
