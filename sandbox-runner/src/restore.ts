import { extractTarZst, fromArchiveLayout } from './archive.js'
import type { RunnerPaths } from './config.js'
import { errorMessage, type Logger } from './log.js'

// Restore mode (the `restore` init container, and warm sandboxes after
// binding): download the snapshot archive with the sandbox token and unpack
// it at "/" (the archive holds workspace/… and home/flow/…). A failure exits
// non-zero, which Flow maps to snapshotFailed (one retry, then inline setup).

/** Exit code of `flow-runner restore` when the archive cannot be restored (track A's Docker entrypoint contract). */
export const RESTORE_FAILED_EXIT = 86

/**
 * Marker the runner writes when its main mode starts, i.e. after the restore
 * step succeeded (sandbox.RunnerReadyPath). The debug terminal waits for it.
 */
export const RUNNER_READY_FILE = '/tmp/flow-runner-ready'

const ARCHIVE_ROUTE = /\/internal\/coding-runtime\/snapshots\/([^/?#]+)\/archive(?:[?#]|$)/

/** The snapshot id of a runtime archive URL (…/snapshots/{id}/archive), or undefined for another URL. */
export function snapshotIdFromArchiveUrl(archiveUrl: string | undefined): string | undefined {
  if (!archiveUrl) return undefined
  const match = ARCHIVE_ROUTE.exec(archiveUrl)
  if (!match) return undefined
  try {
    return decodeURIComponent(match[1]!) || undefined
  } catch {
    return undefined
  }
}

/**
 * The snapshot `flow-runner restore` restores: FLOW_SNAPSHOT_ID, else the id
 * in FLOW_ARCHIVE_URL. Flow grants the sandbox token one snapshot and the
 * archive route refuses any other id, so an id that disagrees with the URL is
 * an error rather than a guess.
 */
export function restoreTarget(env: NodeJS.ProcessEnv): { snapshotId?: string; archiveUrl?: string } {
  const archiveUrl = env.FLOW_ARCHIVE_URL || undefined
  const fromUrl = snapshotIdFromArchiveUrl(archiveUrl)
  const explicit = env.FLOW_SNAPSHOT_ID || undefined
  if (explicit && fromUrl && explicit !== fromUrl) {
    throw new Error(`FLOW_SNAPSHOT_ID ${explicit} does not match FLOW_ARCHIVE_URL (snapshot ${fromUrl})`)
  }
  if (archiveUrl && !explicit && !fromUrl) {
    throw new Error('FLOW_ARCHIVE_URL does not name a snapshot; set FLOW_SNAPSHOT_ID')
  }
  return { snapshotId: explicit || fromUrl, archiveUrl }
}

export interface RestoreApi {
  getSnapshotArchive(snapshotId: string, signal?: AbortSignal, archiveUrl?: string): Promise<ReadableStream<Uint8Array>>
}

/** Downloads (FLOW_ARCHIVE_URL, or the snapshot archive route) and unpacks the archive. */
export async function restoreSnapshot(api: RestoreApi, snapshotId: string, logger: Logger, root = '/', archiveUrl?: string, paths?: RunnerPaths): Promise<void> {
  const started = Date.now()
  logger.info('restoring snapshot', { snapshotId, fromArchiveUrl: Boolean(archiveUrl) })
  try {
    const stream = await api.getSnapshotArchive(snapshotId, undefined, archiveUrl)
    // Archives hold workspace/… and home/flow/…; a runner with other paths renames them.
    await extractTarZst(stream, root, paths && root === '/' ? fromArchiveLayout(paths) : [])
  } catch (error) {
    throw new Error(`restoring snapshot ${snapshotId} failed: ${errorMessage(error)}`)
  }
  logger.info('snapshot restored', { snapshotId, ms: Date.now() - started })
}
