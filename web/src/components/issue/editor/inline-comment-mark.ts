import { Mark, mergeAttributes } from '@tiptap/core'

export type InlineCommentMarkAttrs = {
  commentId: string | null
  resolved: boolean
}

/** TipTap mark for document/issue inline comment threads (LS-0704). */
export const InlineCommentMark = Mark.create({
  name: 'inlineComment',
  excludes: '',
  inclusive: false,
  keepOnSplit: true,
  addAttributes() {
    return {
      commentId: {
        default: null,
        parseHTML: (element) => element.getAttribute('data-comment-id'),
        renderHTML: (attributes) => (attributes.commentId ? { 'data-comment-id': attributes.commentId } : {}),
      },
      resolved: {
        default: false,
        parseHTML: (element) => element.getAttribute('data-resolved') === 'true',
        renderHTML: (attributes) => (attributes.resolved ? { 'data-resolved': 'true' } : {}),
      },
    }
  },
  parseHTML() {
    return [{ tag: 'span[data-comment-id]' }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes, { class: 'inline-comment-mark' }), 0]
  },
})
