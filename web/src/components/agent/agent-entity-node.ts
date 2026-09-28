import { Node, createInlineMarkdownSpec } from '@tiptap/core'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { AGENT_ENTITY_NODE } from './agent-answer-content'
import { AgentEntityChipView } from './agent-entity-chip'

const entityMarkdown = createInlineMarkdownSpec({
  nodeName: AGENT_ENTITY_NODE,
  selfClosing: true,
  allowedAttributes: ['kind', 'id', 'label'],
})

/** Read-only inline atom for issue / project references in agent answers (`[agentEntity kind="issue" id="…" label="…"]`). */
export const AgentEntityNode = Node.create({
  name: AGENT_ENTITY_NODE,
  inline: true,
  group: 'inline',
  atom: true,
  selectable: false,
  addAttributes() {
    return {
      kind: { default: 'issue' },
      id: { default: '' },
      label: { default: '' },
    }
  },
  parseHTML() {
    return [{ tag: 'span[data-agent-entity-node]' }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['span', { 'data-agent-entity-node': HTMLAttributes.kind }, String(HTMLAttributes.label ?? '')]
  },
  renderText({ node }) {
    return String(node.attrs.label ?? '')
  },
  addNodeView() {
    return ReactNodeViewRenderer(AgentEntityChipView, { as: 'span' })
  },
  markdownTokenName: entityMarkdown.markdownTokenizer.name,
  parseMarkdown: entityMarkdown.parseMarkdown,
  markdownTokenizer: entityMarkdown.markdownTokenizer,
  renderMarkdown: entityMarkdown.renderMarkdown,
})
