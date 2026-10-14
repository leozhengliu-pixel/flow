import type { Editor } from '@tiptap/core'
import type { DescriptionSelectionActions, SelectedDescription } from '@/components/issue/editor/structured-blocks'
import { createIssueFromSelectionAction } from '@/components/editor/selection-issue'
import type { BootstrapData, FlowDocument } from '@/types/flow'

type Translate = (source: string) => string

/**
 * The floating-toolbar actions of a document: "Create issue from selection" makes the issue right away
 * (in the document's team and project, else the first team) and puts a mention of it where the text was; "Ask agent"
 * hands the selection to the floating chat.
 */
export function documentSelectionActions({ data, document, t, onAskAgent, onComment }: {
  data: BootstrapData | undefined
  document: Pick<FlowDocument, 'teamIds' | 'projectIds'>
  t: Translate
  /** Opens the agent chat with the selected text quoted. */
  onAskAgent: (selection: SelectedDescription, editor?: Editor) => void
  /** Starts an inline comment on the selection (omitted for viewers). */
  onComment?: DescriptionSelectionActions['onComment']
}): DescriptionSelectionActions {
  return {
    onComment,
    onCreateIssue: createIssueFromSelectionAction(data, { teamId: document.teamIds[0] ?? data?.teams[0]?.id, projectId: document.projectIds[0] }, t),
    // Like the reference app, the selection rides into the floating chat as a "selected text" chip, not as prefilled prose.
    onAskAgent: (selection, editor) => { if (selection.text.trim()) onAskAgent(selection, editor) },
  }
}
