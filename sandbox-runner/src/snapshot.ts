import { relative } from 'node:path'
import { createTarZst, snapshotExcludes, toArchiveLayout } from './archive.js'
import { backoffDelay, sleep } from './backoff.js'
import type { RunnerConfig } from './config.js'
import { baseEnv, writeRuntimeInfo } from './environment.js'
import type { Git } from './git/git.js'
import { errorMessage, silentLogger, type Logger } from './log.js'
import { EventFactory } from './mapping/common.js'
import type { BootstrapResponse, HeartbeatRequest, HeartbeatResponse, SetupSecretsResponse, SnapshotCompleteRequest } from './protocol.js'
import { AuthError } from './runtime-client.js'
import { prepareRepository, runSetup, SetupError, SetupLog } from './setup.js'

// Snapshot mode (§10, §16 step 7): clone at the base revision, run the full
// setup with FLOW_SNAPSHOT=1 / LINEAR_SNAPSHOT=1, then (archive strategy)
// stream `tar --zstd` of /workspace and $HOME to Flow, and report the result.
// Secrets only ever lived in the prepare script's environment, so nothing is
// left to unset before archiving. With the volumesnapshot strategy Flow
// snapshots the PVC after the runner exits.

export interface SnapshotApi {
  heartbeat(request: HeartbeatRequest, signal?: AbortSignal): Promise<HeartbeatResponse>
  setupSecrets(): Promise<SetupSecretsResponse>
  putSnapshotArchive(snapshotId: string, body: ReadableStream<Uint8Array>, uploadUrl?: string, signal?: AbortSignal): Promise<void>
  postSnapshotLogs(snapshotId: string, lines: string[]): Promise<void>
  completeSnapshot(snapshotId: string, request: SnapshotCompleteRequest): Promise<void>
}

export interface SnapshotDeps {
  config: RunnerConfig
  api: SnapshotApi
  bootstrap: BootstrapResponse
  git: Git
  logger?: Logger
  /** Replaces the tar upload (tests). Returns the archive size. */
  upload?: () => Promise<number>
  uploadAttempts?: number
  heartbeatMs?: number
}

/** Batches setup log lines to POST /snapshots/{id}/logs. */
export class SnapshotLogShipper {
  private lines: string[] = []
  private timer: NodeJS.Timeout | undefined
  private sending: Promise<void> = Promise.resolve()

  constructor(
    private readonly send: (lines: string[]) => Promise<void>,
    private readonly logger: Logger = silentLogger,
    private readonly batch = 50,
    private readonly intervalMs = 1000,
  ) {}

  push(line: string): void {
    this.lines.push(line)
    if (this.lines.length >= this.batch) this.flushSoon(0)
    else this.flushSoon(this.intervalMs)
  }

  private flushSoon(delay: number): void {
    if (this.timer && delay > 0) return
    if (this.timer) clearTimeout(this.timer)
    this.timer = setTimeout(() => {
      this.timer = undefined
      void this.flush()
    }, delay)
    this.timer.unref?.()
  }

  flush(): Promise<void> {
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
    const lines = this.lines
    this.lines = []
    if (lines.length) {
      this.sending = this.sending.then(() => this.send(lines).catch((error) => this.logger.warn('posting snapshot logs failed', { error: errorMessage(error) })))
    }
    return this.sending
  }
}

export async function runSnapshot(deps: SnapshotDeps): Promise<number> {
  const { config, api, bootstrap, git } = deps
  const logger = deps.logger ?? silentLogger
  const snapshot = bootstrap.snapshot ?? (bootstrap.snapshotId ? { id: bootstrap.snapshotId, strategy: 'archive' } : undefined)
  if (!snapshot) {
    logger.error('snapshot mode without a snapshot in bootstrap')
    return 1
  }
  const shipper = new SnapshotLogShipper((lines) => api.postSnapshotLogs(snapshot.id, lines), logger)
  const log = new SetupLog((line) => {
    logger.info('snapshot', { line })
    shipper.push(line)
  })
  const heartbeats = new AbortController()
  const heartbeatLoop = (async () => {
    const interval = deps.heartbeatMs ?? Math.max(1, bootstrap.limits.heartbeatSeconds || 15) * 1000
    while (!heartbeats.signal.aborted) {
      try {
        const response = await api.heartbeat({ phase: 'snapshotting' }, heartbeats.signal)
        if (response.stop) {
          log.line('snapshot build canceled')
          process.kill(process.pid, 'SIGTERM')
        }
      } catch (error) {
        if (error instanceof AuthError) return
      }
      await sleep(interval, heartbeats.signal).catch(() => undefined)
    }
  })()

  const env = baseEnv(config, bootstrap, { snapshot: true })
  const events = new EventFactory({})
  let result: SnapshotCompleteRequest
  try {
    await writeRuntimeInfo(config)
    const setupDeps = {
      bootstrap: { ...bootstrap, restored: false },
      paths: config.paths,
      git,
      env,
      log,
      emit: () => {},
      events,
      setupSecrets: () => api.setupSecrets(),
      deadline: Date.now() + Math.max(60, bootstrap.limits.setupTimeoutSeconds || 1800) * 1000,
    }
    await prepareRepository(setupDeps, { detach: true })
    await runSetup(setupDeps)
    const baseSha = await git.head()
    let sizeBytes = 0
    if (snapshot.strategy !== 'volumesnapshot') {
      log.line('archiving workspace and home')
      sizeBytes = await uploadWithRetry(deps, snapshot.id, snapshot.uploadUrl, log)
      log.line(`uploaded snapshot archive (${sizeBytes} bytes)`)
    }
    result = { status: 'ready', baseSha, sizeBytes: sizeBytes || undefined }
  } catch (error) {
    const setup = error instanceof SetupError
    const message = setup ? error.message : `Snapshot failed: ${errorMessage(error)}`
    log.line(message)
    result = { status: 'failed', errorCode: setup ? 'environmentSetupFailed' : 'snapshotFailed', error: message }
  }
  await shipper.flush()
  heartbeats.abort()
  await heartbeatLoop
  await api.completeSnapshot(snapshot.id, result)
  logger.info('snapshot complete', { status: result.status })
  return result.status === 'ready' ? 0 : 1
}

async function uploadWithRetry(deps: SnapshotDeps, snapshotId: string, uploadUrl: string | undefined, log: SetupLog): Promise<number> {
  const attempts = deps.uploadAttempts ?? 3
  for (let attempt = 0; ; attempt++) {
    try {
      if (deps.upload) return await deps.upload()
      const { paths } = deps.config
      const entries = [relative('/', paths.workspace), relative('/', paths.home)]
      const tar = createTarZst('/', entries, snapshotExcludes(paths), toArchiveLayout(paths))
      const upload = deps.api.putSnapshotArchive(snapshotId, tar.stream, uploadUrl)
      await Promise.all([upload, tar.done])
      return tar.bytes()
    } catch (error) {
      if (error instanceof AuthError || attempt + 1 >= attempts) throw error
      log.line(`archive upload failed (${errorMessage(error)}), retrying`)
      await sleep(backoffDelay(attempt, 2000, 30_000))
    }
  }
}
