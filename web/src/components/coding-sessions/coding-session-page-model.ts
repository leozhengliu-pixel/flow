import { useEffect, useState } from 'react'
import { fetchCodingSessionDiff } from '@/lib/coding-sessions-api'
import { codingSessionPath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import type { CodingDiffFile, CodingSession, CodingSessionDiff } from '@/types/coding-sessions'

/** Session page helpers: the diff fetch and file tree, subscription state, the page URL and title. */

/** What decides whether the diff must be fetched again: the pushed head, the runner's summary and the latest PR. */
export function codingDiffRevision(session: Pick<CodingSession, 'id' | 'headSha' | 'diffs' | 'latestPullRequest' | 'branchName' | 'baseRef'>) {
  const summary = (session.diffs ?? []).map(diff => `${diff.path}:${diff.additions}:${diff.deletions}`).join('|')
  return [session.id, session.headSha ?? '', session.latestPullRequest?.headSha ?? '', session.branchName ?? '', session.baseRef ?? '', summary].join('\u0000')
}

/** The session's diff (GET /api/coding/sessions/{id}/diff), fetched again when the branch moves. */
export function useCodingSessionDiff(session: CodingSession) {
  const revision = codingDiffRevision(session)
  const [state, setState] = useState<{ revision: string; diff?: CodingSessionDiff; error?: string }>({ revision: '' })
  const sessionId = session.id
  useEffect(() => {
    const abort = new AbortController()
    fetchCodingSessionDiff(sessionId, abort.signal)
      .then(diff => { if (!abort.signal.aborted) setState({ revision, diff }) })
      .catch(cause => { if (!abort.signal.aborted) setState(previous => ({ revision, diff: previous.diff, error: cause instanceof Error ? cause.message : String(cause) })) })
    return () => abort.abort()
  }, [sessionId, revision])
  return { diff: state.diff, error: state.error, loading: state.revision !== revision }
}

export type CodingDiffTreeNode = { name: string; path: string; children: Map<string, CodingDiffTreeNode>; file?: { index: number; data: CodingDiffFile } }

/** The changed files as a directory tree; single-child directory chains are folded ("src/components"). */
export function codingDiffTree(files: CodingDiffFile[]): CodingDiffTreeNode {
  const root: CodingDiffTreeNode = { name: '', path: '', children: new Map() }
  files.forEach((file, index) => {
    const parts = file.path.split('/').filter(Boolean)
    let node = root
    parts.forEach((part, depth) => {
      const path = parts.slice(0, depth + 1).join('/')
      let child = node.children.get(part)
      if (!child) { child = { name: part, path, children: new Map() }; node.children.set(part, child) }
      if (depth === parts.length - 1) child.file = { index, data: file }
      node = child
    })
  })
  const fold = (node: CodingDiffTreeNode): CodingDiffTreeNode => {
    let current = node
    while (!current.file && current.children.size === 1 && current !== root) {
      const [only] = current.children.values()
      if (only.file) break
      current = { ...only, name: `${current.name}/${only.name}` }
    }
    const children = new Map([...current.children.values()].map(fold).map(child => [child.name, child] as const))
    return { ...current, children }
  }
  return fold(root)
}

/** Whether the viewer gets the session's notifications ("Subscribe to session"; the creator is subscribed unless they opt out). */
export function codingSessionSubscribed(session: Pick<CodingSession, 'creatorId' | 'subscriberIds' | 'unsubscribedIds'>, userId: string) {
  if (!userId || session.unsubscribedIds?.includes(userId)) return false
  return userId === session.creatorId || Boolean(session.subscriberIds?.includes(userId))
}

/** The session page's URL ("Copy › Session URL"). */
export function codingSessionURL(workspace: string, sessionId: string) {
  return `${typeof location === 'undefined' ? '' : location.origin}${codingSessionPath(workspace, sessionId)}`
}

export const codingSessionTitle = (session: CodingSession, data: Pick<BootstrapData, 'issues'>) =>
  session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || 'Coding session'
