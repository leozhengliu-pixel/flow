import type { DescriptionSnapshot } from './editor-content'

export interface DescriptionRecovery {
  documentId: string
  version: number
  snapshot: DescriptionSnapshot
}

export function descriptionRecoveryKey(workspace: string, resource: string, viewerId: string) {
  return `flow:description-recovery:v2:${encodeURIComponent(viewerId)}:${encodeURIComponent(workspace)}:${encodeURIComponent(resource)}`
}

export function readDescriptionRecovery(key: string): DescriptionRecovery | undefined {
  try {
    const value = JSON.parse(sessionStorage.getItem(key) ?? 'null') as DescriptionRecovery | null
    if (value && typeof value.documentId === 'string' && typeof value.version === 'number' && typeof value.snapshot?.markdown === 'string' && typeof value.snapshot.contentState === 'string') return value
  } catch { /* Storage can be unavailable in private browsing. */ }
}

export function writeDescriptionRecovery(key: string, recovery: DescriptionRecovery) {
  try { sessionStorage.setItem(key, JSON.stringify(recovery)); return true }
  catch { return false }
}

/**
 * Drops the backup once a save holds it. `coveredBackup` is the state of the editor's own last backup when the saved
 * snapshot includes it (no local edit since): the backup is covered even though collaborators' edits merged in
 * afterwards made the saved state differ from it.
 */
export function clearDescriptionRecovery(key: string, snapshot: DescriptionSnapshot, coveredBackup?: string) {
  // A response to an earlier save must not erase newer local edits.
  const stored = readDescriptionRecovery(key)?.snapshot.contentState
  if (stored !== snapshot.contentState && (coveredBackup === undefined || stored !== coveredBackup)) return
  try { sessionStorage.removeItem(key) }
  catch { /* The live editor still owns its document. */ }
}

export function downloadDescriptionRecovery(snapshot: DescriptionSnapshot) {
  const url = URL.createObjectURL(new Blob([snapshot.markdown], { type: 'text/markdown;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'description-local-changes.md'
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}
