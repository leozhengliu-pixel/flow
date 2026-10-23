import { fetchIssueRecord } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import type { Issue } from '@/types/flow'

/**
 * Reloads an issue after the server removed the viewer from its description document: the issue is refreshed if
 * the viewer can still open it, or reported as lost (403/404) so the page shows its no-access state.
 */
export async function reloadIssueAfterRevocation(issueId: string, handlers: {
  onIssueUpdated?: (issue: Issue) => void
  onIssueAccessLost?: (issueId: string) => void
  fetchIssue?: (issueId: string) => Promise<Issue>
}) {
  const fetchIssue = handlers.fetchIssue ?? (id => fetchIssueRecord(id))
  try {
    handlers.onIssueUpdated?.(await fetchIssue(issueId))
  } catch (error) {
    if (error instanceof ApiError && (error.status === 403 || error.status === 404)) handlers.onIssueAccessLost?.(issueId)
  }
}
