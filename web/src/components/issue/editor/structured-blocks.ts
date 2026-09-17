import { Details, DetailsContent, DetailsSummary } from '@tiptap/extension-details'
import { TaskItem, TaskList } from '@tiptap/extension-list'

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
export type DescriptionSelectionActions = {
  onCreateIssue?: (selection: SelectedDescription) => void
  onAskAgent?: (selection: SelectedDescription) => void
  onComment?: (selection: SelectedDescription) => void
}
