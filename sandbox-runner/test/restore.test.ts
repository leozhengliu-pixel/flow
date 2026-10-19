import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { createTarZst, extractTarZst, fromArchiveLayout, prefixTransforms, tarZstBuffer, toArchiveLayout } from '../src/archive.js'
import { silentLogger } from '../src/log.js'
import { RESTORE_FAILED_EXIT, restoreSnapshot } from '../src/restore.js'
import { RuntimeClient } from '../src/runtime-client.js'
import { MAIN_TS, startServer, tempDir, TSX_BIN } from './helpers.js'

// Restore mode: FLOW_ARCHIVE_URL (track A) or the snapshot archive route,
// and exit code 86 when the archive cannot be restored.

test('restoreSnapshot downloads from FLOW_ARCHIVE_URL with the sandbox token and unpacks it', async () => {
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

test('snapshot archives keep the container layout when the runner paths differ (local provider)', async () => {
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
