import { Suspense, useState } from 'react'
import { AgentChatPanel } from '@/lib/route-pages'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Composer } from '@/components/editor/composer'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue } from '@/types/flow'
import type { DescriptionSelectionActions, SelectedDescription } from '@/components/issue/editor/structured-blocks'

export const CREATE_ISSUE_FROM_SELECTION = 'flow-create-issue-from-selection'

export function useDescriptionSelectionActions(issue: Issue, data: BootstrapData, onComment: (body: string, bodyData?: Record<string,unknown>) => Promise<void>, onUpload?: (file: File) => Promise<string>) {
  const { t } = useI18n()
  const [comment, setComment] = useState<SelectedDescription & { issueId: string }>()
  const [agent, setAgent] = useState<SelectedDescription & { issueId: string }>()
  const actions: DescriptionSelectionActions = {
    onCreateIssue: selection => window.dispatchEvent(new CustomEvent(CREATE_ISSUE_FROM_SELECTION, { detail: { ...selection, teamId: issue.team.id, projectId: issue.project?.id } })),
    onAskAgent: selection => setAgent({ ...selection, issueId: issue.id }),
    onComment: selection => setComment({ ...selection, issueId: issue.id }),
  }
  const panels = <>
    <Dialog open={Boolean(comment && comment.issueId === issue.id)} onOpenChange={open => { if (!open) setComment(undefined) }}><DialogContent className="description-selection-comment" aria-describedby={undefined}><DialogTitle>{t('Comment on selection')}</DialogTitle>{comment && <><blockquote>{comment.text}</blockquote><Composer key={`${issue.id}:${comment.from}`} users={data.users} placeholder={t('Add a comment…')} onUpload={onUpload} onCancel={() => setComment(undefined)} onSubmit={async (body, bodyData) => { await onComment(body, { ...bodyData, selection: { ...comment } }); setComment(undefined) }}/></>}</DialogContent></Dialog>
    {agent?.issueId === issue.id && <Suspense fallback={null}><AgentChatPanel key={`${issue.id}:${agent.from}`} initialPrompt={agent.text} issues={[issueToExplorerRow(issue,data.workspace.urlKey,data.issues,data)]} open onClose={() => setAgent(undefined)}/></Suspense>}
  </>
  return { actions, panels }
}
