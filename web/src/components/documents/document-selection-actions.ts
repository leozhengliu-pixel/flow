import type { DescriptionSelectionActions } from '@/components/issue/editor/structured-blocks'
import { CREATE_ISSUE_FROM_SELECTION } from '@/components/detail/description-selection-actions'
import { documentPath } from '@/lib/app-routes'
import type { BootstrapData, FlowDocument } from '@/types/flow'

type Translate = (source: string) => string

/** The floating-toolbar actions of a document: an issue seeded from the selection (with a link back) and the document agent. */
export function documentSelectionActions({ data, document, t, onAskAgent, onComment }: {
  data: Pick<BootstrapData, 'teams' | 'workspace'>
  document: Pick<FlowDocument, 'id' | 'slugId' | 'title' | 'teamIds' | 'projectIds'>
  t: Translate
  /** Opens the agent chat with the selected text quoted. */
  onAskAgent: (selectedText: string) => void
  /** Starts an inline comment on the selection (omitted for viewers). */
  onComment?: DescriptionSelectionActions['onComment']
}): DescriptionSelectionActions {
  return {
    onComment,
    onCreateIssue: selection => {
      const link = new URL(documentPath(data.workspace.urlKey, document as FlowDocument), window.location.origin).href
      window.dispatchEvent(new CustomEvent(CREATE_ISSUE_FROM_SELECTION, {
        detail: { text: `${selection.text}\n\n${t('From')} ${link}`, teamId: document.teamIds[0] ?? data.teams[0]?.id, projectId: document.projectIds[0] },
      }))
    },
    // Like the reference app, the selection rides into the floating chat as a "selected text" chip, not as prefilled prose.
    onAskAgent: selection => { if (selection.text.trim()) onAskAgent(selection.text) },
  }
}
