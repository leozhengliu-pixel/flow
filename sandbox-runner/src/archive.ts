import { spawn } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { isAbsolute, join, relative } from 'node:path'
import { Readable } from 'node:stream'
import { pipeline } from 'node:stream/promises'
import type { RunnerPaths } from './config.js'

// tar --zstd helpers for snapshot archives and harness state.

export class ArchiveError extends Error {}

/** Default exclusions for snapshot archives, relative to "/" (as tar sees them). */
export function defaultSnapshotExcludes(paths: RunnerPaths): string[] {
  const home = rel(paths.home)
  return [
    `${home}/.cache/ms-playwright`,
    `${home}/.flow/cli-env`,
    `${home}/.flow/cli-env.json`,
    `${home}/.flow/elicitation-*`,
    `${home}/.flow/runtime.json`,
    `${home}/.claude/projects`,
    `${home}/.codex/sessions`,
    `${home}/.local/share/opencode`,
  ]
}

function rel(path: string): string {
  return relative('/', path) || '.'
}

/**
 * Reads <repo>/.flow/snapshot-exclude: one pattern per line, # comments.
 * `~/x` is relative to $HOME, `/x` absolute, anything else relative to the repo.
 */
export function snapshotExcludes(paths: RunnerPaths): string[] {
  const file = join(paths.repo, '.flow', 'snapshot-exclude')
  const out = defaultSnapshotExcludes(paths)
  if (!existsSync(file)) return out
  for (const raw of readFileSync(file, 'utf8').split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    if (line.includes('..')) continue
    if (line.startsWith('~/')) out.push(rel(join(paths.home, line.slice(2))))
    else if (isAbsolute(line)) out.push(rel(line))
    else out.push(rel(join(paths.repo, line)))
  }
  return out
}

export interface TarStream {
  stream: ReadableStream<Uint8Array>
  /** Resolves when tar exits; rejects on a fatal tar error. */
  done: Promise<void>
  bytes: () => number
}

/** Streams `tar --zstd -c` of `entries` (relative to `cwd`). Missing entries are skipped. */
export function createTarZst(cwd: string, entries: string[], excludes: string[] = []): TarStream {
  const present = entries.filter((entry) => existsSync(join(cwd, entry)))
  const args = ['--zstd', '-cf', '-', '--warning=no-file-changed', '--ignore-failed-read', ...excludes.map((pattern) => `--exclude=${pattern}`), '-C', cwd]
  let stderr = ''
  let bytes = 0
  const child = spawn('tar', present.length ? [...args, '--', ...present] : [...args, '--files-from=/dev/null'], { stdio: ['ignore', 'pipe', 'pipe'] })
  child.stderr.on('data', (chunk: Buffer) => {
    stderr = (stderr + chunk.toString()).slice(-4000)
  })
  child.stdout.on('data', (chunk: Buffer) => {
    bytes += chunk.length
  })
  const done = new Promise<void>((resolve, reject) => {
    child.once('error', (error) => reject(new ArchiveError(`tar failed to start: ${error.message}`)))
    child.once('close', (code) => {
      // GNU tar exits 1 when files changed while being read: not fatal here.
      if (code === 0 || code === 1) resolve()
      else reject(new ArchiveError(`tar exited with ${code}: ${stderr.trim()}`))
    })
  })
  return { stream: Readable.toWeb(child.stdout) as ReadableStream<Uint8Array>, done, bytes: () => bytes }
}

/** Buffers a small archive (harness state). */
export async function tarZstBuffer(cwd: string, entries: string[], maxBytes: number): Promise<Uint8Array | null> {
  if (!entries.some((entry) => existsSync(join(cwd, entry)))) return null
  const tar = createTarZst(cwd, entries)
  const chunks: Uint8Array[] = []
  let size = 0
  const reader = tar.stream.getReader()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    size += value.length
    if (size > maxBytes) {
      await reader.cancel()
      throw new ArchiveError(`archive exceeds ${maxBytes} bytes`)
    }
    chunks.push(value)
  }
  await tar.done
  return Buffer.concat(chunks)
}

/** Unpacks a tar.zst stream into `dest`. */
export async function extractTarZst(source: ReadableStream<Uint8Array> | Uint8Array, dest: string): Promise<void> {
  const child = spawn('tar', ['--zstd', '-xf', '-', '-C', dest, '--no-same-owner', '--no-same-permissions', '--delay-directory-restore'], {
    stdio: ['pipe', 'ignore', 'pipe'],
  })
  let stderr = ''
  child.stderr.on('data', (chunk: Buffer) => {
    stderr = (stderr + chunk.toString()).slice(-4000)
  })
  const exited = new Promise<number | null>((resolve, reject) => {
    child.once('error', reject)
    child.once('close', resolve)
  })
  const input = source instanceof Uint8Array ? Readable.from([Buffer.from(source)]) : Readable.fromWeb(source as import('node:stream/web').ReadableStream<Uint8Array>)
  await pipeline(input, child.stdin)
  const code = await exited
  if (code !== 0) throw new ArchiveError(`tar extract exited with ${code}: ${stderr.trim()}`)
}
