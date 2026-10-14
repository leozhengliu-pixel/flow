import { Details, DetailsContent, DetailsSummary } from '@tiptap/extension-details'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import type { Editor } from '@tiptap/core'

const CollapsibleDetails = Details.extend({
  renderMarkdown: (node, helpers) => `<details${node.attrs?.open ? ' open' : ''}>\n${helpers.renderChildren(node.content ?? [])}\n</details>`,
}).configure({ persist: true })
const CollapsibleSummary = DetailsSummary.extend({
  renderMarkdown: (node, helpers) => `<summary>${helpers.renderChildren(node.content ?? [])}</summary>\n`,
})
const CollapsibleContent = DetailsContent.extend({
  renderMarkdown: (node, helpers) => helpers.renderChildren(node.content ?? []),
})
export const structuredBlocks = [TaskList, TaskItem.configure({ nested: true }), CollapsibleDetails, CollapsibleSummary, CollapsibleContent]
export type SelectedDescription = { text: string; from: number; to: number }
/** The floating toolbar's selection actions; each gets the selection and the editor it was made in (when known). */
export type DescriptionSelectionActions = {
  onCreateIssue?: (selection: SelectedDescription, editor?: Editor) => void
  onAskAgent?: (selection: SelectedDescription, editor?: Editor) => void
  onComment?: (selection: SelectedDescription, editor?: Editor) => void
}
