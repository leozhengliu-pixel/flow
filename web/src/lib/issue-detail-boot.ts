import type { Issue } from '@/types/flow'

export type IssueDetailPane = 'editor' | 'preview' | 'checking-access' | 'loading' | 'not-found'

export function issueDetailPane({
  selectedIssue,
  previewIssue,
  accessPending,
  issueContextLoading,
  missingIssueRecord,
  contextReady,
}: {
  selectedIssue?: Issue | null
  previewIssue?: Issue
  accessPending: boolean
  issueContextLoading: boolean
  missingIssueRecord: boolean
  contextReady: boolean
}): IssueDetailPane {
  if (selectedIssue && !selectedIssue.isSummary) return 'editor'
  if (previewIssue && !previewIssue.isSummary) return 'preview'
  if (accessPending) return 'checking-access'
  if (issueContextLoading || (missingIssueRecord && !contextReady)) return 'loading'
  return 'not-found'
}
