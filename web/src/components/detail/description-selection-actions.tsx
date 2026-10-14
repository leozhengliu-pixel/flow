import { useState } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Composer } from '@/components/editor/composer'
import { askAgentAboutSelection, createIssueFromSelectionAction } from '@/components/editor/selection-issue'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue } from '@/types/flow'
import type { DescriptionSelectionActions, SelectedDescription } from '@/components/issue/editor/structured-blocks'

/** Opens the New issue dialog seeded with text (a comment's "Create issue from comment"); App listens for it. */
export const CREATE_ISSUE_FROM_SELECTION = 'flow-create-issue-from-selection'

/**
 * Selection actions for an issue description: "Create issue from selection" makes a sub-issue in the
 * issue's team and project right away (first line title, the rest description) and puts a mention of it where the
 * text was; "Ask agent" opens the floating chat with the selection as a quote chip; "Comment" starts a comment on it.
 */
export function useDescriptionSelectionActions(issue: Issue, data: BootstrapData, onComment: (body: string, bodyData?: Record<string,unknown>) => Promise<void>, onUpload?: (file: File) => Promise<string>) {
  const { t } = useI18n()
  const [comment, setComment] = useState<SelectedDescription & { issueId: string }>()
  const actions: DescriptionSelectionActions = {
    onCreateIssue: createIssueFromSelectionAction(data, { teamId: issue.team.id, projectId: issue.project?.id, parentId: issue.id }, t),
    // The issue rides along as context even where the page is not the issue's own (a peek, the inbox).
    onAskAgent: (selection, editor) => askAgentAboutSelection(selection, editor, { issueId: issue.id }),
    onComment: selection => setComment({ ...selection, issueId: issue.id }),
  }
  const panels = <Dialog open={Boolean(comment && comment.issueId === issue.id)} onOpenChange={open => { if (!open) setComment(undefined) }}><DialogContent className="description-selection-comment" aria-describedby={undefined}><DialogTitle>{t('Comment on selection')}</DialogTitle>{comment && <><blockquote>{comment.text}</blockquote><Composer key={`${issue.id}:${comment.from}`} users={data.users} placeholder={t('Add a comment…')} onUpload={onUpload} onCancel={() => setComment(undefined)} onSubmit={async (body, bodyData) => { await onComment(body, { ...bodyData, selection: { ...comment } }); setComment(undefined) }}/></>}</DialogContent></Dialog>
  return { actions, panels }
}

/** What a project or initiative description is about; its selection actions act on this entity. */
export type DescriptionEntity = { kind: 'project' | 'initiative'; id: string; name: string; /** Path of the entity's page. */ href?: string }

/**
 * Selection actions for a project or initiative description: "Create issue from selection" makes the
 * issue right away (in the project when it is one, else the given or first team, in its default status) and puts a
 * mention of it where the text was; "Ask agent" opens the floating chat on the page with the selection as a quote
 * chip; and "Comment" (when `onComment` is given) starts an inline comment thread on the selection.
 */
export function useEntityDescriptionSelectionActions({ entity, data, teamId, projectId, onComment }: { entity: DescriptionEntity; data?: BootstrapData; teamId?: string; projectId?: string; onComment?: DescriptionSelectionActions['onComment'] }) {
  const { t } = useI18n()
  const team = teamId ?? data?.teams[0]?.id
  const actions: DescriptionSelectionActions = {
    onCreateIssue: team ? createIssueFromSelectionAction(data, { teamId: team, projectId: entity.kind === 'project' ? entity.id : projectId }, t) : undefined,
    onAskAgent: (selection, editor) => askAgentAboutSelection(selection, editor),
    onComment,
  }
  return { actions, panels: null }
}
