import { Node, createInlineMarkdownSpec, mergeAttributes } from '@tiptap/core'

const mentionMarkdown = createInlineMarkdownSpec({ nodeName: 'mention', selfClosing: true, allowedAttributes: ['id', 'label'] })

/**
 * Inline user mentions are stored as an atom so the user id survives markdown
 * projections and collaborative Yjs updates. The markdown shortcode keeps the
 * identity when content is exported, while the regular @name tokenizer makes
 * hand-written markdown import naturally as a mention.
 */
export const MentionExtension = Node.create({
  name: 'mention',
  inline: true,
  group: 'inline',
  atom: true,
  selectable: false,
  addAttributes() {
    return {
      id: { default: '' },
      label: { default: '' },
    }
  },
  parseHTML() {
    return [{ tag: 'span[data-flow-mention]', getAttrs: node => ({ id: node.getAttribute('data-flow-mention') ?? '', label: node.textContent?.replace(/^@/, '') ?? '' }) }]
  },
  renderHTML({ HTMLAttributes }) {
    const label = typeof HTMLAttributes.label === 'string' ? HTMLAttributes.label : ''
    return ['span', mergeAttributes(HTMLAttributes, { 'data-flow-mention': HTMLAttributes.id ?? '', class: 'flow-mention' }), `@${label}`]
  },
  markdownTokenName: mentionMarkdown.markdownTokenizer.name,
  parseMarkdown: mentionMarkdown.parseMarkdown,
  markdownTokenizer: mentionMarkdown.markdownTokenizer,
  renderMarkdown: node => `@${String(node.attrs?.label ?? '')}`,
})
