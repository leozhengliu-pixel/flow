import { Suspense, useState } from 'react'
import { AgentChatPanel } from '@/lib/route-pages'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Composer } from '@/components/editor/composer'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue } from '@/types/flow'
import type { AgentPageContext } from '@/components/agent/agent-chat-panel'
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

/** What a project or initiative description is about; its selection actions act on this entity. */
export type DescriptionEntity = { kind: 'project' | 'initiative'; id: string; name: string; /** Path of the entity's page, linked from an issue made out of a selection. */ href?: string }

/**
 * Selection actions for a project or initiative description, like the issue description's: "Ask agent" opens the
 * floating chat on that entity with the selection quoted in the composer, "Create issue from selection" seeds
 * the New issue composer (linking the project when it is one), and "Comment" (when `onComment` is given) starts an
 * inline comment thread on the selection.
 */
export function useEntityDescriptionSelectionActions({ entity, data, teamId, projectId, onComment }: { entity: DescriptionEntity; data?: BootstrapData; teamId?: string; projectId?: string; onComment?: DescriptionSelectionActions['onComment'] }) {
  const { t } = useI18n()
  const [agent, setAgent] = useState<SelectedDescription & { entityId: string }>()
  const link = () => new URL(entity.href ?? window.location.pathname, window.location.origin).href
  const actions: DescriptionSelectionActions = {
    onCreateIssue: selection => {
      const team = teamId ?? data?.teams[0]?.id
      if (!team) return
      window.dispatchEvent(new CustomEvent(CREATE_ISSUE_FROM_SELECTION, { detail: { text: `${selection.text}\n\n${t('From')} ${link()}`, teamId: team, projectId: entity.kind === 'project' ? entity.id : projectId } }))
    },
    onAskAgent: selection => setAgent({ ...selection, entityId: entity.id }),
    onComment,
  }
  const context: AgentPageContext = { type: entity.kind, id: entity.id, label: entity.name }
  const prompt = (text: string) => `${t(entity.kind === 'project' ? 'About this selection from the project description:' : 'About this selection from the initiative description:')}\n\n> ${text.replace(/\n/g, '\n> ')}\n\n`
  const panels = agent?.entityId === entity.id
    ? <Suspense fallback={null}><AgentChatPanel key={`${entity.id}:${agent.from}`} data={data} initialPrompt={prompt(agent.text)} issues={[]} pageContext={context} open onClose={() => setAgent(undefined)}/></Suspense>
    : null
  return { actions, panels }
}
