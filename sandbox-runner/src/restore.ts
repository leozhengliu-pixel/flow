import { extractTarZst } from './archive.js'
import { errorMessage, type Logger } from './log.js'

// Restore mode (the `restore` init container, and warm sandboxes after
// binding): download the snapshot archive with the sandbox token and unpack
// it at "/" (the archive holds workspace/… and home/flow/…). A failure exits
// non-zero, which Flow maps to snapshotFailed (one retry, then inline setup).

/** Exit code of `flow-runner restore` when the archive cannot be restored (track A's Docker entrypoint contract). */
export const RESTORE_FAILED_EXIT = 86

export interface RestoreApi {
  getSnapshotArchive(snapshotId: string, signal?: AbortSignal, archiveUrl?: string): Promise<ReadableStream<Uint8Array>>
}

/** Downloads (FLOW_ARCHIVE_URL, or the snapshot archive route) and unpacks the archive. */
export async function restoreSnapshot(api: RestoreApi, snapshotId: string, logger: Logger, root = '/', archiveUrl?: string): Promise<void> {
  const started = Date.now()
  logger.info('restoring snapshot', { snapshotId, fromArchiveUrl: Boolean(archiveUrl) })
  try {
    const stream = await api.getSnapshotArchive(snapshotId, undefined, archiveUrl)
    await extractTarZst(stream, root)
  } catch (error) {
    throw new Error(`restoring snapshot ${snapshotId} failed: ${errorMessage(error)}`)
  }
  logger.info('snapshot restored', { snapshotId, ms: Date.now() - started })
}
