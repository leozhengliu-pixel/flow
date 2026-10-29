import { Node, mergeAttributes } from '@tiptap/core'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { TextSelection, type EditorState } from '@tiptap/pm/state'
import { MathBlockView } from './math-block-view'

export function isMathInput(state: EditorState) {
  const { $from } = state.selection
  return $from.parent.type.name === 'mathBlock' || $from.parent.type.name === 'paragraph' && $from.parent.textContent === '$$'
}

/** Source is text content, not an attribute, so concurrent edits merge through Yjs. */
export const MathBlockSchema = Node.create({
  name: 'mathBlock',
  group: 'block',
  content: 'text*',
  marks: '',
  code: true,
  defining: true,
  parseHTML: () => [{ tag: 'pre[data-math-block]', priority: 100, preserveWhitespace: 'full' }],
  renderHTML: ({ HTMLAttributes }) => ['pre', mergeAttributes(HTMLAttributes, { 'data-math-block': '' }), ['code', 0]],
  markdownTokenName: 'mathBlock',
  parseMarkdown: (token, helpers) => helpers.createNode('mathBlock', {}, token.text ? [{ type: 'text', text: String(token.text) }] : []),
  renderMarkdown: node => `$$\n${(node.content ?? []).map(child => child.text ?? '').join('')}\n$$`,
  markdownTokenizer: {
    name: 'mathBlock',
    level: 'block',
    start: (source: string) => source.search(/^[ \t]*\$\$[ \t]*\r?\n/m),
    tokenize(source: string) {
      const match = /^[ \t]*\$\$[ \t]*\r?\n([\s\S]*?)^[ \t]*\$\$[ \t]*(?:\r?\n|$)/m.exec(source)
      if (!match || match.index !== 0) return undefined
      return { type: 'mathBlock', raw: match[0], text: match[1].replace(/\r?\n$/, '') }
    },
  },
  addKeyboardShortcuts() {
    return {
      Enter: () => {
        const { $from, empty } = this.editor.state.selection
        if (!empty) return false
        if ($from.parent.type.name === 'paragraph' && $from.parent.textContent === '$$' && $from.parentOffset === 2) {
          return this.editor.commands.command(({ tr }) => {
            const pos = $from.before()
            tr.replaceWith(pos, $from.after(), this.type.create())
            tr.setSelection(TextSelection.create(tr.doc, pos + 1))
            return true
          })
        }
        if (this.editor.isActive(this.name)) return this.editor.commands.insertContent('\n')
        return false
      },
      'Mod-Enter': () => this.editor.isActive(this.name) ? this.editor.commands.exitCode() : false,
    }
  },
})

export const MathBlock = MathBlockSchema.extend({
  addNodeView: () => ReactNodeViewRenderer(MathBlockView),
})
