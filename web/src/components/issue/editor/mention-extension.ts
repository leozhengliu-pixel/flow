import { Node, createInlineMarkdownSpec, mergeAttributes } from '@tiptap/core'
import { mentionMarkdown, mentionText } from '@/components/editor/mentions/mention-model'

const mentionShortcode = createInlineMarkdownSpec({
  nodeName: 'mention',
  selfClosing: true,
  allowedAttributes: ['id', 'label', 'href', 'title', 'mentionType'],
})

function mentionAttributes(node: HTMLElement) {
  return {
    id: node.getAttribute('data-flow-mention') ?? '',
    label: node.getAttribute('data-mention-label') ?? node.textContent?.replace(/^@/, '') ?? '',
    href: node.getAttribute('data-mention-href') ?? '',
    title: node.getAttribute('data-mention-title') ?? '',
    mentionType: node.getAttribute('data-mention-type') ?? 'user',
  }
}

/**
 * The inline reference node every rich-text surface shares (people, issues, projects, documents, …): an atom, so
 * identity survives markdown projections and collaborative Yjs updates. `mentionType` names the resource kind and
 * `href` its in-app path. Markdown carries people as `@name` and every other resource as `[label](path)`, which
 * `convertMentionLinks` turns back into a mention wherever markdown is loaded; plain text carries the label.
 * This is the schema only: the React editors extend it with the chip view (`MentionChipNode`).
 */
export const MentionExtension = Node.create({
  name: 'mention',
  inline: true,
  group: 'inline',
  atom: true,
  selectable: false,
  draggable: true,
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
    return [
      { tag: 'span[data-flow-mention]', getAttrs: node => node instanceof HTMLElement ? mentionAttributes(node) : false },
      { tag: 'a[data-flow-mention]', priority: 60, getAttrs: node => node instanceof HTMLElement ? mentionAttributes(node) : false },
    ]
  },
  renderHTML({ HTMLAttributes }) {
    const label = typeof HTMLAttributes.label === 'string' ? HTMLAttributes.label : ''
    const mentionType = typeof HTMLAttributes.mentionType === 'string' ? HTMLAttributes.mentionType : 'user'
    const href = typeof HTMLAttributes.href === 'string' ? HTMLAttributes.href : ''
    const attributes = mergeAttributes(HTMLAttributes, {
      'data-flow-mention': HTMLAttributes.id ?? '',
      'data-mention-label': label,
      'data-mention-href': href,
      'data-mention-title': HTMLAttributes.title ?? '',
      'data-mention-type': mentionType,
      class: mentionType === 'issue' ? 'flow-mention flow-mention--issue' : 'flow-mention',
    })
    // Pasted into another app a resource reads as a link; people stay @name text.
    if (mentionType !== 'user' && href) return ['a', mergeAttributes(attributes, { href }), label]
    return ['span', attributes, `${mentionType === 'user' ? '@' : ''}${label}`]
  },
  renderText({ node }) {
    return mentionText(node.attrs)
  },
  markdownTokenName: mentionShortcode.markdownTokenizer.name,
  parseMarkdown: mentionShortcode.parseMarkdown,
  markdownTokenizer: mentionShortcode.markdownTokenizer,
  renderMarkdown: node => mentionMarkdown(node.attrs ?? {}),
})
