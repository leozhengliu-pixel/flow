import { registerSessionCacheClearer } from '@/lib/session-cache-registry'
import type { Issue } from '@/types/flow'

/** Related issues fetched for a hover card (the paged list only carries the ids), by workspace. */
export const fetched = new Map<string, { workspaceKey: string; pending: Promise<Issue | undefined> }>()

/** Drops fetched related issues on logout, account switch and workspace access loss (all, or one workspace's). */
export function clearRelatedIssueCache(_viewerId?: string, workspaceKey?: string) {
  for (const [key, entry] of [...fetched]) if (!workspaceKey || entry.workspaceKey === workspaceKey) fetched.delete(key)
}
registerSessionCacheClearer(clearRelatedIssueCache)

export function relatedIssueCacheSize() { return fetched.size }

