import { Node, createInlineMarkdownSpec, mergeAttributes } from '@tiptap/core'

const mentionMarkdown = createInlineMarkdownSpec({
  nodeName: 'mention',
  selfClosing: true,
  allowedAttributes: ['id', 'label', 'href', 'title', 'mentionType'],
})

/**
 * Inline mentions are stored as an atom so identity survives markdown
 * projections and collaborative Yjs updates. User mentions keep @name;
 * issue/entity mentions (LS-0407) carry href/title when hydrated from URLs.
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
      href: { default: '' },
      title: { default: '' },
      mentionType: { default: 'user' },
    }
  },
  parseHTML() {
    return [{
      tag: 'span[data-flow-mention]',
      getAttrs: (node) => {
        if (!(node instanceof HTMLElement)) return false
        return {
          id: node.getAttribute('data-flow-mention') ?? '',
          label: node.getAttribute('data-mention-label') ?? node.textContent?.replace(/^@/, '') ?? '',
          href: node.getAttribute('data-mention-href') ?? '',
          title: node.getAttribute('data-mention-title') ?? '',
          mentionType: node.getAttribute('data-mention-type') ?? 'user',
        }
      },
    }]
  },
  renderHTML({ HTMLAttributes }) {
    const label = typeof HTMLAttributes.label === 'string' ? HTMLAttributes.label : ''
    const mentionType = typeof HTMLAttributes.mentionType === 'string' ? HTMLAttributes.mentionType : 'user'
    const prefix = mentionType === 'user' ? '@' : ''
    return [
      'span',
      mergeAttributes(HTMLAttributes, {
        'data-flow-mention': HTMLAttributes.id ?? '',
        'data-mention-label': label,
        'data-mention-href': HTMLAttributes.href ?? '',
        'data-mention-title': HTMLAttributes.title ?? '',
        'data-mention-type': mentionType,
        class: mentionType === 'issue' ? 'flow-mention flow-mention--issue' : 'flow-mention',
      }),
      `${prefix}${label}`,
    ]
  },
  renderText({ node }) {
    const mentionType = String(node.attrs.mentionType ?? 'user')
    const label = String(node.attrs.label ?? '')
    return mentionType === 'user' ? `@${label}` : label
  },
  markdownTokenName: mentionMarkdown.markdownTokenizer.name,
  parseMarkdown: mentionMarkdown.parseMarkdown,
  markdownTokenizer: mentionMarkdown.markdownTokenizer,
  renderMarkdown: (node) => {
    const mentionType = String(node.attrs?.mentionType ?? 'user')
    const label = String(node.attrs?.label ?? '')
    return mentionType === 'user' ? `@${label}` : label
  },
})
